package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/0xCHANDA/womm/internal/core"
	"github.com/0xCHANDA/womm/internal/inspect"
)

// End-to-end tests over the real command tree: real detectors, real
// schema, real compare, real report, real exit-code mapping. Only the
// machine inspector is replaced by a profile of "the other machine",
// so verdicts never depend on what the test host has installed. The
// inspector itself is exercised against real processes in
// internal/inspect/node (including through verify.Verify).

// --- fixtures ---------------------------------------------------------

// stageProject copies testdata/projects/<name> into a fresh temp dir so
// capture can write womm.yaml next to it without touching testdata.
func stageProject(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join("testdata", "projects", name)
	dst := t.TempDir()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

// machine is a profile of the host verify runs on. Tools absent from
// the map are unsupported: verify must report an operational error.
type machine map[string]outcomeE2E

type outcomeE2E struct {
	obs core.Observation
	err error
}

func present(name, version string) outcomeE2E {
	return outcomeE2E{obs: core.Observation{Name: name, Present: true, Version: version}}
}
func absent(name string) outcomeE2E { return outcomeE2E{obs: core.Observation{Name: name}} }
func unknownVersion(name string) outcomeE2E {
	return outcomeE2E{obs: core.Observation{Name: name, Present: true}}
}
func unreachable(name, why string) outcomeE2E {
	return outcomeE2E{obs: core.Observation{Name: name, Present: true}, err: errors.New(why)}
}

func (m machine) Supports(name string) bool { _, ok := m[name]; return ok }
func (m machine) Inspect(_ context.Context, req core.Requirement) (core.Observation, error) {
	o := m[req.Name]
	return o.obs, o.err
}

func onMachine(t *testing.T, m machine) {
	t.Helper()
	prev := newInspectors
	newInspectors = func() []inspect.Inspector { return []inspect.Inspector{m} }
	t.Cleanup(func() { newInspectors = prev })
}

// --- capture goldens ----------------------------------------------------

const exactYAML = `version: 1
requirements:
  - name: node
    constraint: 24.7.0
    evidence:
      - source: package.json
        field: engines.node
        value: '>=22 <25'
      - source: .nvmrc
        field: version
        value: v24.7.0
  - name: pnpm
    constraint: 10.15.1
    evidence:
      - source: package.json
        field: packageManager
        value: pnpm@10.15.1+sha512.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
`

const prereleaseYAML = `version: 1
requirements:
  - name: node
    constraint: '>=24.0.0-0'
    evidence:
      - source: package.json
        field: engines.node
        value: '>=24.0.0-0'
  - name: yarn
    constraint: 4.0.0-rc.1
    evidence:
      - source: package.json
        field: packageManager
        value: yarn@4.0.0-rc.1
`

func TestE2ECapture(t *testing.T) {
	cases := []struct {
		project  string
		wantCode int
		wantYAML string
		wantErr  string
	}{
		{project: "exact", wantYAML: exactYAML},
		{project: "range", wantYAML: "version: 1\nrequirements:\n  - name: node\n    constraint: ^20.10.0 || >=22.0.0\n    evidence:\n      - source: package.json\n        field: engines.node\n        value: ^20.10.0 || >=22.0.0\n"},
		{project: "pm", wantYAML: "version: 1\nrequirements:\n  - name: npm\n    constraint: 11.2.0\n    evidence:\n      - source: package.json\n        field: packageManager\n        value: npm@11.2.0\n"},
		{project: "nothing", wantYAML: "version: 1\nrequirements: []\n"},
		{project: "prerelease-pm", wantYAML: prereleaseYAML},
		{project: "malformed", wantCode: 3, wantErr: "malformed package.json"},
		{project: "conflict", wantCode: 3, wantErr: `evidence conflict: node required as ">=22" (by package.json → engines.node) and "20.19.0" (by .nvmrc → version) cannot both be true`},
		{project: "unsupported-selector", wantCode: 3, wantErr: `.nvmrc selector "lts/iron" is valid nvm syntax but is not supported`},
	}
	for _, tc := range cases {
		t.Run(tc.project, func(t *testing.T) {
			dir := stageProject(t, tc.project)
			code, stdout, stderr := runCLI(t, "capture", dir)
			if code != tc.wantCode {
				t.Fatalf("exit %d, want %d\nstdout %s\nstderr %s", code, tc.wantCode, stdout, stderr)
			}
			out := filepath.Join(dir, "womm.yaml")
			if tc.wantCode != 0 {
				if !strings.Contains(stderr, tc.wantErr) {
					t.Errorf("stderr = %q, want containing %q", stderr, tc.wantErr)
				}
				if _, err := os.Stat(out); !os.IsNotExist(err) {
					t.Errorf("failed capture left %s behind", out)
				}
				if stdout != "" {
					t.Errorf("failed capture wrote to stdout: %q", stdout)
				}
				return
			}
			got, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.wantYAML {
				t.Errorf("womm.yaml mismatch\n--- got ---\n%s--- want ---\n%s", got, tc.wantYAML)
			}
		})
	}
}

// --- verify scenarios ---------------------------------------------------

func TestE2EVerify(t *testing.T) {
	cases := []struct {
		name     string
		project  string
		machine  machine
		wantCode int
		wantOut  string   // full golden stdout when set
		wantIn   []string // fragments otherwise
		wantErr  []string
	}{
		{
			name: "exact node + pnpm match", project: "exact",
			machine:  machine{"node": present("node", "24.7.0"), "pnpm": present("pnpm", "10.15.1")},
			wantCode: 0,
			wantOut: `PASS        node required 24.7.0; observed 24.7.0
            observed version equals the required version
            evidence: package.json → engines.node = ">=22 <25"
            evidence: .nvmrc → version = "v24.7.0"
PASS        pnpm required 10.15.1; observed 10.15.1
            observed version equals the required version
            evidence: package.json → packageManager = "pnpm@10.15.1+sha512.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

2 requirements: 2 pass, 0 fail, 0 unknown, 0 unreachable
`,
		},
		{
			name: "exact node newer patch is incompatible", project: "exact",
			machine:  machine{"node": present("node", "24.8.0"), "pnpm": present("pnpm", "10.15.1")},
			wantCode: 1,
			wantIn:   []string{"FAIL        node required 24.7.0; observed 24.8.0", "observed version differs from the required version", "PASS        pnpm", "1 pass, 1 fail, 0 unknown, 0 unreachable"},
		},
		{
			name: "range match lower alternative", project: "range",
			machine:  machine{"node": present("node", "20.19.0")},
			wantCode: 0,
			wantIn:   []string{"PASS        node required ^20.10.0 || >=22.0.0; observed 20.19.0", "1 requirement: 1 pass"},
		},
		{
			name: "range match upper alternative boundary", project: "range",
			machine:  machine{"node": present("node", "22.0.0")},
			wantCode: 0,
			wantIn:   []string{"PASS        node"},
		},
		{
			name: "range gap is incompatible", project: "range",
			machine:  machine{"node": present("node", "21.5.0")},
			wantCode: 1,
			wantIn:   []string{"FAIL        node required ^20.10.0 || >=22.0.0; observed 21.5.0", "observed version does not satisfy the requirement"},
		},
		{
			name: "package manager match", project: "pm",
			machine:  machine{"npm": present("npm", "11.2.0")},
			wantCode: 0,
			wantIn:   []string{"PASS        npm required 11.2.0; observed 11.2.0"},
		},
		{
			name: "package manager mismatch", project: "pm",
			machine:  machine{"npm": present("npm", "10.9.2")},
			wantCode: 1,
			wantIn:   []string{"FAIL        npm required 11.2.0; observed 10.9.2"},
		},
		{
			name: "missing target", project: "pm",
			machine:  machine{"npm": absent("npm")},
			wantCode: 1,
			wantIn:   []string{"FAIL        npm required 11.2.0; observed absent", "required target is absent"},
		},
		{
			name: "unknown version", project: "pm",
			machine:  machine{"npm": unknownVersion("npm")},
			wantCode: 3,
			wantIn:   []string{"UNKNOWN     npm required 11.2.0; observed present, version unknown", "target is present but its version is unknown"},
		},
		{
			name: "unreachable probe", project: "pm",
			machine:  machine{"npm": unreachable("npm", "version probe timed out after 5s")},
			wantCode: 3,
			wantIn:   []string{"UNREACHABLE npm required 11.2.0; observed present, version unknown", "target is present but could not be queried: version probe timed out after 5s", "0 fail, 0 unknown, 1 unreachable"},
		},
		{
			name: "fail plus unreachable is inconclusive", project: "exact",
			machine:  machine{"node": present("node", "18.0.0"), "pnpm": unreachable("pnpm", "version probe failed: exit status 1")},
			wantCode: 3,
			wantIn:   []string{"FAIL        node", "UNREACHABLE pnpm", "0 pass, 1 fail, 0 unknown, 1 unreachable"},
		},
		{
			name: "fail plus unknown is inconclusive", project: "exact",
			machine:  machine{"node": present("node", "18.0.0"), "pnpm": unknownVersion("pnpm")},
			wantCode: 3,
			wantIn:   []string{"FAIL        node", "UNKNOWN     pnpm", "0 pass, 1 fail, 1 unknown, 0 unreachable"},
		},
		{
			name: "prerelease observed against prerelease range is refused", project: "prerelease-pm",
			machine:  machine{"node": present("node", "24.7.0-beta.1"), "yarn": present("yarn", "4.0.0-rc.1")},
			wantCode: 3,
			wantIn:   []string{"UNKNOWN     node required >=24.0.0-0; observed 24.7.0-beta.1", "observed version is a prerelease; whether a prerelease satisfies a version range is not decided by WOMM", "PASS        yarn required 4.0.0-rc.1; observed 4.0.0-rc.1"},
			wantErr:  []string{`error: node: prerelease observed against a version range: "24.7.0-beta.1" against ">=24.0.0-0"`},
		},
		{
			name: "release observed against prerelease range passes", project: "prerelease-pm",
			machine:  machine{"node": present("node", "24.7.0"), "yarn": present("yarn", "4.0.0-rc.1")},
			wantCode: 0,
			wantIn:   []string{"PASS        node required >=24.0.0-0; observed 24.7.0", "PASS        yarn"},
		},
		{
			name: "exact prerelease pin rejects the next rc and the release", project: "prerelease-pm",
			machine:  machine{"node": present("node", "24.7.0"), "yarn": present("yarn", "4.0.0-rc.2")},
			wantCode: 1,
			wantIn:   []string{"FAIL        yarn required 4.0.0-rc.1; observed 4.0.0-rc.2"},
		},
		{
			name: "exact prerelease pin rejects the final release", project: "prerelease-pm",
			machine:  machine{"node": present("node", "24.7.0"), "yarn": present("yarn", "4.0.0")},
			wantCode: 1,
			wantIn:   []string{"FAIL        yarn required 4.0.0-rc.1; observed 4.0.0"},
		},
		{
			name: "nothing declared verifies trivially", project: "nothing",
			machine:  machine{},
			wantCode: 0,
			wantOut:  "0 requirements: 0 pass, 0 fail, 0 unknown, 0 unreachable\n",
		},
		{
			name: "unsupported requirement on this machine profile", project: "exact",
			machine:  machine{"node": present("node", "24.7.0")},
			wantCode: 3,
			wantIn:   []string{"PASS        node", "1 requirement: 1 pass"},
			wantErr:  []string{`error: no inspector supports requirement: "pnpm"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := stageProject(t, tc.project)
			if code, _, stderr := runCLI(t, "capture", dir); code != 0 {
				t.Fatalf("capture failed: %s", stderr)
			}
			onMachine(t, tc.machine)
			code, stdout, stderr := runCLI(t, "verify", dir)
			if code != tc.wantCode {
				t.Errorf("exit %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tc.wantCode, stdout, stderr)
			}
			if tc.wantOut != "" && stdout != tc.wantOut {
				t.Errorf("stdout mismatch\n--- got ---\n%s--- want ---\n%s", stdout, tc.wantOut)
			}
			for _, frag := range tc.wantIn {
				if !strings.Contains(stdout, frag) {
					t.Errorf("stdout lacks %q:\n%s", frag, stdout)
				}
			}
			for _, frag := range tc.wantErr {
				if !strings.Contains(stderr, frag) {
					t.Errorf("stderr lacks %q:\n%s", frag, stderr)
				}
			}
			if len(tc.wantErr) == 0 && stderr != "" {
				t.Errorf("unexpected stderr: %q", stderr)
			}
			// Output ordering is by requirement name whatever the
			// declaration order in the file.
			var last string
			for _, line := range strings.Split(stdout, "\n") {
				if len(line) > 12 && line[0] != ' ' && !strings.Contains(line, "requirement") {
					name := strings.Fields(line)[1]
					if name < last {
						t.Errorf("report is not sorted by name: %q after %q", name, last)
					}
					last = name
				}
			}
		})
	}
}

// TestE2ERepeatedRunsAreIdentical runs capture and verify several
// times on the same project and machine profile and requires
// byte-identical files, stdout, stderr and exit codes.
func TestE2ERepeatedRunsAreIdentical(t *testing.T) {
	onMachine(t, machine{"node": present("node", "18.0.0"), "pnpm": unreachable("pnpm", "version probe timed out after 5s")})
	type run struct {
		yaml, out, err string
		code           int
	}
	var runs []run
	for i := 0; i < 3; i++ {
		dir := stageProject(t, "exact")
		if code, _, stderr := runCLI(t, "capture", dir); code != 0 {
			t.Fatal(stderr)
		}
		yaml, _ := os.ReadFile(filepath.Join(dir, "womm.yaml"))
		code, out, err := runCLI(t, "verify", dir)
		runs = append(runs, run{string(yaml), out, err, code})
	}
	for i := 1; i < len(runs); i++ {
		if runs[i] != runs[0] {
			t.Fatalf("run %d differs from run 0:\n%+v\n---\n%+v", i, runs[i], runs[0])
		}
	}
	if runs[0].code != 3 {
		t.Errorf("exit = %d, want 3", runs[0].code)
	}
}

// --- the built binary ---------------------------------------------------

var (
	buildOnce sync.Once
	binPath   string
	buildErr  error
)

// womBinary builds cmd/womm once per test process and returns its
// path, so tests can check real process exit codes and streams.
func womBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "womm-e2e-")
		if err != nil {
			buildErr = err
			return
		}
		binPath = filepath.Join(dir, "womm")
		cmd := exec.Command("go", "build", "-o", binPath, "../../cmd/womm")
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = errors.New(string(out))
		}
	})
	if buildErr != nil {
		t.Fatalf("building womm: %v", buildErr)
	}
	return binPath
}

func runBinary(t *testing.T, dir string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(womBinary(t), args...)
	cmd.Dir = dir
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		code = 0
	case errors.As(err, &exit):
		code = exit.ExitCode()
	default:
		t.Fatalf("running womm: %v", err)
	}
	return code, out.String(), errb.String()
}

// TestE2EBinaryExitCodes drives the compiled binary through every exit
// code the machine cannot influence: 0 (capture, empty verify), 2
// (usage) and 3 (configuration). The one machine-dependent run (a real
// requirement against the real host) checks the contract's shape —
// the exit code is one of the verdict codes and matches the summary —
// without assuming what the host has installed.
func TestE2EBinaryExitCodes(t *testing.T) {
	dir := stageProject(t, "exact")

	if code, stdout, stderr := runBinary(t, dir, "capture"); code != 0 || stdout != "wrote womm.yaml (2 requirements)\n" || stderr != "" {
		t.Fatalf("capture: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if code, _, stderr := runBinary(t, dir, "capture"); code != 3 || !strings.Contains(stderr, "already exists") {
		t.Errorf("second capture: exit %d, stderr %q", code, stderr)
	}
	if code, stdout, stderr := runBinary(t, dir, "capture", "--nope"); code != 2 || !strings.Contains(stderr, "unknown flag") || !strings.Contains(stderr, "Usage:") || stdout != "" {
		t.Errorf("usage error: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if code, _, stderr := runBinary(t, dir, "frobnicate"); code != 2 || !strings.Contains(stderr, "unknown command") {
		t.Errorf("unknown command: exit %d, stderr %q", code, stderr)
	}

	empty := t.TempDir()
	if err := os.WriteFile(filepath.Join(empty, "womm.yaml"), []byte("version: 1\nrequirements: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, stdout, _ := runBinary(t, empty, "verify"); code != 0 || stdout != "0 requirements: 0 pass, 0 fail, 0 unknown, 0 unreachable\n" {
		t.Errorf("empty verify: exit %d, stdout %q", code, stdout)
	}

	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "womm.yaml"), []byte("version: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, stdout, stderr := runBinary(t, bad, "verify"); code != 3 || stdout != "" || !strings.Contains(stderr, "schema version 2") {
		t.Errorf("future schema: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if code, _, stderr := runBinary(t, t.TempDir(), "verify"); code != 3 || !strings.Contains(stderr, "cannot read") {
		t.Errorf("missing womm.yaml: exit %d, stderr %q", code, stderr)
	}

	// Real host, real inspector: only the contract's shape is asserted.
	code, stdout, stderr := runBinary(t, dir, "verify")
	if code != 0 && code != 1 && code != 3 {
		t.Fatalf("verify on the host: exit %d is outside the 0/1/3 verdict codes", code)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	var n, pass, fail, unknown, unreach int
	if _, err := fmt.Sscanf(lines[len(lines)-1], "%d requirements: %d pass, %d fail, %d unknown, %d unreachable", &n, &pass, &fail, &unknown, &unreach); err != nil || n != 2 {
		t.Fatalf("summary line missing or malformed (%v):\n%s", err, stdout)
	}
	want := 0
	switch {
	case unknown+unreach > 0 || strings.Contains(stderr, "error:"):
		want = 3
	case fail > 0:
		want = 1
	}
	if code != want {
		t.Errorf("exit %d does not follow the summary %q / stderr %q (want %d)", code, lines[len(lines)-1], stderr, want)
	}
	again, out2, _ := runBinary(t, dir, "verify")
	if again != code || out2 != stdout {
		t.Errorf("verify on the host is not repeatable: (%d,%q) vs (%d,%q)", code, stdout, again, out2)
	}
}
