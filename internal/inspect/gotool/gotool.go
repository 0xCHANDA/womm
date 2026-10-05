// Package gotool implements the L1 machine inspector for the Go
// toolchain: it answers "what Go does this machine have" for a
// core.Requirement named "go", with one fixed probe, `go version`.
//
// It shares every L1 guarantee with the Node inspector because it shares
// the code: executable resolution (internal/inspect/toolpath), process
// containment (internal/inspect/probe) and the allowlisted environment.
// On top of that, Go-specific hardening:
//
//   - GOTOOLCHAIN=local: the go command never downloads or switches to
//     another toolchain (it would otherwise, on a module's say-so);
//   - GOENV=off: the user's go env file (which can set GOTOOLCHAIN,
//     GOFLAGS...) is not read, and GOFLAGS and every other inherited
//     variable never reach the probe (allowlisted environment);
//   - telemetry is redirected to a path that cannot exist
//     (TEST_TELEMETRY_DIR=/dev/null/...): since Go 1.23 even `go version`
//     writes counter files under $HOME/.config/go/telemetry — a probe
//     never mutates the machine — and with telemetry mode "on" the go
//     command may start an upload process. Pinned by a test that runs the
//     real go command and asserts it writes nothing.
//   - cwd is the filesystem root: no go.mod, go.work or toolchain file
//     can be found by walking up from it.
package gotool

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/0xCHANDA/womm/internal/core"
	"github.com/0xCHANDA/womm/internal/goversion"
	"github.com/0xCHANDA/womm/internal/inspect/probe"
	"github.com/0xCHANDA/womm/internal/inspect/toolpath"
)

// ErrUnsupportedTool marks a Requirement.Name other than "go". The
// inspector must never fall back to resolving or executing an arbitrary
// name.
var ErrUnsupportedTool = errors.New("unsupported tool for GoInspector")

// ToolName is the only name this inspector supports.
const ToolName = "go"

// telemetryDir is a path that cannot be created, not even by root: a
// directory below /dev/null.
const telemetryDir = "/dev/null/womm-no-go-telemetry"

// GoInspector inspects the machine for the Go toolchain.
type GoInspector struct {
	resolvePath func(name string) (string, error)
	runDir      string
	timeout     time.Duration
}

// NewGoInspector returns a GoInspector over the system directories only.
func NewGoInspector() *GoInspector { return NewGoInspectorWith(toolpath.System()) }

// NewGoInspectorWith is NewGoInspector over the given resolver (explicit
// user directories, system directories, account shim directories).
func NewGoInspectorWith(r *toolpath.Resolver) *GoInspector {
	return &GoInspector{resolvePath: r.Resolve, runDir: probe.Dir, timeout: probe.DefaultTimeout}
}

// Supports reports whether name is "go".
func (g *GoInspector) Supports(name string) bool { return name == ToolName }

// Inspect observes the machine for req.Name. See inspect.Inspector for
// the returned-error contract.
func (g *GoInspector) Inspect(ctx context.Context, req core.Requirement) (core.Observation, error) {
	if !g.Supports(req.Name) {
		return core.Observation{}, fmt.Errorf("%w: %q", ErrUnsupportedTool, req.Name)
	}
	path, err := g.resolvePath(req.Name)
	if errors.Is(err, toolpath.ErrNotFound) {
		return core.Observation{Name: req.Name, Present: false}, nil
	}
	if err != nil {
		return core.Observation{}, fmt.Errorf("resolving %s: %w", req.Name, err)
	}
	out, err := probe.Run(ctx, probe.Spec{
		Path:    path,
		Args:    []string{"version"},
		Dir:     g.runDir,
		Env:     goEnv(path, probe.AccountHome()),
		Timeout: g.timeout,
	})
	if err != nil {
		// Presence was established (the binary resolved and started);
		// only the query failed.
		return core.Observation{Name: req.Name, Present: true, Path: path}, fmt.Errorf("inspecting %s: %w", req.Name, err)
	}
	v, _ := goversion.ParseVersionOutput(out) // "" when it cannot be trusted as a version
	return core.Observation{Name: req.Name, Present: true, Version: v, Path: path}, nil
}

// goEnv is the environment of a `go version` probe of execPath: the
// shared allowlist plus the Go-specific forced values.
func goEnv(execPath, home string) []string {
	return probe.Env(execPath, []string{
		"HOME=" + home,
		"GOTOOLCHAIN=local",
		"GOENV=off",
		"TEST_TELEMETRY_DIR=" + telemetryDir,
	})
}
