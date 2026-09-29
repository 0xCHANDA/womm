package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runCLI(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(context.Background(), args, &out, &errb)
	return code, out.String(), errb.String()
}

const fullProjectYAML = `version: 1
requirements:
  - name: node
    constraint: '>=22 <25'
    evidence:
      - source: package.json
        field: engines.node
        value: '>=22 <25'
  - name: npm
    constraint: 11.2.0
    evidence:
      - source: package.json
        field: packageManager
        value: npm@11.2.0
`

func TestCaptureCommandWritesWommYAML(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"package.json": `{"engines":{"node":">=22 <25"},"packageManager":"npm@11.2.0"}`,
	})
	code, stdout, stderr := runCLI(t, "capture", dir)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if want := "wrote " + filepath.Join(dir, "womm.yaml") + " (2 requirements)\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	got, err := os.ReadFile(filepath.Join(dir, "womm.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != fullProjectYAML {
		t.Errorf("womm.yaml mismatch:\n%s", got)
	}

	// Second run: the file exists, no --force → execution error (3),
	// file untouched.
	code, _, stderr = runCLI(t, "capture", dir)
	if code != 3 || !strings.Contains(stderr, "already exists") || !strings.Contains(stderr, "--force") {
		t.Fatalf("exit %d, stderr %q; want 3 with an 'already exists' hint", code, stderr)
	}
	if strings.Contains(stderr, "Usage:") {
		t.Errorf("an execution error must not print usage:\n%s", stderr)
	}

	// --force overwrites deterministically.
	code, _, stderr = runCLI(t, "capture", "--force", dir)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	again, _ := os.ReadFile(filepath.Join(dir, "womm.yaml"))
	if !bytes.Equal(again, got) {
		t.Errorf("repeated capture is not byte-identical:\n%s\n---\n%s", again, got)
	}
}

func TestCaptureCommandOutputFlagAndSingular(t *testing.T) {
	dir := writeProject(t, map[string]string{".nvmrc": "22.14.0\n"})
	out := filepath.Join(t.TempDir(), "custom.yaml")
	code, stdout, stderr := runCLI(t, "capture", "-o", out, dir)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if want := "wrote " + out + " (1 requirement)\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "womm.yaml")); !os.IsNotExist(err) {
		t.Errorf("default output was written despite --output: %v", err)
	}
}

func TestCaptureCommandDefaultsToCurrentDirectory(t *testing.T) {
	dir := writeProject(t, map[string]string{"package.json": `{"name":"x"}`})
	wd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	code, stdout, stderr := runCLI(t, "capture")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if stdout != "wrote womm.yaml (0 requirements)\n" {
		t.Errorf("stdout = %q", stdout)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "womm.yaml"))
	if string(got) != "version: 1\nrequirements: []\n" {
		t.Errorf("empty capture must be explicit: %q", got)
	}
}

func TestCaptureCommandExitCodes(t *testing.T) {
	conflict := writeProject(t, map[string]string{
		"package.json": `{"engines":{"node":">=22"}}`,
		".nvmrc":       "20.19.0\n",
	})
	malformed := writeProject(t, map[string]string{"package.json": `{"engines":`})
	unsupported := writeProject(t, map[string]string{".nvmrc": "lts/*\n"})

	cases := []struct {
		name     string
		args     []string
		wantCode int
		wantErr  string
	}{
		{"conflicting declarations", []string{"capture", conflict}, 3, "evidence conflict"},
		{"malformed package.json", []string{"capture", malformed}, 3, "malformed package.json"},
		{"unsupported selector", []string{"capture", unsupported}, 3, "not supported"},
		{"missing project", []string{"capture", filepath.Join(t.TempDir(), "nope")}, 3, "no such file"},
		{"too many args", []string{"capture", "a", "b"}, 2, "accepts at most 1 arg"},
		{"unknown flag", []string{"capture", "--bogus"}, 2, "unknown flag"},
		{"unknown command", []string{"captur"}, 2, "unknown command"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _, stderr := runCLI(t, tc.args...)
			if code != tc.wantCode {
				t.Errorf("exit = %d, want %d (stderr %q)", code, tc.wantCode, stderr)
			}
			if !strings.Contains(stderr, tc.wantErr) {
				t.Errorf("stderr = %q, want containing %q", stderr, tc.wantErr)
			}
		})
	}
	for _, dir := range []string{conflict, malformed, unsupported} {
		if _, err := os.Stat(filepath.Join(dir, "womm.yaml")); !os.IsNotExist(err) {
			t.Errorf("a failed capture wrote %s/womm.yaml", dir)
		}
	}
}

func TestCaptureCommandFlagsDoNotLeakBetweenRuns(t *testing.T) {
	a := writeProject(t, map[string]string{"package.json": `{"name":"a"}`})
	custom := filepath.Join(t.TempDir(), "a.yaml")
	if code, _, stderr := runCLI(t, "capture", "-o", custom, a); code != 0 {
		t.Fatal(stderr)
	}
	b := writeProject(t, map[string]string{"package.json": `{"name":"b"}`})
	if code, _, stderr := runCLI(t, "capture", b); code != 0 {
		t.Fatal(stderr)
	}
	if _, err := os.Stat(filepath.Join(b, "womm.yaml")); err != nil {
		t.Errorf("second run reused the first run's --output: %v", err)
	}
}

func TestVersionCommand(t *testing.T) {
	code, stdout, _ := runCLI(t, "version")
	if code != 0 || stdout != "womm version "+version+"\n" {
		t.Errorf("exit %d, stdout %q", code, stdout)
	}
}
