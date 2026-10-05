// Package probe is the one place where an L1 inspector starts a process.
// Every tool WOMM inspects (node, npm, pnpm, yarn, go, ...) is run
// through Run, so every ecosystem gets the same containment:
//
//   - the resolved absolute path is executed directly with fixed
//     arguments — no shell, no project code;
//   - from a working directory the caller chose (never the project);
//   - with exactly the environment the caller built (no inheritance
//     happens here);
//   - bounded in time (timeout, then a process-group SIGKILL, then a
//     WaitDelay backstop on the output pipes);
//   - bounded in output (MaxOutputBytes retained, the rest discarded);
//   - in its own process group, with WOMM as child subreaper so a
//     descendant that escaped the group (setsid) is adopted and killed
//     (reaper_linux.go).
//
// It decides nothing about the output: parsing a version out of it is
// the inspector's job.
package probe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// ErrTimeout marks a probe that exceeded its timeout. The underlying
// process group is killed; nothing leaks past this error.
var ErrTimeout = errors.New("version probe timed out")

const (
	// DefaultTimeout bounds every version probe. A hanging binary must
	// never hang WOMM.
	DefaultTimeout = 5 * time.Second
	// WaitDelay bounds how long Wait may block on the output pipes
	// after the process group was killed: a descendant that escaped
	// the group (setsid) can keep the pipe open, and WOMM must still
	// return. Such a process is then killed as an adopted child (see
	// reaper_linux.go); either way it can never hold WOMM.
	WaitDelay = 2 * time.Second
	// MaxOutputBytes bounds captured stdout+stderr. Version output is
	// untrusted; a broken or malicious binary must not be able to
	// exhaust memory through it. A real version string is at most a
	// few dozen bytes.
	MaxOutputBytes = 4096
	// Dir is the working directory of every production probe: the
	// filesystem root. It has no ancestors at all, so nothing a tool
	// walks up to (.yarnrc, .npmrc, package.json, go.mod, .tool-versions)
	// can be planted by anyone but root, and it is never the project.
	// os.TempDir() would be wrong on both counts: /tmp is world-writable
	// and $TMPDIR is inherited verbatim (a relative value points into
	// the project).
	Dir = "/"
)

// Spec describes one probe.
type Spec struct {
	// Path is the absolute executable, already resolved and vetted by
	// internal/inspect/toolpath.
	Path string
	// Args are the fixed arguments ("--version", "version").
	Args []string
	// Dir is the working directory; see the package Dir constant.
	Dir string
	// Env is the complete child environment.
	Env []string
	// Timeout bounds the probe; zero or negative means DefaultTimeout.
	Timeout time.Duration
}

// Run starts the probe and returns its bounded combined output. A
// failure after the process was started is an error that is never
// reinterpreted as "missing": the caller knows presence was
// established when it resolved the path.
func Run(ctx context.Context, s Spec) (string, error) {
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	becomeSubreaper()
	cmd := exec.CommandContext(ctx, s.Path, s.Args...)
	cmd.Dir = s.Dir
	cmd.Env = s.Env
	cmd.Stdin = nil

	// Run the probe in its own process group. If the resolved binary
	// is itself a wrapper that forks a further process (a shim, an
	// nvm/asdf launcher script, ...), signalling only the immediate
	// child on timeout/cancellation would leave that descendant
	// running — an orphan that keeps the output pipe open and hangs
	// Wait() until it exits on its own, defeating the whole point of
	// the timeout. Killing the negative PID kills the whole group.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	// Belt and suspenders: even if the group kill above somehow fails
	// to close every descendant's copy of the pipe, Wait must not
	// block forever on it.
	cmd.WaitDelay = WaitDelay

	out := &boundedWriter{limit: MaxOutputBytes}
	cmd.Stdout = out
	cmd.Stderr = out

	err := cmd.Run()
	// The probe is reaped (and on failure its group killed). Whatever
	// left the group (setsid) was orphaned by that and is now WOMM's
	// own child: kill it, on the success path too — a probe must not
	// leave processes behind. Best effort, bounded; the timeout
	// guarantee above never depends on it.
	killAdoptedDescendants()
	if err == nil {
		return out.buf.String(), nil
	}
	// cmd.Run failed: distinguish why. exec.CommandContext kills the
	// process as soon as ctx is done, so a failure coinciding with an
	// expired/cancelled context is that, not a genuine exec failure.
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("%w after %s", ErrTimeout, timeout)
	}
	if ctx.Err() != nil {
		return "", fmt.Errorf("version probe cancelled: %w", ctx.Err())
	}
	// The binary exists (we resolved and started it) but failed to
	// run to completion: a genuine execution failure.
	return "", fmt.Errorf("version probe failed: %w", err)
}

// boundedWriter caps how many bytes it will actually retain; bytes
// beyond limit are accepted (so the child is never blocked writing
// into a full pipe, which could otherwise stall until the timeout
// fires) but discarded. This is the defense against a broken or
// malicious binary trying to exhaust memory through its own output.
type boundedWriter struct {
	buf   bytes.Buffer
	limit int
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if remaining := w.limit - w.buf.Len(); remaining > 0 {
		if len(p) < remaining {
			remaining = len(p)
		}
		w.buf.Write(p[:remaining])
	}
	return len(p), nil
}
