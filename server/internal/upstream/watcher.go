// Package upstream watches the JP masterdata source for new game data and
// triggers a CN sync when it changes. Instead of polling the GitHub commits API
// (which is rate-limited — see the project notes), it fetches the raw
// versions/current_version.json and compares dataVersion. Optionally it keeps a
// local git clone of the masterdata repo refreshed (git pull) so future work can
// read masterdata from disk without hammering the API.
package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"moesekai/server/internal/config"
)

// VersionInfo is the subset of current_version.json we care about.
type VersionInfo struct {
	AppVersion   string `json:"appVersion"`
	DataVersion  string `json:"dataVersion"`
	AssetVersion string `json:"assetVersion"`
}

// SyncFn runs a CN sync. Returning an error keeps the change pending for retry.
type SyncFn func() error

// Status reports the watcher's state for the admin UI.
type Status struct {
	Enabled          bool   `json:"enabled"`
	Repo             string `json:"repo"`
	Branch           string `json:"branch"`
	LastCheck        string `json:"lastCheck,omitempty"`
	LastDataVersion  string `json:"lastDataVersion,omitempty"`
	ChangeDetectedAt string `json:"changeDetectedAt,omitempty"`
	LastSync         string `json:"lastSync,omitempty"`
	LastError        string `json:"lastError,omitempty"`
	GitMirrorReady   bool   `json:"gitMirrorReady"`
}

// Watcher polls current_version.json and triggers sync on dataVersion change.
type Watcher struct {
	cfg      *config.Config
	syncFn   SyncFn
	client   *http.Client
	interval time.Duration
	gitDir   string // local clone path; empty disables the git mirror
	useGit   bool

	mu     sync.Mutex
	status Status

	stopCh chan struct{}
}

// Options configures the watcher.
type Options struct {
	Interval time.Duration // poll interval (default 5m)
	GitDir   string        // local masterdata clone dir; "" disables git mirror
	UseGit   bool          // whether to maintain the git mirror
}

func New(cfg *config.Config, syncFn SyncFn, opts Options) *Watcher {
	interval := opts.Interval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	return &Watcher{
		cfg:      cfg,
		syncFn:   syncFn,
		client:   &http.Client{Timeout: 20 * time.Second},
		interval: interval,
		gitDir:   opts.GitDir,
		useGit:   opts.UseGit && opts.GitDir != "",
		stopCh:   make(chan struct{}),
	}
}

// rawVersionURL builds the raw current_version.json URL for the configured repo.
func (w *Watcher) rawVersionURL() string {
	repo := w.cfg.GetOr(config.KeyUpstreamRepo, "Team-Haruki/haruki-sekai-master")
	branch := w.cfg.GetOr(config.KeyUpstreamBranch, "main")
	return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/versions/current_version.json", repo, branch)
}

// Start launches the polling loop unless disabled in config.
func (w *Watcher) Start() {
	if !w.cfg.GetBool(config.KeySchedulerOn, true) {
		fmt.Println("[upstream] scheduler disabled by config")
		w.setStatus(func(s *Status) { s.Enabled = false })
		return
	}
	w.setStatus(func(s *Status) {
		s.Enabled = true
		s.Repo = w.cfg.GetOr(config.KeyUpstreamRepo, "Team-Haruki/haruki-sekai-master")
		s.Branch = w.cfg.GetOr(config.KeyUpstreamBranch, "main")
	})
	if w.useGit {
		go w.ensureGitMirror()
	}
	go w.loop()
}

func (w *Watcher) Stop() { close(w.stopCh) }

func (w *Watcher) Status() Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

func (w *Watcher) setStatus(fn func(*Status)) {
	w.mu.Lock()
	fn(&w.status)
	w.mu.Unlock()
}

func (w *Watcher) loop() {
	// Record the initial version without triggering a sync (avoids a sync on
	// every restart). A change relative to this baseline triggers work.
	w.check(true)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.check(false)
		}
	}
}

// CheckNow runs an immediate check (admin "check now" button). When force is
// true it triggers a sync even if the version is unchanged.
func (w *Watcher) CheckNow(force bool) (Status, error) {
	changed, err := w.fetchAndCompare()
	if err != nil {
		return w.Status(), err
	}
	if changed || force {
		w.runSync()
	}
	return w.Status(), nil
}

func (w *Watcher) check(baseline bool) {
	changed, err := w.fetchAndCompare()
	if err != nil {
		fmt.Printf("[upstream] check failed: %v\n", err)
		return
	}
	if baseline {
		fmt.Printf("[upstream] baseline dataVersion=%s\n", w.Status().LastDataVersion)
		return
	}
	if changed {
		w.runSync()
	}
}

// fetchAndCompare fetches the version file and updates status, returning whether
// dataVersion changed since the last observed value.
func (w *Watcher) fetchAndCompare() (bool, error) {
	info, err := w.fetchVersion()
	if err != nil {
		w.setStatus(func(s *Status) {
			s.LastCheck = nowRFC3339()
			s.LastError = err.Error()
		})
		return false, err
	}
	changed := false
	w.setStatus(func(s *Status) {
		s.LastCheck = nowRFC3339()
		s.LastError = ""
		if s.LastDataVersion != "" && s.LastDataVersion != info.DataVersion {
			changed = true
			s.ChangeDetectedAt = nowRFC3339()
		}
		s.LastDataVersion = info.DataVersion
	})
	if changed {
		fmt.Printf("[upstream] dataVersion changed -> %s\n", info.DataVersion)
	}
	return changed, nil
}

func (w *Watcher) fetchVersion() (VersionInfo, error) {
	var info VersionInfo
	req, err := http.NewRequest(http.MethodGet, w.rawVersionURL(), nil)
	if err != nil {
		return info, err
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return info, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return info, fmt.Errorf("version fetch http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return info, err
	}
	if info.DataVersion == "" {
		return info, fmt.Errorf("current_version.json missing dataVersion")
	}
	return info, nil
}

// runSync refreshes the git mirror (if enabled) then runs the CN sync.
func (w *Watcher) runSync() {
	if w.useGit {
		if err := w.pullGitMirror(); err != nil {
			fmt.Printf("[upstream] git mirror pull failed (continuing with HTTP sync): %v\n", err)
		}
	}
	if w.syncFn == nil {
		return
	}
	if err := w.syncFn(); err != nil {
		fmt.Printf("[upstream] sync failed: %v\n", err)
		w.setStatus(func(s *Status) { s.LastError = err.Error() })
		return
	}
	w.setStatus(func(s *Status) {
		s.LastSync = nowRFC3339()
		s.LastError = ""
	})
	fmt.Println("[upstream] sync completed after upstream change")
}

// ---- git mirror (optional) ----

func (w *Watcher) repoURL() string {
	repo := w.cfg.GetOr(config.KeyUpstreamRepo, "Team-Haruki/haruki-sekai-master")
	return fmt.Sprintf("https://github.com/%s.git", repo)
}

// ensureGitMirror clones the masterdata repo on first run (shallow), or marks
// the mirror ready if it already exists.
func (w *Watcher) ensureGitMirror() {
	if w.gitDir == "" {
		return
	}
	gitPath := filepath.Join(w.gitDir, ".git")
	if _, err := os.Stat(gitPath); err == nil {
		w.setStatus(func(s *Status) { s.GitMirrorReady = true })
		return
	}
	if err := os.MkdirAll(filepath.Dir(w.gitDir), 0o755); err != nil {
		fmt.Printf("[upstream] git mirror mkdir failed: %v\n", err)
		return
	}
	branch := w.cfg.GetOr(config.KeyUpstreamBranch, "main")
	fmt.Printf("[upstream] cloning masterdata mirror (shallow) -> %s\n", w.gitDir)
	if err := runGit(w.gitDir, true, "clone", "--depth", "1", "--branch", branch, w.repoURL(), w.gitDir); err != nil {
		fmt.Printf("[upstream] git clone failed: %v\n", err)
		return
	}
	w.setStatus(func(s *Status) { s.GitMirrorReady = true })
	fmt.Println("[upstream] git mirror ready")
}

// pullGitMirror fast-forwards the local mirror. Clones first if absent.
func (w *Watcher) pullGitMirror() error {
	if w.gitDir == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(w.gitDir, ".git")); err != nil {
		w.ensureGitMirror()
		return nil
	}
	branch := w.cfg.GetOr(config.KeyUpstreamBranch, "main")
	return runGit(w.gitDir, false, "pull", "--ff-only", "origin", branch)
}

// runGit runs a git command. When isClone is true, dir is the parent (the clone
// target is in args); otherwise dir is the repo working directory.
func runGit(dir string, isClone bool, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	if isClone {
		cmd.Dir = filepath.Dir(dir)
	} else {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }
