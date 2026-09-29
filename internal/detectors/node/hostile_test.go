package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// detectWithin runs the Node detector and fails the test if it does not
// return within d: a blocked read is a hang, not a slow test.
func detectWithin(t *testing.T, dir string, d time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := NewNodeDetector().Detect(context.Background(), dir)
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		t.Fatalf("Detect did not return within %s (blocked read?)", d)
		return nil
	}
}

func TestDetectorRefusesFIFOSource(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "package.json"), 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	err := detectWithin(t, dir, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("error = %v, want a not-a-regular-file refusal", err)
	}
}

func TestDetectorRefusesDirectorySource(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".nvmrc"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := detectWithin(t, dir, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("error = %v, want a not-a-regular-file refusal", err)
	}
}

func TestDetectorRefusesOversizedSource(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Sparse: one byte past the limit without writing the payload.
	if err := f.Truncate(maxSourceBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	err = detectWithin(t, dir, 10*time.Second)
	if !errors.Is(err, errSourceTooLarge) {
		t.Fatalf("error = %v, want errSourceTooLarge", err)
	}

	// Exactly at the limit is read (and then fails as JSON, not size).
	if err := os.Truncate(filepath.Join(dir, "package.json"), maxSourceBytes); err != nil {
		t.Fatal(err)
	}
	err = detectWithin(t, dir, 10*time.Second)
	if err == nil || errors.Is(err, errSourceTooLarge) || !strings.Contains(err.Error(), "malformed package.json") {
		t.Fatalf("error = %v, want a malformed-JSON error at exactly the limit", err)
	}
}

func TestDetectorStripsUTF8BOM(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"engines":{"node":">=22 <25"},"packageManager":"npm@11.2.0"}`)...), 0o644); err != nil {
		t.Fatal(err)
	}
	reqs, err := NewNodeDetector().Detect(context.Background(), dir)
	if err != nil || len(reqs) != 1 || reqs[0].Constraint != ">=22 <25" {
		t.Fatalf("BOM package.json: reqs=%+v err=%v", reqs, err)
	}
	pm, err := NewPackageManagerDetector().Detect(context.Background(), dir)
	if err != nil || len(pm) != 1 || pm[0].Name != "npm" {
		t.Fatalf("BOM package.json (packageManager): reqs=%+v err=%v", pm, err)
	}

	// Only the exact UTF-8 BOM is stripped; other leading bytes stay
	// malformed JSON.
	if err := os.WriteFile(filepath.Join(dir, "package.json"), append([]byte{0xFF, 0xFE}, []byte(`{}`)...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewNodeDetector().Detect(context.Background(), dir); err == nil || !strings.Contains(err.Error(), "malformed package.json") {
		t.Fatalf("UTF-16 BOM: err=%v, want malformed", err)
	}
}

func TestDetectorNormalizesEnginesWhitespace(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(dir, "package.json", "{\"engines\":{\"node\":\"  >=22\\t<25 \\n||\\n ^20.10.0 \"}}"); err != nil {
		t.Fatal(err)
	}
	reqs, err := NewNodeDetector().Detect(context.Background(), dir)
	if err != nil || len(reqs) != 1 {
		t.Fatalf("reqs=%+v err=%v", reqs, err)
	}
	if got, want := reqs[0].Constraint, ">=22 <25 || ^20.10.0"; got != want {
		t.Errorf("constraint = %q, want normalized %q", got, want)
	}
	if got, want := reqs[0].Evidence[0].Value, "  >=22\t<25 \n||\n ^20.10.0 "; got != want {
		t.Errorf("evidence value = %q, want verbatim %q", got, want)
	}
}

func TestDetectorUnreadableSourceIsAnError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read anything; permission refusal cannot be exercised")
	}
	dir := t.TempDir()
	if err := writeFile(dir, ".nvmrc", "v24.7.0\n"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, ".nvmrc"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, ".nvmrc"), 0o644) })
	_, err := NewNodeDetector().Detect(context.Background(), dir)
	if err == nil || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("error = %v, want a permission error (never silent absence)", err)
	}
}
