package backup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"moesekai/server/internal/config"
	"moesekai/server/internal/importer"
)

// materializeTranslations writes the current DB-backed translations into a fresh
// "translations" directory under parent, returning its path. This is the backup
// payload (same layout the consumer site and legacy backups use).
func (m *Manager) materializeTranslations(parent string) (string, error) {
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	// Generator.WriteAll writes <outDir>/translation/...; backups historically
	// used a top-level "translations" dir, so generate then point at it.
	gen := m.gen.WithOutDir(parent)
	if _, err := gen.WriteAll(); err != nil {
		return "", err
	}
	// WriteAll produces parent/translation/...; rename to parent/translations.
	src := filepath.Join(parent, "translation")
	dst := filepath.Join(parent, "translations")
	_ = os.RemoveAll(dst)
	if err := os.Rename(src, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// ---- GitHub backup (git commit/push) ----

func (m *Manager) backupGit() error {
	repoURL := m.cfg.Get(config.KeyBackupGitRepoURL)
	branch := m.cfg.GetOr(config.KeyBackupGitBranch, "backup-translations")
	if strings.TrimSpace(repoURL) == "" {
		return fmt.Errorf("backup git repo url not configured")
	}

	work := filepath.Join(m.workDir, "git-backup")
	_ = os.RemoveAll(work)
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(work)

	repoDir := filepath.Join(work, "repo")
	// Clone the backup branch shallowly. If the branch doesn't exist yet, init
	// a fresh repo on that branch instead.
	if err := git(work, "clone", "--depth", "1", "--branch", branch, repoURL, repoDir); err != nil {
		if err := m.initFreshBackupRepo(repoDir, repoURL, branch); err != nil {
			return fmt.Errorf("clone and init both failed: %w", err)
		}
	}
	if err := git(repoDir, "config", "user.name", "MoeSekai Bot"); err != nil {
		return err
	}
	if err := git(repoDir, "config", "user.email", "bot@moesekai.com"); err != nil {
		return err
	}

	// Replace translations/ with freshly generated data.
	target := filepath.Join(repoDir, "translations")
	_ = os.RemoveAll(target)
	srcDir, err := m.materializeTranslations(work)
	if err != nil {
		return err
	}
	if err := copyDir(srcDir, target); err != nil {
		return err
	}

	if err := git(repoDir, "add", "translations"); err != nil {
		return err
	}
	msg := fmt.Sprintf("chore: backup translations %s", time.Now().UTC().Format("2006-01-02 15:04:05 UTC"))
	if err := git(repoDir, "commit", "-m", msg); err != nil {
		// Nothing to commit is not an error.
		if strings.Contains(err.Error(), "nothing to commit") || strings.Contains(err.Error(), "working tree clean") {
			return nil
		}
		return err
	}
	return git(repoDir, "push", "origin", branch)
}

func (m *Manager) initFreshBackupRepo(repoDir, repoURL, branch string) error {
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		return err
	}
	if err := git(repoDir, "init"); err != nil {
		return err
	}
	if err := git(repoDir, "checkout", "-b", branch); err != nil {
		return err
	}
	return git(repoDir, "remote", "add", "origin", repoURL)
}

func (m *Manager) restoreGit() (importer.Result, error) {
	repoURL := m.cfg.Get(config.KeyBackupGitRepoURL)
	branch := m.cfg.GetOr(config.KeyBackupGitBranch, "backup-translations")
	if strings.TrimSpace(repoURL) == "" {
		return importer.Result{}, fmt.Errorf("backup git repo url not configured")
	}
	work := filepath.Join(m.workDir, "git-restore")
	_ = os.RemoveAll(work)
	if err := os.MkdirAll(work, 0o755); err != nil {
		return importer.Result{}, err
	}
	defer os.RemoveAll(work)

	repoDir := filepath.Join(work, "repo")
	if err := git(work, "clone", "--depth", "1", "--branch", branch, repoURL, repoDir); err != nil {
		return importer.Result{}, err
	}
	src := filepath.Join(repoDir, "translations")
	if err := importer.ValidateDir(src); err != nil {
		return importer.Result{}, err
	}
	return importer.ImportDir(src, m.store, m.eventStr)
}

// git runs a git command in dir with non-interactive credentials.
func git(dir string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, sanitizeGit(strings.TrimSpace(string(out))))
	}
	return nil
}

// sanitizeGit masks any embedded token in git output.
func sanitizeGit(s string) string {
	if i := strings.Index(s, "@github.com"); i > 0 {
		if start := strings.Index(s, "https://"); start >= 0 && start < i {
			return s[:start+8] + "***" + s[i:]
		}
	}
	return s
}

// copyDir recursively copies src to dst.
func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
