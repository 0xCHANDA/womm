package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xCHANDA/womm/internal/core"
	"github.com/0xCHANDA/womm/internal/inspect"
	"github.com/0xCHANDA/womm/internal/inspect/toolpath"
)

type stubInspector struct {
	obs map[string]core.Observation
	err map[string]error
}

func (s stubInspector) Supports(name string) bool { _, ok := s.obs[name]; return ok }
func (s stubInspector) Inspect(_ context.Context, req core.Requirement) (core.Observation, error) {
	return s.obs[req.Name], s.err[req.Name]
}

func useInspector(t *testing.T, s stubInspector) {
	t.Helper()
	prev := newInspectors
	newInspectors = func(*toolpath.Resolver) []inspect.Inspector { return []inspect.Inspector{s} }
	t.Cleanup(func() { newInspectors = prev })
}

func writeWomm(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "womm.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const twoReqs = `version: 1
requirements:
  - name: node
    constraint: ">=22 <25"
    evidence:
      - source: package.json
        field: engines.node
        value: ">=22 <25"
  - name: pnpm
    constraint: 10.15.1
    evidence:
      - source: package.json
        field: packageManager
        value: pnpm@10.15.1
`

func TestVerifyCommandPass(t *testing.T) {
	useInspector(t, stubInspector{obs: map[string]core.Observation{
		"node": {Name: "node", Present: true, Version: "24.7.0"},
		"pnpm": {Name: "pnpm", Present: true, Version: "10.15.1"},
	}})
	dir := t.TempDir()
	writeWomm(t, dir, twoReqs)

	code, stdout, stderr := runCLI(t, "verify", dir)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	want := `PASS        node required >=22 <25; observed 24.7.0
            observed version satisfies the requirement
            evidence: package.json → engines.node = ">=22 <25"
PASS        pnpm required 10.15.1; observed 10.15.1
            observed version equals the required version
            evidence: package.json → packageManager = "pnpm@10.15.1"

2 requirements: 2 pass, 0 fail, 0 unknown, 0 unreachable
`
	if stdout != want {
		t.Errorf("stdout mismatch\n--- got ---\n%s--- want ---\n%s", stdout, want)
	}
	if stderr != "" {
		t.Errorf("stderr should be empty on a clean pass: %q", stderr)
	}
}

func TestVerifyCommandFail(t *testing.T) {
	useInspector(t, stubInspector{obs: map[string]core.Observation{
		"node": {Name: "node", Present: true, Version: "20.19.0"},
		"pnpm": {Name: "pnpm", Present: false},
	}})
	dir := t.TempDir()
	writeWomm(t, dir, twoReqs)

	code, stdout, stderr := runCLI(t, "verify", dir)
	if code != 1 {
		t.Fatalf("exit %d, want 1; stderr %q", code, stderr)
	}
	if !strings.HasPrefix(stdout, "FAIL        node required >=22 <25; observed 20.19.0\n") ||
		!strings.Contains(stdout, "FAIL        pnpm required 10.15.1; observed absent\n") ||
		!strings.HasSuffix(stdout, "2 requirements: 0 pass, 2 fail, 0 unknown, 0 unreachable\n") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if stderr != "" {
		t.Errorf("a conclusive FAIL must not print cobra's Error: line or usage; stderr %q", stderr)
	}
}

func TestVerifyCommandInconclusivePrecedence(t *testing.T) {
	probeErr := errors.New("version probe timed out after 5s")
	cases := []struct {
		name     string
		insp     stubInspector
		wantOut  []string
		wantErr  []string
		wantCode int
	}{
		{
			name: "fail + unreachable",
			insp: stubInspector{
				obs: map[string]core.Observation{"node": {Name: "node", Present: true, Version: "20.19.0"}, "pnpm": {Name: "pnpm", Present: true}},
				err: map[string]error{"pnpm": probeErr},
			},
			wantOut:  []string{"FAIL        node", "UNREACHABLE pnpm required 10.15.1; observed present, version unknown", "could not be queried: version probe timed out after 5s", "1 fail, 0 unknown, 1 unreachable"},
			wantCode: 3,
		},
		{
			name: "fail + unknown",
			insp: stubInspector{
				obs: map[string]core.Observation{"node": {Name: "node", Present: true, Version: "20.19.0"}, "pnpm": {Name: "pnpm", Present: true}},
			},
			wantOut:  []string{"FAIL        node", "UNKNOWN     pnpm", "1 fail, 1 unknown, 0 unreachable"},
			wantCode: 3,
		},
		{
			name: "invalid observed version",
			insp: stubInspector{
				obs: map[string]core.Observation{"node": {Name: "node", Present: true, Version: "24.7.0"}, "pnpm": {Name: "pnpm", Present: true, Version: "v10.15.1"}},
			},
			wantOut:  []string{"PASS        node", "UNKNOWN     pnpm required 10.15.1; observed v10.15.1", "observed version is invalid"},
			wantErr:  []string{`error: pnpm: invalid observed version "v10.15.1"`},
			wantCode: 3,
		},
		{
			name: "unsupported requirement",
			insp: stubInspector{
				obs: map[string]core.Observation{"node": {Name: "node", Present: true, Version: "24.7.0"}},
			},
			wantOut:  []string{"PASS        node", "1 requirement: 1 pass"},
			wantErr:  []string{`error: no inspector supports requirement: "pnpm"`},
			wantCode: 3,
		},
		{
			name: "inspection failure without presence",
			insp: stubInspector{
				obs: map[string]core.Observation{"node": {Name: "node", Present: true, Version: "24.7.0"}, "pnpm": {}},
				err: map[string]error{"pnpm": errors.New("resolving pnpm: permission denied")},
			},
			wantOut:  []string{"PASS        node", "1 requirement: 1 pass"},
			wantErr:  []string{"error: pnpm: inspection failed: resolving pnpm: permission denied"},
			wantCode: 3,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useInspector(t, tc.insp)
			dir := t.TempDir()
			writeWomm(t, dir, twoReqs)
			code, stdout, stderr := runCLI(t, "verify", dir)
			if code != tc.wantCode {
				t.Errorf("exit = %d, want %d", code, tc.wantCode)
			}
			for _, w := range tc.wantOut {
				if !strings.Contains(stdout, w) {
					t.Errorf("stdout lacks %q:\n%s", w, stdout)
				}
			}
			for _, w := range tc.wantErr {
				if !strings.Contains(stderr, w) {
					t.Errorf("stderr lacks %q:\n%s", w, stderr)
				}
			}
			if strings.Contains(stderr, "Usage:") || strings.Contains(stderr, "error: exit") {
				t.Errorf("stderr leaks cobra output:\n%s", stderr)
			}
		})
	}
}

func TestVerifyCommandConfigurationErrors(t *testing.T) {
	useInspector(t, stubInspector{obs: map[string]core.Observation{"node": {Name: "node", Present: true, Version: "24.7.0"}}})
	cases := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"malformed yaml", "version: 1\nrequirements: [\n", "not valid YAML"},
		{"missing version", "requirements: []\n", "no version field"},
		{"future version", "version: 2\nrequirements: []\n", "schema version 2"},
		{"missing evidence", "version: 1\nrequirements:\n  - name: node\n    constraint: '>=22'\n", "evidence"},
		{"duplicate names", "version: 1\nrequirements:\n  - name: node\n    constraint: '>=22'\n    evidence: [{source: a, field: b}]\n  - name: node\n    constraint: '>=23'\n    evidence: [{source: a, field: b}]\n", "duplicate name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeWomm(t, dir, tc.body)
			code, stdout, stderr := runCLI(t, "verify", dir)
			if code != 3 {
				t.Errorf("exit = %d, want 3", code)
			}
			if stdout != "" {
				t.Errorf("nothing must be reported for a file that does not load; stdout %q", stdout)
			}
			if !strings.Contains(stderr, tc.wantErr) {
				t.Errorf("stderr = %q, want containing %q", stderr, tc.wantErr)
			}
		})
	}

	t.Run("missing file", func(t *testing.T) {
		dir := t.TempDir()
		code, _, stderr := runCLI(t, "verify", dir)
		if code != 3 || !strings.Contains(stderr, "no womm.yaml at "+filepath.Join(dir, "womm.yaml")) || !strings.Contains(stderr, "run `womm capture") {
			t.Errorf("exit %d, stderr %q", code, stderr)
		}
	})
	t.Run("file passed positionally", func(t *testing.T) {
		dir := t.TempDir()
		path := writeWomm(t, dir, "version: 1\nrequirements: []\n")
		code, _, stderr := runCLI(t, "verify", path)
		if code != 3 || !strings.Contains(stderr, "use --file") {
			t.Errorf("exit %d, stderr %q", code, stderr)
		}
	})
	t.Run("invalid constraint in file", func(t *testing.T) {
		dir := t.TempDir()
		writeWomm(t, dir, "version: 1\nrequirements:\n  - name: node\n    constraint: 'not a range'\n    evidence: [{source: a, field: b}]\n")
		code, stdout, stderr := runCLI(t, "verify", dir)
		if code != 3 || !strings.Contains(stdout, "UNKNOWN     node") || !strings.Contains(stderr, "invalid requirement constraint") {
			t.Errorf("exit %d\nstdout %s\nstderr %s", code, stdout, stderr)
		}
	})
}

func TestVerifyCommandWarningsAndFileFlag(t *testing.T) {
	useInspector(t, stubInspector{obs: map[string]core.Observation{"node": {Name: "node", Present: true, Version: "24.7.0"}}})
	custom := filepath.Join(t.TempDir(), "spec.yaml")
	if err := os.WriteFile(custom, []byte("version: 1\nextra: true\nrequirements:\n  - name: node\n    constraint: '>=22'\n    evidence: [{source: a, field: b}]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCLI(t, "verify", "-f", custom)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "PASS        node") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, `warning: unknown key "extra"`) {
		t.Errorf("warnings must reach stderr: %q", stderr)
	}
}

func TestVerifyCommandEmptyFileIsPass(t *testing.T) {
	useInspector(t, stubInspector{})
	dir := t.TempDir()
	writeWomm(t, dir, "version: 1\nrequirements: []\n")
	code, stdout, _ := runCLI(t, "verify", dir)
	if code != 0 || stdout != "0 requirements: 0 pass, 0 fail, 0 unknown, 0 unreachable\n" {
		t.Errorf("exit %d, stdout %q", code, stdout)
	}
}

func TestVerifyCommandUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"verify", "--bogus"}, {"verify", "a", "b"}} {
		code, _, stderr := runCLI(t, args...)
		if code != 2 || !strings.Contains(stderr, "Usage:") {
			t.Errorf("%v: exit %d, stderr %q; want 2 with usage", args, code, stderr)
		}
	}
}

func TestVerifyCommandIsDeterministic(t *testing.T) {
	useInspector(t, stubInspector{
		obs: map[string]core.Observation{"node": {Name: "node", Present: true, Version: "20.19.0"}, "pnpm": {Name: "pnpm", Present: true}},
		err: map[string]error{"pnpm": errors.New("version probe failed: exit status 7")},
	})
	dir := t.TempDir()
	writeWomm(t, dir, twoReqs)
	c1, o1, e1 := runCLI(t, "verify", dir)
	c2, o2, e2 := runCLI(t, "verify", dir)
	if c1 != c2 || o1 != o2 || e1 != e2 {
		t.Errorf("repeated runs differ: (%d,%q,%q) vs (%d,%q,%q)", c1, o1, e1, c2, o2, e2)
	}
}

// cancelStub cancels the CLI context from inside the first probe, the
// way a Ctrl-C lands while a tool is being queried.
type cancelStub struct{ cancel context.CancelFunc }

func (c cancelStub) Supports(string) bool { return true }
func (c cancelStub) Inspect(_ context.Context, req core.Requirement) (core.Observation, error) {
	c.cancel()
	return core.Observation{Name: req.Name, Present: true}, context.Canceled
}

// TestVerifyCommandCancelledPrintsNoVerdict: an interrupted run must not
// print any PASS/FAIL line — partial output reads as a result.
func TestVerifyCommandCancelledPrintsNoVerdict(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prev := newInspectors
	newInspectors = func(*toolpath.Resolver) []inspect.Inspector { return []inspect.Inspector{cancelStub{cancel}} }
	t.Cleanup(func() { newInspectors = prev })

	dir := t.TempDir()
	writeWomm(t, dir, twoReqs)
	var out, errb bytes.Buffer
	code := run(ctx, []string{"verify", dir}, &out, &errb)
	if code != 3 {
		t.Errorf("exit %d, want 3", code)
	}
	if out.Len() != 0 {
		t.Errorf("stdout must be empty after cancellation, got:\n%s", out.String())
	}
	if errb.String() != "error: verification cancelled; no result\n" {
		t.Errorf("stderr = %q", errb.String())
	}
}

// TestVerifyCommandShowsExecutedPath pins the user-visible half of the
// executed-path evidence: stdout says which binary answered; the
// partial observation of an unreachable tool still names it.
func TestVerifyCommandShowsExecutedPath(t *testing.T) {
	useInspector(t, stubInspector{
		obs: map[string]core.Observation{
			"node": {Name: "node", Present: true, Version: "24.7.0", Path: "/home/u/.volta/bin/node"},
			"pnpm": {Name: "pnpm", Present: true, Path: "/opt/pnpm/pnpm"},
		},
		err: map[string]error{"pnpm": errors.New("version probe timed out after 5s")},
	})
	dir := t.TempDir()
	writeWomm(t, dir, twoReqs)

	code, stdout, _ := runCLI(t, "verify", dir)
	if code != 3 {
		t.Fatalf("exit %d, want 3 (unreachable)", code)
	}
	for _, want := range []string{
		"PASS        node required >=22 <25; observed 24.7.0 at /home/u/.volta/bin/node\n",
		"UNREACHABLE pnpm required 10.15.1; observed present at /opt/pnpm/pnpm, version unknown\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}
