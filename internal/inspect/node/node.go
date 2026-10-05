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
//   - resolved from an ordered, validated directory list (explicit
//     user directories, then the system directories), never the
//     inherited process PATH (see internal/inspect/toolpath);
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
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Masterminds/semver/v3"

	"github.com/0xCHANDA/womm/internal/core"
	"github.com/0xCHANDA/womm/internal/inspect/toolpath"
)

// errExecutableNotFound reports that a tool name could not be resolved
// in the configured directories. It is a sentinel distinguishing "not
// installed" (a legitimate Observation, never an error) from a genuine
// resolution failure such as toolpath.ErrUnsafe.
var errExecutableNotFound = toolpath.ErrNotFound

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
	// waitDelay bounds how long Wait may block on the output pipes
	// after the process group was killed: a descendant that escaped
	// the group (setsid) can keep the pipe open, and WOMM must still
	// return. Such a process is then killed as an adopted child (see
	// reaper_linux.go); either way it can never hold WOMM.
	waitDelay = 2 * time.Second
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
	// path. Production code always uses a toolpath.Resolver; tests in
	// this package may substitute it to point at fixtures without
	// touching the real machine or its PATH.
	resolvePath func(name string) (string, error)
	// runDir is the working directory every probe executes from. It
	// must never be the inspected project's root, and must have no
	// ancestor an unprivileged user can write to: yarn 1 walks up from
	// the cwd looking for .yarnrc `yarn-path` and executes that file
	// even for --version; pnpm walks up for a package.json
	// `packageManager` and downloads+runs that version. A shared
	// temp dir (/tmp) or an inherited $TMPDIR would hand both of them
	// an attacker-chosen ancestor. See probeDir.
	runDir string
	// timeout bounds each probe. Production code always uses
	// probeTimeout; only same-package tests shrink it, to keep the
	// timeout test fast without weakening the real 5s default.
	timeout time.Duration
}

// probeDir is the working directory of every production probe: the
// filesystem root. It has no ancestors at all, so nothing a tool walks
// up to (.yarnrc, .yarnrc.yml, .npmrc, package.json) can be planted by
// anyone but root, and it is never the project. os.TempDir() was the
// previous choice and is wrong on both counts: /tmp is world-writable
// and $TMPDIR is inherited verbatim (a relative value points into the
// project).
const probeDir = "/"

// NewNodeInspector returns a NodeInspector configured for production
// use over the system directories only: the filesystem root as the
// working directory and the full 5-second probe timeout.
func NewNodeInspector() *NodeInspector { return NewNodeInspectorWith(toolpath.System()) }

// NewNodeInspectorWith is NewNodeInspector over the given resolver,
// which decides where executables come from (explicit user directories
// before the system ones). Everything else — cwd, timeout, environment,
// output cap — is identical for every resolver.
func NewNodeInspectorWith(r *toolpath.Resolver) *NodeInspector {
	return &NodeInspector{
		resolvePath: r.Resolve,
		runDir:      probeDir,
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
		// Presence was established above (the binary resolved and
		// was started); only the query failed. Return that knowledge
		// as a partial observation alongside the error, so a caller
		// can tell "present but unqueryable" (unreachable) apart
		// from "nothing is known" — see inspect.Inspector. The path is
		// part of that knowledge: which binary failed to answer.
		return core.Observation{Name: req.Name, Present: true, Path: path}, fmt.Errorf("inspecting %s: %w", req.Name, err)
	}

	return core.Observation{
		Name:    req.Name,
		Present: true,
		Version: parseVersion(req.Name, out),
		Path:    path,
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

	// path is only ever a value the resolver returned: an absolute path
	// inside the validated directory list. Executed directly, with a
	// fixed argument — no shell involved.
	becomeSubreaper()
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Dir = n.runDir
	cmd.Env = sanitizedEnvFor(path)
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
	cmd.WaitDelay = waitDelay

	out := &boundedWriter{limit: maxOutputBytes}
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
func sanitizedEnv() []string { return sanitizedEnvFor("") }

// sanitizedEnvFor is sanitizedEnv for a probe of execPath: an ALLOWLIST.
// The probe inherits only locale settings; every other variable it sees
// is forced below. A denylist cannot work here: version-manager shims
// (Volta, mise, asdf, nodenv, fnm, ...) are dispatchers that pick the
// binary to run from VOLTA_HOME, MISE_*, ASDF_*, XDG_DATA_HOME and the
// like, any of which a launcher (direnv, make, `npm run`) can point into
// the project — project code execution behind a legitimate-looking
// path, and a PASS. The same holds for every future tool whose
// configuration lives in the environment. The cost is stated: a tool
// that only starts with a custom variable (Nix-style wrappers) is
// reported UNREACHABLE, honestly.
//
// A binary from a user-named directory may be a launcher that finds its
// sibling tools through PATH — npm and pnpm from nvm, fnm or Volta are
// `#!/usr/bin/env node` scripts — and with the system directories alone
// they would silently run on the system node (or fail to start). So
// that directory, already validated by the resolver, goes first. System
// binaries keep exactly the fixed system PATH.
func sanitizedEnvFor(execPath string) []string {
	env := os.Environ()
	out := make([]string, 0, len(forcedEnv)+4)
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		if key == "LANG" || key == "LANGUAGE" || strings.HasPrefix(key, "LC_") {
			out = append(out, kv)
		}
	}
	out = append(out, forcedEnv...)
	return append(out, "PATH="+probePath(execPath))
}

// probePath is the PATH of a probe: the executable's own directory when
// it is not a system directory, then the system directories.
func probePath(execPath string) string {
	dirs := append([]string(nil), toolpath.SystemDirs...)
	if execPath != "" {
		dir := filepath.Dir(execPath)
		system := false
		for _, d := range dirs {
			if d == dir {
				system = true
				break
			}
		}
		if !system {
			dirs = append([]string{dir}, dirs...)
		}
	}
	return strings.Join(dirs, string(os.PathListSeparator))
}

// forcedEnv are the variables every probe gets with fixed values (PATH
// is set per probe, see probePath).
//
// On many machines /usr/local/bin/pnpm and /usr/local/bin/yarn are
// Corepack shims: "pnpm --version" may download pnpm from the network
// before answering, and may rewrite the packageManager field of a
// package.json it finds by walking up from the working directory.
// A version probe must observe the machine, never change it or reach
// out of it, so both behaviors are disabled: with the network off, an
// uncached shim fails fast and the target is reported as unreachable
// — truthfully, WOMM could not query it.
//
// YARN_IGNORE_PATH=1 makes yarn 1 ignore any `yarn-path` it finds;
// npm_config_manage_package_manager_versions=false makes pnpm answer
// with the binary that was actually invoked instead of fetching the
// version an ancestor package.json pins. Together with probeDir these
// are belt and suspenders: the cwd has no ancestors to walk, and the
// walkers are told not to act on what they would find.
var forcedEnv = []string{
	"COREPACK_ENABLE_NETWORK=0",
	"COREPACK_ENABLE_AUTO_PIN=0",
	"COREPACK_ENABLE_STRICT=0",
	"YARN_IGNORE_PATH=1",
	"npm_config_manage_package_manager_versions=false",
	"HOME=" + accountHome,
	"COREPACK_HOME=" + accountHome + "/.cache/node/corepack",
}

// accountHome is the home directory of the account WOMM runs as,
// taken from the user database (see toolpath.AccountHome) — never from
// the inherited $HOME, which a launcher can point into a project. Corepack shims resolve their cache from it and package
// managers read their user config from it; both must be the user's
// own. When the account has no home directory a non-existent path is
// used: cached shims then fail fast and are reported as unreachable
// rather than run from an attacker-chosen directory.
var accountHome = resolveAccountHome()

func resolveAccountHome() string {
	if home := toolpath.AccountHome(); home != "" {
		return home
	}
	return "/nonexistent"
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
