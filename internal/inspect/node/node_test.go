package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xCHANDA/womm/internal/core"
)

// --- fixtures -----------------------------------------------------

// writeFakeTool creates an executable shell script at dir/name and
// returns its absolute path. Tests use this instead of depending on
// any real node/npm/pnpm/yarn being installed on the machine running
// the tests.
func writeFakeTool(t *testing.T, dir, name, script string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatalf("writing fake %s: %v", name, err)
	}
	return path
}

// staticResolver returns a resolvePath stub that answers only for the
// names in paths and reports errExecutableNotFound for everything
// else — simulating "some tools installed, some not" without ever
// touching a real system PATH.
func staticResolver(paths map[string]string) func(string) (string, error) {
	return func(name string) (string, error) {
		if p, ok := paths[name]; ok {
			return p, nil
		}
		return "", errExecutableNotFound
	}
}

// spyResolver wraps inner and records every name it is asked to
// resolve, so tests can prove which tools were actually probed (and,
// just as importantly, which were not).
func spyResolver(inner func(string) (string, error)) (func(string) (string, error), *[]string) {
	calls := new([]string)
	return func(name string) (string, error) {
		*calls = append(*calls, name)
		return inner(name)
	}, calls
}

func newTestInspector(resolve func(string) (string, error), runDir string) *NodeInspector {
	return &NodeInspector{resolvePath: resolve, runDir: runDir, timeout: probeTimeout}
}

// --- normal behavior ------------------------------------------------

func TestInspectPresentTools(t *testing.T) {
	dir := t.TempDir()
	tools := map[string]struct {
		script  string
		wantVer string
	}{
		"node": {"echo v24.7.0\n", "24.7.0"},  // node's documented "v" prefix
		"npm":  {"echo 10.9.2\n", "10.9.2"},   // npm reports the bare number
		"pnpm": {"echo 10.15.1\n", "10.15.1"}, // pnpm reports the bare number
		"yarn": {"echo 4.6.0\n", "4.6.0"},     // yarn (berry) reports the bare number
	}

	paths := map[string]string{}
	for name, tc := range tools {
		paths[name] = writeFakeTool(t, dir, name, tc.script)
	}
	insp := newTestInspector(staticResolver(paths), t.TempDir())

	for name, tc := range tools {
		t.Run(name, func(t *testing.T) {
			obs, err := insp.Inspect(context.Background(), core.Requirement{Name: name})
			if err != nil {
				t.Fatalf("Inspect(%s) error: %v", name, err)
			}
			want := core.Observation{Name: name, Present: true, Version: tc.wantVer}
			if obs != want {
				t.Errorf("Inspect(%s) = %+v, want %+v", name, obs, want)
			}
		})
	}
}

// --- missing binaries -------------------------------------------------

func TestInspectMissingTools(t *testing.T) {
	// No paths registered at all: every supported tool is "not
	// installed" on this machine.
	insp := newTestInspector(staticResolver(map[string]string{}), t.TempDir())

	for _, name := range []string{"node", "npm", "pnpm", "yarn"} {
		t.Run(name, func(t *testing.T) {
			obs, err := insp.Inspect(context.Background(), core.Requirement{Name: name})
			if err != nil {
				t.Fatalf("missing binary must not be an error, got: %v", err)
			}
			want := core.Observation{Name: name, Present: false}
			if obs != want {
				t.Errorf("Inspect(%s) = %+v, want %+v", name, obs, want)
			}
		})
	}
}

// --- scope enforcement -------------------------------------------------

func TestInspectUnsupportedNameNeverExecutes(t *testing.T) {
	resolve, calls := spyResolver(staticResolver(map[string]string{}))
	insp := newTestInspector(resolve, t.TempDir())

	for _, name := range []string{"curl", "bash", "python", "malicious-script", "../../foo", "./evil", ""} {
		t.Run(name, func(t *testing.T) {
			*calls = nil
			obs, err := insp.Inspect(context.Background(), core.Requirement{Name: name})
			if !errors.Is(err, ErrUnsupportedTool) {
				t.Fatalf("Inspect(%q) error = %v, want ErrUnsupportedTool", name, err)
			}
			if obs != (core.Observation{}) {
				t.Errorf("Inspect(%q) obs = %+v, want zero value", name, obs)
			}
			if len(*calls) != 0 {
				t.Errorf("Inspect(%q) resolved a path (calls=%v); an unsupported name must never reach resolution/exec", name, *calls)
			}
		})
	}
}

func TestInspectOnlyProbesTheRequestedTool(t *testing.T) {
	// A pnpm requirement must not cause node/npm/yarn to be probed as
	// a side effect. Demand-driven: exactly the requested tool, and
	// nothing else.
	dir := t.TempDir()
	paths := map[string]string{
		"node": writeFakeTool(t, dir, "node", "echo v20.0.0\n"),
		"npm":  writeFakeTool(t, dir, "npm", "echo 10.0.0\n"),
		"pnpm": writeFakeTool(t, dir, "pnpm", "echo 9.0.0\n"),
		"yarn": writeFakeTool(t, dir, "yarn", "echo 4.0.0\n"),
	}
	resolve, calls := spyResolver(staticResolver(paths))
	insp := newTestInspector(resolve, t.TempDir())

	if _, err := insp.Inspect(context.Background(), core.Requirement{Name: "pnpm"}); err != nil {
		t.Fatalf("Inspect(pnpm) error: %v", err)
	}
	if got := *calls; len(got) != 1 || got[0] != "pnpm" {
		t.Fatalf("resolvePath calls = %v, want exactly [\"pnpm\"]", got)
	}
}

// --- PATH hijacking (adversarial) --------------------------------------

// TestResolveSystemPathIgnoresHijackedProcessPATH proves — by actually
// running the production resolveSystemPath + Inspect pipeline — that a
// project-controlled PATH entry placed ahead of the real system
// directories is never resolved or executed, even though the
// process's inherited PATH environment variable says it should win.
func TestResolveSystemPathIgnoresHijackedProcessPATH(t *testing.T) {
	legitSystemDir := t.TempDir()
	legitPath := writeFakeTool(t, legitSystemDir, "node", "echo v9.9.9\n")

	maliciousProjectDir := t.TempDir()
	maliciousBinDir := filepath.Join(maliciousProjectDir, "node_modules", ".bin")
	if err := os.MkdirAll(maliciousBinDir, 0o755); err != nil {
		t.Fatalf("mkdir malicious bin dir: %v", err)
	}
	marker := filepath.Join(maliciousProjectDir, "pwned")
	writeFakeTool(t, maliciousBinDir, "node", "touch '"+marker+"'\necho v0.0.0-PWNED\n")

	// Corrupt the *inherited* PATH so a naive PATH-searching resolver
	// (exec.LookPath, or "PATH lookup" in general) would find the
	// malicious script before any real system directory.
	t.Setenv("PATH", maliciousBinDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Swap the sanitized allowlist to point at a temp directory
	// standing in for /usr/local/bin et al. (the real ones can't be
	// written to in a test). Production always uses the real,
	// hardcoded list; only the allowlist target changes here, not the
	// resolution logic being exercised.
	origDirs := systemPathDirs
	systemPathDirs = []string{legitSystemDir}
	t.Cleanup(func() { systemPathDirs = origDirs })

	insp := &NodeInspector{resolvePath: resolveSystemPath, runDir: t.TempDir(), timeout: probeTimeout}
	obs, err := insp.Inspect(context.Background(), core.Requirement{Name: "node"})
	if err != nil {
		t.Fatalf("Inspect error: %v", err)
	}

	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("malicious node_modules/.bin/node was executed (marker file exists); PATH hijack succeeded")
	}
	want := core.Observation{Name: "node", Present: true, Version: "9.9.9"}
	if obs != want {
		t.Errorf("Inspect = %+v, want %+v (the legitimate system binary)", obs, want)
	}

	if got, err := resolveSystemPath("node"); err != nil || got != legitPath {
		t.Errorf("resolveSystemPath(node) = (%q, %v), want (%q, nil)", got, err, legitPath)
	}
}

func TestResolveSystemPathRejectsPathLikeNames(t *testing.T) {
	for _, name := range []string{"../../foo", "./node", "a/b", "/etc/passwd"} {
		if _, err := resolveSystemPath(name); !errors.Is(err, errExecutableNotFound) {
			t.Errorf("resolveSystemPath(%q) error = %v, want errExecutableNotFound", name, err)
		}
	}
}

// --- project-root (CWD) isolation --------------------------------------

// TestInspectNeverRunsFromProjectRoot proves the probed command's
// actual working directory is not wherever the WOMM process itself
// happens to be running from (which, in real usage, may well be the
// inspected project's root).
func TestInspectNeverRunsFromProjectRoot(t *testing.T) {
	simulatedProjectRoot := t.TempDir()
	t.Chdir(simulatedProjectRoot) // simulate `womm verify` run from inside the project

	dir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "cwd.txt")
	t.Setenv("WOMM_TEST_CWD_MARKER", marker)
	path := writeFakeTool(t, dir, "npm", "pwd > \"$WOMM_TEST_CWD_MARKER\"\necho 1.2.3\n")

	insp := newTestInspector(staticResolver(map[string]string{"npm": path}), os.TempDir())
	if _, err := insp.Inspect(context.Background(), core.Requirement{Name: "npm"}); err != nil {
		t.Fatalf("Inspect error: %v", err)
	}

	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("reading cwd marker: %v", err)
	}
	gotDir := strings.TrimSpace(string(data))

	resolvedProjectRoot, err := filepath.EvalSymlinks(simulatedProjectRoot)
	if err != nil {
		t.Fatalf("resolving project root: %v", err)
	}
	if gotDir == resolvedProjectRoot {
		t.Fatalf("probe ran with CWD = project root (%s); it must run from a neutral directory", gotDir)
	}
}

// --- timeout -------------------------------------------------------

func TestInspectTimesOutAndKillsChild(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(t.TempDir(), "alive.log")
	// Appends a line roughly every 20ms for far longer than any
	// timeout used below, and would keep doing so if left running.
	path := writeFakeTool(t, dir, "yarn",
		"for i in $(seq 1 500); do echo tick >> '"+logFile+"'; sleep 0.02; done\n")

	insp := &NodeInspector{
		resolvePath: staticResolver(map[string]string{"yarn": path}),
		runDir:      t.TempDir(),
		// Same-package-only override: production always uses the real
		// 5s (see probeTimeout / NewNodeInspector). Shrinking it here
		// keeps this test fast without weakening that default.
		timeout: 100 * time.Millisecond,
	}

	start := time.Now()
	_, err := insp.Inspect(context.Background(), core.Requirement{Name: "yarn"})
	elapsed := time.Since(start)

	if !errors.Is(err, ErrProbeTimeout) {
		t.Fatalf("error = %v, want ErrProbeTimeout", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Inspect took %s to return after a 100ms timeout; WOMM must not hang", elapsed)
	}

	countLines := func() int {
		data, _ := os.ReadFile(logFile)
		return len(strings.Split(strings.TrimSpace(string(data)), "\n"))
	}
	n1 := countLines()
	time.Sleep(300 * time.Millisecond)
	n2 := countLines()
	if n2 > n1 {
		t.Fatalf("child process kept writing after timeout (lines %d -> %d); it was not cleaned up", n1, n2)
	}
}

// --- context cancellation --------------------------------------------

func TestInspectRespectsCallerCancellation(t *testing.T) {
	dir := t.TempDir()
	path := writeFakeTool(t, dir, "node", "sleep 30\necho v1.0.0\n")
	insp := newTestInspector(staticResolver(map[string]string{"node": path}), t.TempDir())

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := insp.Inspect(ctx, core.Requirement{Name: "node"})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error from a cancelled context, got nil")
	}
	if errors.Is(err, ErrProbeTimeout) {
		t.Fatalf("error = %v, want cancellation, not the 5s probe timeout", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Inspect took %s to honor cancellation", elapsed)
	}
}

// --- malformed output --------------------------------------------------

func TestInspectMalformedOutputNeverFabricatesVersion(t *testing.T) {
	cases := map[string]string{
		"garbage":              "echo garbage\n",
		"prose":                "echo hello world\n",
		"empty":                "true\n",
		"multipleLines":        "echo 1.2.3\necho 4.5.6\n",
		"invalidSemverLeading": "echo 01.2.3\n",
	}
	dir := t.TempDir()
	for name, script := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeFakeTool(t, dir, "npm-"+name, script)
			insp := newTestInspector(staticResolver(map[string]string{"npm": path}), t.TempDir())
			obs, err := insp.Inspect(context.Background(), core.Requirement{Name: "npm"})
			if err != nil {
				t.Fatalf("Inspect error: %v", err)
			}
			if !obs.Present {
				t.Fatalf("obs.Present = false, want true (the binary ran successfully)")
			}
			if obs.Version != "" {
				t.Fatalf("obs.Version = %q, want \"\" (unknown) for unparseable output", obs.Version)
			}
		})
	}
}

// --- output abuse ----------------------------------------------------

func TestBoundedWriterEnforcesLimit(t *testing.T) {
	w := &boundedWriter{limit: 16}
	big := strings.Repeat("A", 10_000)

	n, err := w.Write([]byte(big))
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != len(big) {
		t.Fatalf("Write returned n=%d, want %d (must accept everything, to avoid blocking the writer)", n, len(big))
	}
	if w.buf.Len() != 16 {
		t.Fatalf("buffered %d bytes, want exactly the 16-byte limit", w.buf.Len())
	}
	if w.buf.String() != strings.Repeat("A", 16) {
		t.Fatalf("buffered content = %q, want the first 16 bytes", w.buf.String())
	}
}

func TestInspectExcessiveOutputIsBounded(t *testing.T) {
	dir := t.TempDir()
	// A single line far larger than maxOutputBytes.
	path := writeFakeTool(t, dir, "yarn", "head -c 20000 /dev/zero | tr '\\0' 'A'\n")
	insp := newTestInspector(staticResolver(map[string]string{"yarn": path}), t.TempDir())

	obs, err := insp.Inspect(context.Background(), core.Requirement{Name: "yarn"})
	if err != nil {
		t.Fatalf("Inspect error: %v", err)
	}
	if !obs.Present || obs.Version != "" {
		t.Fatalf("obs = %+v, want Present=true, Version=\"\" for bounded garbage output", obs)
	}
}

// --- version parsing (unit) --------------------------------------------

func TestParseVersion(t *testing.T) {
	cases := []struct {
		tool, raw, want string
	}{
		{"node", "v24.7.0\n", "24.7.0"},
		{"node", "24.7.0\n", "24.7.0"}, // bare form also accepted for node
		{"npm", "10.9.2\n", "10.9.2"},
		{"pnpm", "10.15.1\n", "10.15.1"},
		{"yarn", "4.6.0\n", "4.6.0"},
		{"npm", "garbage\n", ""},
		{"npm", "hello world\n", ""},
		{"npm", "", ""},
		{"npm", "\n\n", ""},
		{"npm", "1.2.3\n4.5.6\n", ""}, // more than one candidate line: never guess which
		{"node", "01.2.3\n", ""},      // leading zero: not strict semver
	}
	for _, c := range cases {
		if got := parseVersion(c.tool, c.raw); got != c.want {
			t.Errorf("parseVersion(%q, %q) = %q, want %q", c.tool, c.raw, got, c.want)
		}
	}
}
