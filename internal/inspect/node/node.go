// Package node implements the L1 machine inspector for the Node.js
// ecosystem: node, npm, pnpm, yarn.
//
// It answers only "what does this machine currently have installed"
// for a tool an existing core.Requirement already named — never
// "does it satisfy the requirement" (core.Match, a later PR) and
// never which tools happen to exist on the machine beyond that.
//
// L1 rules, enforced throughout this package:
//
//   - known executable names only (supportedTools);
//   - resolved from a sanitized system search path, never the
//     inherited process PATH (see path.go);
//   - executed directly (exec.CommandContext with the resolved
//     absolute path and the fixed "--version" argument) — no shell,
//     no npm scripts, no lifecycle hooks, no npx, no Corepack, no
//     project code;
//   - run from a neutral working directory, never the project root;
//   - bounded by a hard timeout and bounded captured output.
package node

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/Masterminds/semver/v3"

	"github.com/0xCHANDA/womm/internal/core"
)

// ErrUnsupportedTool marks a Requirement.Name outside the tools this
// Inspector knows how to probe. It is a sentinel: NodeInspector must
// never fall back to resolving or executing an arbitrary name.
var ErrUnsupportedTool = errors.New("unsupported tool for NodeInspector")

// ErrProbeTimeout marks a version probe that exceeded probeTimeout.
// The underlying process is killed; it never leaks past this error.
var ErrProbeTimeout = errors.New("version probe timed out")

// supportedTools are the only executable names this Inspector will
// ever resolve and run. Requirement.Name must match one of these
// exactly — no alias, no path, no argument widens this set.
var supportedTools = map[string]bool{
	"node": true,
	"npm":  true,
	"pnpm": true,
	"yarn": true,
}

const (
	// probeTimeout bounds every version probe. A hanging binary must
	// never hang WOMM.
	probeTimeout = 5 * time.Second
	// maxOutputBytes bounds captured stdout+stderr. Version output is
	// untrusted; a broken or malicious binary must not be able to
	// exhaust memory through it. A real version string is at most a
	// few dozen bytes.
	maxOutputBytes = 4096
)

// NodeInspector inspects the machine for the Node.js ecosystem tools.
// See the package doc for the L1 guarantees it upholds.
type NodeInspector struct {
	// resolvePath resolves a bare tool name to an absolute executable
	// path. Production code always uses resolveSystemPath; tests in
	// this package may substitute it to point at fixtures without
	// touching the real machine or its PATH.
	resolvePath func(name string) (string, error)
	// runDir is the working directory every probe executes from. It
	// must never be the inspected project's root — defense in depth
	// against a binary whose behavior depends on its CWD or on
	// discovering project-local config by walking up from it.
	runDir string
	// timeout bounds each probe. Production code always uses
	// probeTimeout; only same-package tests shrink it, to keep the
	// timeout test fast without weakening the real 5s default.
	timeout time.Duration
}

// NewNodeInspector returns a NodeInspector configured for production
// use: real sanitized-PATH resolution, the system temp directory as a
// neutral working directory, and the full 5-second probe timeout.
func NewNodeInspector() *NodeInspector {
	return &NodeInspector{
		resolvePath: resolveSystemPath,
		runDir:      os.TempDir(),
		timeout:     probeTimeout,
	}
}

// Supports reports whether name is one of the tools this Inspector
// knows how to probe.
func (n *NodeInspector) Supports(name string) bool { return supportedTools[name] }

// Inspect observes the machine for req.Name. See the Inspector
// interface doc for the returned-error contract.
func (n *NodeInspector) Inspect(ctx context.Context, req core.Requirement) (core.Observation, error) {
	if !n.Supports(req.Name) {
		// Refuse before any resolution or execution is attempted:
		// Requirement.Name must never become arbitrary command
		// execution.
		return core.Observation{}, fmt.Errorf("%w: %q", ErrUnsupportedTool, req.Name)
	}

	path, err := n.resolvePath(req.Name)
	if errors.Is(err, errExecutableNotFound) {
		// Not installed is a legitimate machine observation, not an
		// internal failure.
		return core.Observation{Name: req.Name, Present: false}, nil
	}
	if err != nil {
		return core.Observation{}, fmt.Errorf("resolving %s: %w", req.Name, err)
	}

	out, err := n.probe(ctx, path)
	if err != nil {
		return core.Observation{}, fmt.Errorf("inspecting %s: %w", req.Name, err)
	}

	return core.Observation{
		Name:    req.Name,
		Present: true,
		Version: parseVersion(req.Name, out),
	}, nil
}

// probe executes path with the fixed version-query argument and
// returns its bounded combined output. It never uses a shell: the
// resolved absolute path is executed directly, exactly the L1
// contract requires.
func (n *NodeInspector) probe(ctx context.Context, path string) (string, error) {
	timeout := n.timeout
	if timeout <= 0 {
		timeout = probeTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// path is only ever a value resolveSystemPath returned: an
	// absolute path inside the fixed system allowlist. Executed
	// directly, with a fixed argument — no shell involved.
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Dir = n.runDir
	cmd.Env = sanitizedEnv()
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
	cmd.WaitDelay = 2 * time.Second

	out := &boundedWriter{limit: maxOutputBytes}
	cmd.Stdout = out
	cmd.Stderr = out

	err := cmd.Run()
	if err == nil {
		return out.buf.String(), nil
	}
	// cmd.Run failed: distinguish why. exec.CommandContext kills the
	// process as soon as ctx is done, so a failure coinciding with an
	// expired/cancelled context is that, not a genuine exec failure.
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("%w after %s", ErrProbeTimeout, timeout)
	}
	if ctx.Err() != nil {
		return "", fmt.Errorf("version probe cancelled: %w", ctx.Err())
	}
	// The binary exists (we resolved and started it) but failed to
	// run to completion: a genuine execution failure, never
	// reinterpreted as "missing".
	return "", fmt.Errorf("version probe failed: %w", err)
}

// sanitizedEnv builds the child process environment: the current
// process environment with hostile-influence variables replaced.
// The executable itself is always started by its already resolved
// absolute path, so stripping PATH does not affect how it is found;
// it exists so the probed tool's own environment never carries a
// project-tainted PATH either, as defense in depth.
//
// NODE_OPTIONS is stripped for the same reason, one level deeper:
// every probed tool here is ultimately a Node.js process, and
// NODE_OPTIONS=--require <path> (set by a wrapper, a sourced shell
// profile, or any other inherited environment) makes node execute
// arbitrary JS at startup — including project code — before it ever
// prints a version. The L1 guarantee is "never execute project
// code", so a code-execution vector in the inherited environment is
// filtered out regardless of who set it.
func sanitizedEnv() []string {
	env := os.Environ()
	stripped := strippedEnvKeys
	filtered := make([]string, 0, len(env)+len(stripped))
	for _, kv := range env {
		hostile := false
		for _, key := range stripped {
			if strings.HasPrefix(kv, key+"=") {
				hostile = true
				break
			}
		}
		if hostile {
			continue
		}
		filtered = append(filtered, kv)
	}
	return append(filtered, "PATH="+strings.Join(systemPathDirs, string(os.PathListSeparator)))
}

// strippedEnvKeys are the environment variables removed from every
// probe's environment. PATH is replaced (not just dropped) below with
// the sanitized system path; the rest are simply removed.
var strippedEnvKeys = []string{
	"NODE_OPTIONS", // node-only: can inject --require/-e code execution
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

// parseVersion extracts a version from a tool's raw probe output, or
// reports it as unknown ("") when the output cannot be trusted as a
// version. It never guesses: exactly one non-blank line is required,
// and that line must be a strict semantic version (the same
// semver.StrictNewVersion policy the rest of the project already uses
// for exact versions — see detectors/node/nvmrc.go).
func parseVersion(tool, raw string) string {
	lines := nonBlankLines(raw)
	if len(lines) != 1 {
		return ""
	}
	candidate := lines[0]
	if tool == "node" {
		// The only tool with a documented "v" prefix convention
		// (node --version → "v24.7.0"); npm/pnpm/yarn report the bare
		// number. Stripping it elsewhere would be guessing.
		candidate = strings.TrimPrefix(candidate, "v")
	}
	v, err := semver.StrictNewVersion(candidate)
	if err != nil {
		return ""
	}
	return v.String()
}

// nonBlankLines splits s on newlines and returns the trimmed
// non-blank ones, preserving order.
func nonBlankLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			out = append(out, t)
		}
	}
	return out
}
