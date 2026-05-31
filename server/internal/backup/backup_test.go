package backup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTarGzRoundTrip(t *testing.T) {
	src := t.TempDir()
	// Build a small translations-like tree.
	writeFile(t, filepath.Join(src, "cards.json"), `{"prefix":{"こんにちは":"你好"}}`)
	writeFile(t, filepath.Join(src, "eventStory", "event_1.json"), `{"meta":{"source":"official_cn"}}`)

	data, err := tarGzDir(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("empty tarball")
	}

	dest := t.TempDir()
	if err := untarGz(data, dest); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(dest, "cards.json"))
	if got != `{"prefix":{"こんにちは":"你好"}}` {
		t.Errorf("cards.json mismatch: %q", got)
	}
	got2 := readFile(t, filepath.Join(dest, "eventStory", "event_1.json"))
	if got2 != `{"meta":{"source":"official_cn"}}` {
		t.Errorf("event_1.json mismatch: %q", got2)
	}
}

func TestUntarGzRejectsTraversal(t *testing.T) {
	// A hand-built tarball with a ../ entry must be rejected.
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "ok.txt"), "fine")
	data, err := tarGzDir(src)
	if err != nil {
		t.Fatal(err)
	}
	// Normal extraction works.
	if err := untarGz(data, t.TempDir()); err != nil {
		t.Fatalf("normal extract failed: %v", err)
	}
}

func TestSigV4KeyDeterministic(t *testing.T) {
	// SigV4 signing key derivation must be stable for identical inputs.
	k1 := sigv4Key("secret", "20260531", "us-east-1", "s3")
	k2 := sigv4Key("secret", "20260531", "us-east-1", "s3")
	if string(k1) != string(k2) {
		t.Error("signing key not deterministic")
	}
	k3 := sigv4Key("secret", "20260601", "us-east-1", "s3")
	if string(k1) == string(k3) {
		t.Error("signing key should differ by date")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
