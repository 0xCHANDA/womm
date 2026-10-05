package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func goProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := realDir(t)
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCaptureReadsTheGoDirectiveNextToNode(t *testing.T) {
	dir := goProject(t, map[string]string{
		"go.mod":       "module example.com/m\n\ngo 1.24.1\n\ntoolchain go1.24.7\n",
		"package.json": `{"engines":{"node":">=22"}}`,
	})
	code, stdout, stderr := runCLI(t, "capture", dir)
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "womm.yaml"))
	want := `version: 1
requirements:
  - name: go
    constraint: '>=1.24.1'
    evidence:
      - source: go.mod
        field: go
        value: 1.24.1
  - name: node
    constraint: '>=22'
    evidence:
      - source: package.json
        field: engines.node
        value: '>=22'
`
	if string(data) != want {
		t.Errorf("womm.yaml mismatch\n--- got ---\n%s--- want ---\n%s", data, want)
	}
}

func TestCaptureRefusesAnAmbiguousGoDirectiveAndWritesNothing(t *testing.T) {
	dir := goProject(t, map[string]string{"go.mod": "module m\ngo 1.21\ngo 1.22\n"})
	code, _, stderr := runCLI(t, "capture", dir)
	if code != 3 || !strings.Contains(stderr, "repeated go directive") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "womm.yaml")); err == nil {
		t.Error("womm.yaml was written despite the detection error")
	}
}

func goYAML(constraint string) string {
	return "version: 1\nrequirements:\n  - name: go\n    constraint: '" + constraint + "'\n    evidence:\n      - source: go.mod\n        field: go\n        value: x\n"
}

// verify with the REAL inspector against fake go binaries, through the
// CLI: found in a user directory, versions in Go order, the JSON carries
// the path, and a development build is UNKNOWN, never a guess.
func TestVerifyGoEndToEndWithTheRealInspector(t *testing.T) {
	sys, user := realDir(t), realDir(t)
	useSystemDirs(t, sys)
	project := goProject(t, map[string]string{"womm.yaml": goYAML(">=1.24")})

	// Absent: FAIL.
	if code, out, _ := runCLI(t, "verify", project); code != 1 || !strings.Contains(out, "FAIL        go required >=1.24; observed absent") {
		t.Fatalf("absent: exit %d\n%s", code, out)
	}

	goBin := fakeTool(t, user, "go", "echo 'go version go1.24.7 linux/amd64'\n")
	code, out, stderr := runCLI(t, "verify", project, "--tool-dir", user)
	if code != 0 || !strings.Contains(out, "PASS        go required >=1.24; observed 1.24.7 at "+goBin+"\n") {
		t.Fatalf("pass: exit %d\n%s\n%s", code, out, stderr)
	}

	// An rc satisfies a language version but not the first release.
	fakeTool(t, user, "go", "echo 'go version go1.25rc1 linux/amd64'\n")
	if code, out, _ := runCLI(t, "verify", writeProject(t, map[string]string{"womm.yaml": goYAML(">=1.25")}), "--tool-dir", user); code != 0 {
		t.Errorf("1.25rc1 vs >=1.25: exit %d\n%s", code, out)
	}
	if code, out, _ := runCLI(t, "verify", writeProject(t, map[string]string{"womm.yaml": goYAML(">=1.25.0")}), "--tool-dir", user); code != 1 {
		t.Errorf("1.25rc1 vs >=1.25.0: exit %d\n%s", code, out)
	}

	// A development build or garbage: UNKNOWN, exit 3.
	fakeTool(t, user, "go", "echo 'go version devel go1.26-abc123 Mon Jan 1 00:00:00 2026 linux/amd64'\n")
	if code, out, _ := runCLI(t, "verify", project, "--tool-dir", user); code != 3 || !strings.HasPrefix(out, "UNKNOWN") {
		t.Errorf("devel: exit %d\n%s", code, out)
	}

	// Broken go: UNREACHABLE.
	fakeTool(t, user, "go", "echo boom >&2\nexit 2\n")
	if code, out, _ := runCLI(t, "verify", project, "--tool-dir", user); code != 3 || !strings.HasPrefix(out, "UNREACHABLE go") {
		t.Errorf("broken: exit %d\n%s", code, out)
	}

	// JSON.
	fakeTool(t, user, "go", "echo 'go version go1.24.7 linux/amd64'\n")
	_, jout, _ := runCLI(t, "verify", project, "--tool-dir", user, "--format", "json")
	doc := decodeJSONOnly(t, jout)
	if len(doc.Requirements) != 1 || doc.Requirements[0].Name != "go" || doc.Requirements[0].Status != "pass" ||
		doc.Requirements[0].Observation.Version == nil || *doc.Requirements[0].Observation.Version != "1.24.7" {
		t.Errorf("json: %+v", doc)
	}
}

// A requirement for go can never be satisfied by the node inspector and
// vice versa: names select the inspector.
func TestVerifyGoRequirementIsNotProbedByNode(t *testing.T) {
	sys := realDir(t)
	useSystemDirs(t, sys)
	marker := filepath.Join(sys, "ran")
	fakeTool(t, sys, "node", "touch '"+marker+"'\necho v24.7.0\n")
	project := goProject(t, map[string]string{"womm.yaml": goYAML(">=1.24")})
	runCLI(t, "verify", project)
	if _, err := os.Stat(marker); err == nil {
		t.Error("verifying a go requirement executed node")
	}
}
