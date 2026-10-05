package probe

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func script(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func spec(path string, timeout time.Duration) Spec {
	return Spec{Path: path, Args: []string{"--version"}, Dir: "/", Env: []string{"PATH=/usr/bin:/bin"}, Timeout: timeout}
}

func TestBoundedWriterEnforcesLimit(t *testing.T) {
	w := &boundedWriter{limit: 16}
	big := strings.Repeat("A", 10_000)
	n, err := w.Write([]byte(big))
	if err != nil || n != len(big) {
		t.Fatalf("Write = %d, %v; must accept everything so the writer is never blocked", n, err)
	}
	if w.buf.String() != strings.Repeat("A", 16) {
		t.Fatalf("buffered %q, want exactly the first 16 bytes", w.buf.String())
	}
}

func TestRunReturnsBoundedOutput(t *testing.T) {
	out, err := Run(context.Background(), spec(script(t, "echo v1.2.3\n"), 0))
	if err != nil || out != "v1.2.3\n" {
		t.Fatalf("Run = %q, %v", out, err)
	}
	out, err = Run(context.Background(), spec(script(t, "head -c 100000 /dev/zero | tr '\\0' 'A'\n"), 0))
	if err != nil || len(out) != MaxOutputBytes {
		t.Fatalf("Run returned %d bytes (err %v), want exactly the %d-byte cap", len(out), err, MaxOutputBytes)
	}
}

func TestRunUsesExactlyTheGivenEnvironmentArgsAndDir(t *testing.T) {
	t.Setenv("WOMM_PROBE_INHERITED", "leaked")
	p := script(t, "echo \"$# $1 $PWD ${WOMM_PROBE_INHERITED:-none} $FORCED\"\n")
	s := spec(p, 0)
	s.Env = append(s.Env, "FORCED=yes")
	dir := t.TempDir()
	s.Dir = dir
	out, err := Run(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	if got := strings.TrimSpace(out); got != "1 --version "+dir+" none yes" && got != "1 --version "+real+" none yes" {
		t.Fatalf("probe saw %q; Run must not inherit anything and must use the given Dir/Args", got)
	}
}

func TestRunTimesOutAndKillsTheWholeGroup(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "alive")
	p := script(t, "( while :; do date +%s%N >> '"+marker+"'; sleep 0.05; done ) &\nsleep 30\n")
	start := time.Now()
	_, err := Run(context.Background(), spec(p, 150*time.Millisecond))
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("error = %v, want ErrTimeout", err)
	}
	if d := time.Since(start); d > 150*time.Millisecond+WaitDelay+2*time.Second {
		t.Fatalf("took %s", d)
	}
	time.Sleep(200 * time.Millisecond)
	before, _ := os.ReadFile(marker)
	time.Sleep(300 * time.Millisecond)
	after, _ := os.ReadFile(marker)
	if len(after) != len(before) {
		t.Fatal("a descendant in the probe's process group survived the timeout")
	}
}

func TestRunCancellationIsNotATimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	_, err := Run(ctx, spec(script(t, "sleep 30\n"), 0))
	if err == nil || errors.Is(err, ErrTimeout) || !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want a cancellation", err)
	}
}

func TestRunFailureIsAnErrorNotAbsence(t *testing.T) {
	if _, err := Run(context.Background(), spec(script(t, "echo boom >&2\nexit 7\n"), 0)); err == nil || !strings.Contains(err.Error(), "version probe failed") {
		t.Fatalf("error = %v", err)
	}
}

// A descendant that leaves the process group (setsid) must neither hold
// Run past timeout+WaitDelay nor outlive it.
func TestRunKillsAnEscapedDescendantOnSuccessToo(t *testing.T) {
	log := filepath.Join(t.TempDir(), "beat")
	p := script(t, "setsid sh -c 'while :; do date +%s%N >> \""+log+"\"; sleep 0.05; done' >/dev/null 2>&1 &\necho 1.0.0\n")
	out, err := Run(context.Background(), spec(p, 0))
	if err != nil || strings.TrimSpace(out) != "1.0.0" {
		t.Fatalf("Run = %q, %v", out, err)
	}
	time.Sleep(200 * time.Millisecond)
	a, _ := os.ReadFile(log)
	time.Sleep(300 * time.Millisecond)
	b, _ := os.ReadFile(log)
	if len(a) != len(b) {
		t.Fatal("an escaped (setsid) descendant survived a successful probe")
	}
}
