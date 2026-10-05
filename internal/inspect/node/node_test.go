package node

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/0xCHANDA/womm/internal/core"
	"github.com/0xCHANDA/womm/internal/inspect/toolpath"
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
			want := core.Observation{Name: name, Present: true, Version: tc.wantVer, Path: paths[name]}
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
// running the production resolver + Inspect pipeline — that a
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
	origDirs := toolpath.SystemDirs
	toolpath.SystemDirs = []string{legitSystemDir}
	t.Cleanup(func() { toolpath.SystemDirs = origDirs })

	resolver := toolpath.System()
	insp := &NodeInspector{resolvePath: resolver.Resolve, runDir: t.TempDir(), timeout: probeTimeout}
	obs, err := insp.Inspect(context.Background(), core.Requirement{Name: "node"})
	if err != nil {
		t.Fatalf("Inspect error: %v", err)
	}

	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("malicious node_modules/.bin/node was executed (marker file exists); PATH hijack succeeded")
	}
	want := core.Observation{Name: "node", Present: true, Version: "9.9.9", Path: legitPath}
	if obs != want {
		t.Errorf("Inspect = %+v, want %+v (the legitimate system binary)", obs, want)
	}

	if got, err := resolver.Resolve("node"); err != nil || got != legitPath {
		t.Errorf("Resolve(node) = (%q, %v), want (%q, nil)", got, err, legitPath)
	}
}

func TestResolveSystemPathRejectsPathLikeNames(t *testing.T) {
	for _, name := range []string{"../../foo", "./node", "a/b", "/etc/passwd"} {
		if _, err := toolpath.System().Resolve(name); !errors.Is(err, errExecutableNotFound) {
			t.Errorf("Resolve(%q) error = %v, want errExecutableNotFound", name, err)
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
	path := writeFakeTool(t, dir, "npm", "pwd > '"+marker+"'\necho 1.2.3\n")

	// TMPDIR is what os.TempDir() would have used; a relative value
	// would have pointed the probe into the project. Production must
	// not consult it at all.
	t.Setenv("TMPDIR", ".")
	insp := NewNodeInspector()
	insp.resolvePath = staticResolver(map[string]string{"npm": path})
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
	if gotDir != probeDir {
		t.Fatalf("probe ran with CWD = %s; production probes run from %s (no writable ancestors)", gotDir, probeDir)
	}
}

// TestProbeDirHasNoWritableAncestors pins the property the working
// directory exists for: nothing above it can be planted by an
// unprivileged user, so a tool walking up from the cwd (yarn 1
// .yarnrc yarn-path, pnpm package.json packageManager) finds nothing.
func TestProbeDirHasNoWritableAncestors(t *testing.T) {
	if probeDir != "/" {
		t.Fatalf("probeDir = %q; the only directory with no ancestors is /", probeDir)
	}
	if filepath.Dir(probeDir) != probeDir {
		t.Fatalf("probeDir %q has an ancestor", probeDir)
	}
}

// TestInspectProbeSeesWalkerGuards proves the yarn/pnpm ancestor-walk
// guards reach the child process even when the inherited environment
// says the opposite.
func TestInspectProbeSeesWalkerGuards(t *testing.T) {
	t.Setenv("YARN_IGNORE_PATH", "0")
	t.Setenv("npm_config_manage_package_manager_versions", "true")
	t.Setenv("NPM_CONFIG_MANAGE_PACKAGE_MANAGER_VERSIONS", "true")
	dir := t.TempDir()
	path := writeFakeTool(t, dir, "yarn",
		`if [ "$YARN_IGNORE_PATH" = "1" ] && [ "$npm_config_manage_package_manager_versions" = "false" ] && [ -z "$NPM_CONFIG_MANAGE_PACKAGE_MANAGER_VERSIONS" ]; then echo 1.0.0; else echo 9.9.9; fi`+"\n")
	insp := newTestInspector(staticResolver(map[string]string{"yarn": path}), probeDir)
	obs, err := insp.Inspect(context.Background(), core.Requirement{Name: "yarn"})
	if err != nil {
		t.Fatal(err)
	}
	if obs.Version != "1.0.0" {
		t.Fatalf("walker guards did not reach the probe (reported %q)", obs.Version)
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

// --- inherited-environment injection (adversarial) -----------------------

// TestInspectStripsNodeOptions proves NODE_OPTIONS from the inherited
// environment never reaches the probed binary. NODE_OPTIONS=--require
// <file> makes every probed tool (all of them Node.js processes)
// execute arbitrary JS at startup — including project code — before
// printing its version. The L1 guarantee is "never execute project
// code", so this vector must be filtered out.
func TestInspectStripsNodeOptions(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "pwned")
	payload := filepath.Join(t.TempDir(), "evil.js")
	if err := os.WriteFile(payload, []byte(
		`require("fs").writeFileSync(process.env.WOMM_TEST_MARKER, "x")`+"\n"), 0o644); err != nil {
		t.Fatalf("writing payload: %v", err)
	}
	t.Setenv("WOMM_TEST_MARKER", marker)
	t.Setenv("NODE_OPTIONS", "--require "+payload)

	path := writeFakeTool(t, dir, "node", "echo v24.7.0\n")
	// The fake tool is a /bin/sh script, so it would run regardless;
	// the assertion is that the *injected* JS never did.
	insp := newTestInspector(staticResolver(map[string]string{"node": path}), t.TempDir())
	obs, err := insp.Inspect(context.Background(), core.Requirement{Name: "node"})
	if err != nil {
		t.Fatalf("Inspect error: %v", err)
	}
	if obs != (core.Observation{Name: "node", Present: true, Version: "24.7.0", Path: path}) {
		t.Errorf("obs = %+v, want the plain version observation", obs)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("NODE_OPTIONS --require payload was executed; inherited NODE_OPTIONS must be stripped from the probe environment")
	}
}

// TestSanitizedEnvStripsKeysWithoutPathCollision is a direct unit check
// of sanitizedEnv: inherited variables do not reach the probe (only
// locale settings do), and the sanitized PATH is always present.
func TestSanitizedEnvStripsKeysWithoutPathCollision(t *testing.T) {
	t.Setenv("NODE_OPTIONS", "--require /evil.js")
	t.Setenv("WOMM_TEST_INNOCENT", "dropped-too")
	t.Setenv("LC_MESSAGES", "C")

	env := sanitizedEnv()
	joined := "\n" + strings.Join(env, "\n") + "\n"
	if strings.Contains(joined, "NODE_OPTIONS=") {
		t.Errorf("NODE_OPTIONS survived sanitization: %v", env)
	}
	if strings.Contains(joined, "WOMM_TEST_INNOCENT=") {
		t.Errorf("an inherited variable reached the probe (the environment is an allowlist): %v", env)
	}
	if !strings.Contains(joined, "\nLC_MESSAGES=C\n") {
		t.Errorf("locale settings must be inherited: %v", env)
	}
	wantPath := "PATH=" + strings.Join(toolpath.SystemDirs, string(os.PathListSeparator))
	found := false
	for _, kv := range env {
		if kv == wantPath {
			found = true
		}
	}
	if !found {
		t.Errorf("sanitized PATH %q missing from env: %v", wantPath, env)
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

// --- partial observations on probe failure ---------------------------

// TestInspectProbeFailureReportsPresence pins the partial-observation
// contract: once the binary resolved and started, a failed query must
// still say "present" (and nothing else) next to the error, so the
// verify layer can classify the target as unreachable rather than as
// unknown or absent.
func TestInspectProbeFailureReportsPresence(t *testing.T) {
	cases := map[string]struct {
		script  string
		timeout time.Duration
		wantErr error
	}{
		"non-zero exit":  {script: "echo boom >&2\nexit 7\n", timeout: probeTimeout},
		"timeout":        {script: "sleep 30\n", timeout: 100 * time.Millisecond, wantErr: ErrProbeTimeout},
		"not executable": {script: "", timeout: probeTimeout},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeFakeTool(t, t.TempDir(), "npm", tc.script)
			if name == "not executable" {
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			insp := &NodeInspector{
				resolvePath: staticResolver(map[string]string{"npm": path}),
				runDir:      t.TempDir(),
				timeout:     tc.timeout,
			}
			obs, err := insp.Inspect(context.Background(), core.Requirement{Name: "npm"})
			if err == nil {
				t.Fatal("expected an inspection error")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			want := core.Observation{Name: "npm", Present: true, Path: path}
			if obs != want {
				t.Fatalf("Observation = %#v, want %#v (present, no version, the path that failed)", obs, want)
			}
		})
	}
}

// TestInspectNoPresenceWithoutResolution pins the other half of the
// contract: when presence was never established, the observation is
// the zero value — nothing is fabricated for the caller to map.
func TestInspectNoPresenceWithoutResolution(t *testing.T) {
	insp := newTestInspector(staticResolver(nil), t.TempDir())
	obs, err := insp.Inspect(context.Background(), core.Requirement{Name: "docker"})
	if !errors.Is(err, ErrUnsupportedTool) {
		t.Fatalf("error = %v, want ErrUnsupportedTool", err)
	}
	if obs != (core.Observation{}) {
		t.Fatalf("Observation = %#v, want zero value", obs)
	}

	broken := func(string) (string, error) { return "", errors.New("permission denied") }
	obs, err = newTestInspector(broken, t.TempDir()).Inspect(context.Background(), core.Requirement{Name: "node"})
	if err == nil || obs != (core.Observation{}) {
		t.Fatalf("resolution failure: obs=%#v err=%v; want zero observation and an error", obs, err)
	}
}

// --- Corepack: no network, no auto-pin ----------------------------------

// TestSanitizedEnvForcesCorepackOffline pins that whatever the inherited
// environment says, a probe runs with Corepack's network and auto-pin
// switched off, and with exactly one PATH entry (the sanitized one).
func TestSanitizedEnvForcesCorepackOffline(t *testing.T) {
	t.Setenv("COREPACK_ENABLE_NETWORK", "1")
	t.Setenv("COREPACK_ENABLE_AUTO_PIN", "1")
	t.Setenv("COREPACK_ENABLE_STRICT", "1")
	t.Setenv("PATH", "/evil/bin:/usr/bin")

	env := sanitizedEnv()
	count := map[string]int{}
	value := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		count[k]++
		value[k] = v
	}
	for _, k := range []string{"PATH", "COREPACK_ENABLE_NETWORK", "COREPACK_ENABLE_AUTO_PIN", "COREPACK_ENABLE_STRICT"} {
		if count[k] != 1 {
			t.Errorf("%s appears %d times in the probe env, want exactly once: %v", k, count[k], env)
		}
	}
	if value["COREPACK_ENABLE_NETWORK"] != "0" || value["COREPACK_ENABLE_AUTO_PIN"] != "0" || value["COREPACK_ENABLE_STRICT"] != "0" {
		t.Errorf("Corepack switches not forced off: %v", env)
	}
	if value["PATH"] != strings.Join(toolpath.SystemDirs, string(os.PathListSeparator)) {
		t.Errorf("PATH = %q, want the sanitized allowlist", value["PATH"])
	}
}

// TestInspectProbeSeesCorepackOffline proves the forced values reach the
// probed process itself, not just the slice.
func TestInspectProbeSeesCorepackOffline(t *testing.T) {
	t.Setenv("COREPACK_ENABLE_NETWORK", "1")
	dir := t.TempDir()
	path := writeFakeTool(t, dir, "pnpm",
		`if [ "$COREPACK_ENABLE_NETWORK" = "0" ] && [ "$COREPACK_ENABLE_AUTO_PIN" = "0" ]; then echo 1.0.0; else echo 9.9.9; fi`+"\n")
	insp := newTestInspector(staticResolver(map[string]string{"pnpm": path}), t.TempDir())
	obs, err := insp.Inspect(context.Background(), core.Requirement{Name: "pnpm"})
	if err != nil {
		t.Fatal(err)
	}
	if obs.Version != "1.0.0" {
		t.Fatalf("probe saw Corepack network enabled (reported %q)", obs.Version)
	}
}

// --- descendants that leave the process group ------------------------------

// TestInspectReturnsEvenIfDescendantEscapesGroup pins two guarantees
// against a shim whose grandchild calls setsid (leaves the process
// group, survives the group kill, keeps the output pipe open):
// WaitDelay still brings Inspect back within timeout + WaitDelay with
// the timeout error and the partial observation, and — WOMM being a
// child subreaper — the escaped process is adopted and killed
// afterwards instead of living on under init.
func TestInspectReturnsEvenIfDescendantEscapesGroup(t *testing.T) {
	if _, err := os.Stat("/usr/bin/setsid"); err != nil {
		t.Skip("setsid not available")
	}
	dir := t.TempDir()
	log := filepath.Join(t.TempDir(), "alive")
	path := writeFakeTool(t, dir, "yarn",
		"/usr/bin/setsid sh -c 'for i in $(seq 1 1500); do echo tick >> "+log+"; sleep 0.02; done' &\nsleep 30\n")
	insp := &NodeInspector{
		resolvePath: staticResolver(map[string]string{"yarn": path}),
		runDir:      t.TempDir(),
		timeout:     100 * time.Millisecond,
	}
	start := time.Now()
	obs, err := insp.Inspect(context.Background(), core.Requirement{Name: "yarn"})
	elapsed := time.Since(start)
	if !errors.Is(err, ErrProbeTimeout) {
		t.Fatalf("error = %v, want ErrProbeTimeout", err)
	}
	if obs != (core.Observation{Name: "yarn", Present: true, Path: path}) {
		t.Fatalf("observation = %#v, want present without version", obs)
	}
	if elapsed > 100*time.Millisecond+waitDelay+2*time.Second {
		t.Fatalf("Inspect took %s with an escaped descendant; must be bounded by timeout + WaitDelay", elapsed)
	}
	// The escapee must be dead: its log stops growing.
	time.Sleep(100 * time.Millisecond)
	before, _ := os.ReadFile(log)
	time.Sleep(300 * time.Millisecond)
	after, _ := os.ReadFile(log)
	if len(after) > len(before) {
		t.Fatalf("setsid-escaped descendant survived Inspect (log grew %d -> %d bytes)", len(before), len(after))
	}
}

// --- inherited home / cache / loader variables --------------------------------

// TestSanitizedEnvForcesAccountHome pins that HOME and COREPACK_HOME in
// the probe come from the account's passwd entry, never from the
// inherited environment (a Corepack shim executes whatever sits in its
// cache directory), and that loader/coverage injection variables are
// gone.
func TestSanitizedEnvForcesAccountHome(t *testing.T) {
	t.Setenv("HOME", "/evil/project")
	t.Setenv("COREPACK_HOME", "/evil/project/.corepack")
	t.Setenv("XDG_CACHE_HOME", "/evil/project/.cache")
	t.Setenv("XDG_CONFIG_HOME", "/evil/project/.config")
	t.Setenv("LD_PRELOAD", "/evil/evil.so")
	t.Setenv("LD_AUDIT", "/evil/audit.so")
	t.Setenv("LD_LIBRARY_PATH", "/evil/lib")
	t.Setenv("NODE_V8_COVERAGE", "/evil/cov")
	t.Setenv("NODE_REDIRECT_WARNINGS", "/evil/warn")

	env := sanitizedEnv()
	value := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if _, dup := value[k]; dup {
			t.Errorf("%s appears twice", k)
		}
		value[k] = v
	}
	for _, k := range []string{"XDG_CACHE_HOME", "XDG_CONFIG_HOME", "LD_PRELOAD", "LD_AUDIT", "LD_LIBRARY_PATH", "NODE_V8_COVERAGE", "NODE_REDIRECT_WARNINGS"} {
		if _, present := value[k]; present {
			t.Errorf("%s survived sanitization", k)
		}
	}
	if value["HOME"] != accountHome || strings.HasPrefix(value["HOME"], "/evil") {
		t.Errorf("HOME = %q, want the account home %q", value["HOME"], accountHome)
	}
	if value["COREPACK_HOME"] != accountHome+"/.cache/node/corepack" {
		t.Errorf("COREPACK_HOME = %q", value["COREPACK_HOME"])
	}
	if !filepath.IsAbs(accountHome) {
		t.Errorf("accountHome %q is not absolute", accountHome)
	}
}

// TestInspectProbeSeesAccountHome proves the forced values reach the
// child: a shim that echoes its HOME must not see the hostile one.
func TestInspectProbeSeesAccountHome(t *testing.T) {
	t.Setenv("HOME", "/evil/project")
	t.Setenv("COREPACK_HOME", "/evil/project/.corepack")
	dir := t.TempDir()
	path := writeFakeTool(t, dir, "pnpm",
		`case "$HOME" in /evil*) echo 9.9.9;; *) case "$COREPACK_HOME" in "$HOME"/.cache/node/corepack) echo 1.0.0;; *) echo 8.8.8;; esac;; esac`+"\n")
	insp := newTestInspector(staticResolver(map[string]string{"pnpm": path}), probeDir)
	obs, err := insp.Inspect(context.Background(), core.Requirement{Name: "pnpm"})
	if err != nil {
		t.Fatal(err)
	}
	if obs.Version != "1.0.0" {
		t.Fatalf("probe saw the inherited HOME/COREPACK_HOME (reported %q)", obs.Version)
	}
}

// TestInspectSweepsEscapeeAfterSuccessfulProbe: a shim that answers
// correctly but leaves a setsid-detached process behind must not leak
// it — the sweep runs on the success path too.
func TestInspectSweepsEscapeeAfterSuccessfulProbe(t *testing.T) {
	if _, err := os.Stat("/usr/bin/setsid"); err != nil {
		t.Skip("setsid not available")
	}
	dir := t.TempDir()
	log := filepath.Join(t.TempDir(), "alive")
	path := writeFakeTool(t, dir, "npm",
		"/usr/bin/setsid sh -c 'for i in $(seq 1 1500); do echo tick >> "+log+"; sleep 0.02; done' </dev/null >/dev/null 2>&1 &\necho 11.2.0\n")
	insp := newTestInspector(staticResolver(map[string]string{"npm": path}), probeDir)
	obs, err := insp.Inspect(context.Background(), core.Requirement{Name: "npm"})
	if err != nil || obs.Version != "11.2.0" {
		t.Fatalf("obs=%+v err=%v", obs, err)
	}
	time.Sleep(100 * time.Millisecond)
	before, _ := os.ReadFile(log)
	time.Sleep(300 * time.Millisecond)
	after, _ := os.ReadFile(log)
	if len(after) > len(before) {
		t.Fatalf("detached descendant survived a successful probe (log grew %d -> %d bytes)", len(before), len(after))
	}
}

// TestInspectRecordsExecutedPath pins the evidence contract of
// core.Observation.Path: it is exactly the executable that was started
// (never a resolved symlink target — a shim must be recorded as the
// shim), empty when nothing was executed, and independent of whether
// the version could be parsed.
func TestInspectRecordsExecutedPath(t *testing.T) {
	dir := t.TempDir()
	good := writeFakeTool(t, dir, "node", "echo v24.7.0\n")
	garbage := writeFakeTool(t, dir, "npm", "echo not-a-version\n")

	target := writeFakeTool(t, t.TempDir(), "real-yarn", "echo 4.6.0\n")
	shim := filepath.Join(dir, "yarn")
	if err := os.Symlink(target, shim); err != nil {
		t.Fatal(err)
	}

	insp := newTestInspector(staticResolver(map[string]string{"node": good, "npm": garbage, "yarn": shim}), t.TempDir())
	cases := []struct {
		name string
		want core.Observation
	}{
		{"node", core.Observation{Name: "node", Present: true, Version: "24.7.0", Path: good}},
		{"npm", core.Observation{Name: "npm", Present: true, Version: "", Path: garbage}},
		{"yarn", core.Observation{Name: "yarn", Present: true, Version: "4.6.0", Path: shim}},
		{"pnpm", core.Observation{Name: "pnpm", Present: false}},
	}
	for _, tc := range cases {
		obs, err := insp.Inspect(context.Background(), core.Requirement{Name: tc.name})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if obs != tc.want {
			t.Errorf("%s: Observation = %#v, want %#v", tc.name, obs, tc.want)
		}
	}
}

// --- probe PATH for user-named tool directories -----------------------

func TestProbePath(t *testing.T) {
	sys := strings.Join(toolpath.SystemDirs, string(os.PathListSeparator))
	cases := []struct{ exec, want string }{
		{"", sys},
		{"/usr/bin/node", sys},
		{"/usr/local/bin/npm", sys},
		{"/home/u/.volta/bin/node", "/home/u/.volta/bin" + string(os.PathListSeparator) + sys},
		{"/opt/node22/bin/npm", "/opt/node22/bin" + string(os.PathListSeparator) + sys},
	}
	for _, tc := range cases {
		if got := probePath(tc.exec); got != tc.want {
			t.Errorf("probePath(%q) = %q, want %q", tc.exec, got, tc.want)
		}
	}
	// The inherited PATH never leaks in, whatever the executable.
	t.Setenv("PATH", "/evil/bin")
	for _, e := range sanitizedEnvFor("/home/u/.volta/bin/node") {
		if strings.HasPrefix(e, "PATH=") && strings.Contains(e, "/evil/bin") {
			t.Errorf("inherited PATH leaked into the probe environment: %s", e)
		}
	}
}

// TestInspectSiblingToolsComeFromTheToolDirectory is the nvm/fnm/Volta
// shape: npm is a launcher that finds `node` through PATH. Run from a
// user-named directory it must see that directory's node, not whatever
// the system directories hold — otherwise the npm observation is about
// a mixture of two installations.
func TestInspectSiblingToolsComeFromTheToolDirectory(t *testing.T) {
	toolDir := t.TempDir()
	writeFakeTool(t, toolDir, "node", "echo v24.7.0\n")
	npm := writeFakeTool(t, toolDir, "npm", "node --version | sed 's/^v//'\n")

	insp := newTestInspector(staticResolver(map[string]string{"npm": npm}), t.TempDir())
	obs, err := insp.Inspect(context.Background(), core.Requirement{Name: "npm"})
	if err != nil {
		t.Fatal(err)
	}
	if obs.Version != "24.7.0" {
		t.Fatalf("npm observed %q; its launcher did not find the sibling node in %s", obs.Version, toolDir)
	}
}

// TestInspectSystemBinaryKeepsTheFixedSystemPath pins that nothing
// changes for binaries from the system directories: PATH is exactly the
// fixed allowlist, so v0.1 behaviour is untouched.
func TestInspectSystemBinaryKeepsTheFixedSystemPath(t *testing.T) {
	sysDir := t.TempDir()
	orig := toolpath.SystemDirs
	toolpath.SystemDirs = []string{sysDir}
	t.Cleanup(func() { toolpath.SystemDirs = orig })

	marker := filepath.Join(t.TempDir(), "path")
	tool := writeFakeTool(t, sysDir, "node", "printf %s \"$PATH\" > '"+marker+"'\necho v24.7.0\n")
	insp := newTestInspector(staticResolver(map[string]string{"node": tool}), t.TempDir())
	if _, err := insp.Inspect(context.Background(), core.Requirement{Name: "node"}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != sysDir {
		t.Errorf("probe PATH = %q, want exactly the system directories %q", got, sysDir)
	}
}

// TestAccountHomeIgnoresInheritedHomeForUIDWithoutPasswdEntry reproduces
// the hole left by the account-home fix: os/user.Current() falls back to
// $HOME (when $USER is set too) if the uid has no passwd entry — Docker
// `--user 1234`, OpenShift, many CI images — and the forced HOME /
// COREPACK_HOME handed to the probe were then exactly the inherited,
// project-reachable values the fix was meant to remove. The in-process
// accountHome cannot show it (it is computed at init, and the test uid
// has a passwd entry), so the test binary re-runs itself as a uid that
// has none, with a hostile HOME and a USER set.
func TestAccountHomeIgnoresInheritedHomeForUIDWithoutPasswdEntry(t *testing.T) {
	if os.Getenv("WOMM_ACCOUNT_HOME_CHILD") == "1" {
		return // the child half is TestAccountHomeChild
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root to run as a uid without a passwd entry")
	}
	const uid = 54321
	if _, err := user.LookupId(strconv.Itoa(uid)); err == nil {
		t.Skipf("uid %d unexpectedly has a passwd entry", uid)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The go-build directory is not searchable by another uid.
	dir, err := os.MkdirTemp("", "womm-accounthome")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(dir, "node.test")
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyPath, data, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(copyPath, "-test.run=^TestAccountHomeChild$", "-test.v")
	cmd.Dir = dir
	cmd.Env = []string{"WOMM_ACCOUNT_HOME_CHILD=1", "USER=ci", "HOME=/evil/project"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: uid}}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "--- PASS: TestAccountHomeChild") {
		t.Fatalf("child did not run:\n%s", out)
	}
}

// TestAccountHomeChild is the half that runs as the passwd-less uid.
func TestAccountHomeChild(t *testing.T) {
	if os.Getenv("WOMM_ACCOUNT_HOME_CHILD") != "1" {
		t.Skip("run by TestAccountHomeIgnoresInheritedHomeForUIDWithoutPasswdEntry")
	}
	if got := resolveAccountHome(); got != "/nonexistent" {
		t.Fatalf("account home = %q with HOME=/evil/project, USER=ci and no passwd entry; want /nonexistent (the inherited HOME must never be used)", got)
	}
	for _, kv := range sanitizedEnv() {
		if strings.Contains(kv, "/evil/project") {
			t.Errorf("hostile HOME reached the probe environment: %s", kv)
		}
	}
}

// dispatcherShim writes a Volta/mise/asdf-shaped launcher: it does not
// answer itself, it executes whichever binary an environment variable
// names (VOLTA_HOME, MISE_*, ASDF_*, ... select the real tool).
func dispatcherShim(t *testing.T, dir, name, envVar string) string {
	t.Helper()
	return writeFakeTool(t, dir, name, "exec \"$"+envVar+"/bin/"+name+"\" \"$@\"\n")
}

// TestInspectDoesNotLetTheEnvironmentSteerAShimDispatcher reproduces a
// security review finding: version-manager shims choose the binary to
// run from inherited variables, and a launcher (direnv, make, npm run)
// can set those to a directory inside the project — project code
// execution behind a legitimate-looking path, and a PASS.
func TestInspectDoesNotLetTheEnvironmentSteerAShimDispatcher(t *testing.T) {
	project := t.TempDir()
	marker := filepath.Join(project, "pwned")
	if err := os.MkdirAll(filepath.Join(project, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeTool(t, filepath.Join(project, "bin"), "node", "touch '"+marker+"'\necho v24.7.0\n")

	shimDir := t.TempDir()
	for _, v := range []string{"VOLTA_HOME", "MISE_DATA_DIR", "ASDF_DATA_DIR", "ASDF_NODEJS_VERSION", "NODENV_ROOT", "FNM_DIR", "N_PREFIX", "NVM_DIR", "RTX_DATA_DIR", "XDG_DATA_HOME", "SHELL", "BASH_ENV", "ENV", "IFS"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv(v, project)
			shim := dispatcherShim(t, shimDir, "node", v)
			insp := newTestInspector(staticResolver(map[string]string{"node": shim}), t.TempDir())
			obs, err := insp.Inspect(context.Background(), core.Requirement{Name: "node"})
			if _, statErr := os.Stat(marker); statErr == nil {
				t.Fatalf("%s=<project> steered the shim into running project code", v)
			}
			if err == nil && obs.Version == "24.7.0" {
				t.Errorf("%s: the project's binary answered for the shim: %+v", v, obs)
			}
		})
	}
}

// TestSanitizedEnvIsAnAllowlist pins the shape: nothing is inherited
// except locale settings; everything else the probe sees is forced.
func TestSanitizedEnvIsAnAllowlist(t *testing.T) {
	for _, v := range []string{"VOLTA_HOME", "MISE_ANYTHING", "ASDF_DATA_DIR", "FOO", "TMPDIR", "SHELL", "npm_config_userconfig", "NODE_PATH", "BASH_ENV"} {
		t.Setenv(v, "/evil")
	}
	t.Setenv("LANG", "en_US.UTF-8")
	t.Setenv("LC_ALL", "C")
	got := map[string]string{}
	for _, kv := range sanitizedEnvFor("/home/u/.volta/bin/node") {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	want := map[string]bool{"LANG": true, "LC_ALL": true, "PATH": true, "HOME": true, "COREPACK_HOME": true, "COREPACK_ENABLE_NETWORK": true,
		"COREPACK_ENABLE_AUTO_PIN": true, "COREPACK_ENABLE_STRICT": true, "YARN_IGNORE_PATH": true, "npm_config_manage_package_manager_versions": true}
	for k := range got {
		if !want[k] && !strings.HasPrefix(k, "LC_") { // any inherited locale variable is allowed
			t.Errorf("unexpected variable in the probe environment: %s=%s", k, got[k])
		}
		if false {
			t.Errorf("unexpected variable in the probe environment: %s=%s", k, got[k])
		}
	}
	if got["LANG"] != "en_US.UTF-8" || got["LC_ALL"] != "C" {
		t.Errorf("locale not preserved: %v", got)
	}
}

// probePath must never emit a directory that would split into several
// entries: only validated, separator-free directories may be prepended.
func TestProbePathNeverSplitsADirectory(t *testing.T) {
	sys := strings.Join(toolpath.SystemDirs, string(os.PathListSeparator))
	for _, exec := range []string{"/x/a:/x/evil/npm", "/x/a:/node", "/x/nl\n/node"} {
		if got := probePath(exec); got != sys {
			t.Errorf("probePath(%q) = %q, want the system PATH only", exec, got)
		}
	}
}

// TestAccountHomeIgnoresHostileHomeAtInit re-runs the test binary as the
// SAME uid with a hostile HOME and USER and checks the package's
// account home and probe environment. Unlike the passwd-less-uid test it
// needs no root, so CI exercises it: a production binding to
// os.Getenv("HOME") is caught here.
func TestAccountHomeIgnoresHostileHomeAtInit(t *testing.T) {
	if os.Getenv("WOMM_HOSTILE_HOME_CHILD") == "1" {
		if strings.HasPrefix(accountHome, "/evil") {
			t.Fatalf("accountHome = %q: the inherited HOME was used", accountHome)
		}
		for _, kv := range sanitizedEnvFor("/usr/bin/node") {
			if strings.Contains(kv, "/evil/project") {
				t.Errorf("hostile value reached the probe environment: %s", kv)
			}
		}
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestAccountHomeIgnoresHostileHomeAtInit$", "-test.v")
	cmd.Env = []string{"WOMM_HOSTILE_HOME_CHILD=1", "HOME=/evil/project", "USER=ci", "PATH=" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--- PASS: TestAccountHomeIgnoresHostileHomeAtInit") {
		t.Fatalf("child: %v\n%s", err, out)
	}
}
