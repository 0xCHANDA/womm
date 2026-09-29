package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Acceptance matrix (Campaign II item 6): realistic package.json /
// .nvmrc / womm.yaml shapes crossed with host profiles, asserted with
// focused checks (captured constraint, evidence, verify verdict line,
// exit code, stderr) rather than whole-file goldens.

type captureCase struct {
	project string
	// wantReqs maps requirement name → captured constraint (for a
	// successful capture); wantErr is a stderr fragment for exit 3.
	wantReqs map[string]string
	wantErr  string
	// wantEvidence maps requirement name → evidence value literal.
	wantEvidence map[string]string
}

func TestAcceptanceCapture(t *testing.T) {
	cases := []captureCase{
		{project: "caret", wantReqs: map[string]string{"node": "^20.10.0"}},
		{project: "tilde", wantReqs: map[string]string{"node": "~22.14.0"}},
		{project: "xrange", wantReqs: map[string]string{"node": "22.x"}},
		{project: "hyphen", wantReqs: map[string]string{"node": "20.10.0 - 22.14.0"}},
		{project: "whitespace", wantReqs: map[string]string{"node": ">=20 <25", "npm": "11.2.0"},
			wantEvidence: map[string]string{"node": "  >= 20 \t <  25  ", "npm": "npm@11.2.0"}},
		{project: "bom", wantReqs: map[string]string{"node": ">=22"}},
		{project: "hash-sha1", wantReqs: map[string]string{"npm": "11.2.0"},
			wantEvidence: map[string]string{"npm": "npm@11.2.0+sha1." + strings.Repeat("b", 40)}},
		{project: "pm-yarn-hash224", wantReqs: map[string]string{"yarn": "3.2.3"}},
		{project: "hash-bad", wantErr: `integrity hash algorithm "sha256" is not supported`},
		{project: "nvmrc-comments", wantReqs: map[string]string{"node": "22.14.0"},
			wantEvidence: map[string]string{"node": "22.14.0"}},
		{project: "nvmrc-settings", wantReqs: map[string]string{"node": "22.14.0"}},
		{project: "nvmrc-dup-settings", wantErr: `setting "FOO" appears more than once`},
		{project: "nvmrc-alias", wantErr: `"lts/*" is valid nvm syntax but is not supported`},
		{project: "nvmrc-node-setting", wantErr: `"node=22.14.0" is invalid for nvm`},
		{project: "nvmrc-empty", wantErr: "declares no version"},
		// engines.npm is not a source in v0.1: ignored, node only.
		{project: "engines-npm-ignored", wantReqs: map[string]string{"node": ">=22"}},
	}
	for _, tc := range cases {
		t.Run(tc.project, func(t *testing.T) {
			dir := stageProject(t, tc.project)
			code, stdout, stderr := runCLI(t, "capture", dir)
			if tc.wantErr != "" {
				if code != 3 || !strings.Contains(stderr, tc.wantErr) {
					t.Fatalf("exit %d, stderr %q; want 3 containing %q", code, stderr, tc.wantErr)
				}
				if _, err := os.Stat(filepath.Join(dir, "womm.yaml")); !os.IsNotExist(err) {
					t.Errorf("failed capture wrote womm.yaml")
				}
				return
			}
			if code != 0 {
				t.Fatalf("exit %d, stderr %q, stdout %q", code, stderr, stdout)
			}
			yaml, err := os.ReadFile(filepath.Join(dir, "womm.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			got := parseCaptured(t, string(yaml))
			if len(got) != len(tc.wantReqs) {
				t.Fatalf("captured %v, want %v\n%s", got, tc.wantReqs, yaml)
			}
			for name, want := range tc.wantReqs {
				if got[name] != want {
					t.Errorf("%s: constraint %q, want %q\n%s", name, got[name], want, yaml)
				}
			}
			for name, ev := range tc.wantEvidence {
				if !strings.Contains(string(yaml), "value: "+yamlScalar(ev)) {
					t.Errorf("%s: evidence value %q not found verbatim in:\n%s", name, ev, yaml)
				}
			}
			// Determinism: a second capture with --force is byte-identical.
			if code, _, stderr := runCLI(t, "capture", "--force", dir); code != 0 {
				t.Fatal(stderr)
			}
			again, _ := os.ReadFile(filepath.Join(dir, "womm.yaml"))
			if string(again) != string(yaml) {
				t.Errorf("repeated capture differs")
			}
		})
	}
}

// parseCaptured extracts name → constraint pairs from a captured file
// without depending on the schema package's internals.
func parseCaptured(t *testing.T, yaml string) map[string]string {
	t.Helper()
	out := map[string]string{}
	var name string
	for _, line := range strings.Split(yaml, "\n") {
		trim := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trim, "- name: "):
			name = strings.TrimPrefix(trim, "- name: ")
		case strings.HasPrefix(trim, "constraint: ") && name != "":
			out[name] = unquote(strings.TrimPrefix(trim, "constraint: "))
			name = ""
		}
	}
	return out
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
	}
	return s
}

// yamlScalar renders how the encoder spells a value that needs quoting
// for the substring check; plain values are returned as is.
func yamlScalar(v string) string {
	if strings.ContainsAny(v, "\t\n") || strings.HasPrefix(v, " ") || strings.HasSuffix(v, " ") || strings.HasPrefix(v, ">") || strings.HasPrefix(v, "^") || strings.Contains(v, " ") {
		// yaml.v3 double-quotes strings with tabs/newlines and
		// single-quotes the rest that need quoting.
		if strings.ContainsAny(v, "\t\n") {
			return `"` + strings.NewReplacer("\t", `\t`, "\n", `\n`, `"`, `\"`).Replace(v) + `"`
		}
		return "'" + strings.ReplaceAll(v, "'", "''") + "'"
	}
	return v
}

func TestAcceptanceVerify(t *testing.T) {
	type vc struct {
		name     string
		project  string
		machine  machine
		wantCode int
		wantLine string // status line prefix, e.g. "PASS        node"
		wantErr  string
	}
	cases := []vc{
		{"caret inside", "caret", machine{"node": present("node", "20.19.0")}, 0, "PASS        node required ^20.10.0; observed 20.19.0", ""},
		{"caret below", "caret", machine{"node": present("node", "20.9.0")}, 1, "FAIL        node required ^20.10.0; observed 20.9.0", ""},
		{"caret next major", "caret", machine{"node": present("node", "21.0.0")}, 1, "FAIL        node", ""},
		{"tilde patch", "tilde", machine{"node": present("node", "22.14.9")}, 0, "PASS        node required ~22.14.0", ""},
		{"tilde next minor", "tilde", machine{"node": present("node", "22.15.0")}, 1, "FAIL        node required ~22.14.0", ""},
		{"x range", "xrange", machine{"node": present("node", "22.99.1")}, 0, "PASS        node required 22.x", ""},
		{"x range other major", "xrange", machine{"node": present("node", "23.0.0")}, 1, "FAIL        node required 22.x", ""},
		{"hyphen inclusive upper", "hyphen", machine{"node": present("node", "22.14.0")}, 0, "PASS        node required 20.10.0 - 22.14.0", ""},
		{"hyphen above", "hyphen", machine{"node": present("node", "22.14.1")}, 1, "FAIL        node required 20.10.0 - 22.14.0", ""},
		{"whitespace normalized both", "whitespace", machine{"node": present("node", "24.7.0"), "npm": present("npm", "11.2.0")}, 0, "PASS        node required >=20 <25; observed 24.7.0", ""},
		{"prerelease host against caret", "caret", machine{"node": present("node", "20.19.0-nightly.1")}, 3, "UNKNOWN     node required ^20.10.0; observed 20.19.0-nightly.1", "prerelease observed against a version range"},
		{"prerelease host against exact", "nvmrc-comments", machine{"node": present("node", "22.14.0-rc.1")}, 1, "FAIL        node required 22.14.0; observed 22.14.0-rc.1", ""},
		{"sha1 pin match", "hash-sha1", machine{"npm": present("npm", "11.2.0")}, 0, "PASS        npm required 11.2.0; observed 11.2.0", ""},
		{"sha224 yarn pin mismatch", "pm-yarn-hash224", machine{"yarn": present("yarn", "4.0.0")}, 1, "FAIL        yarn required 3.2.3; observed 4.0.0", ""},
		{"multiple fail", "whitespace", machine{"node": present("node", "18.0.0"), "npm": absent("npm")}, 1, "FAIL        npm  required 11.2.0; observed absent", ""},
		{"unknown + fail", "whitespace", machine{"node": unknownVersion("node"), "npm": absent("npm")}, 3, "UNKNOWN     node", ""},
		{"unreachable + pass", "whitespace", machine{"node": present("node", "24.7.0"), "npm": unreachable("npm", "version probe failed: exit status 1")}, 3, "UNREACHABLE npm", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := stageProject(t, tc.project)
			if code, _, stderr := runCLI(t, "capture", dir); code != 0 {
				t.Fatalf("capture: %s", stderr)
			}
			onMachine(t, tc.machine)
			code, stdout, stderr := runCLI(t, "verify", dir)
			if code != tc.wantCode {
				t.Errorf("exit %d, want %d\n%s%s", code, tc.wantCode, stdout, stderr)
			}
			if !strings.Contains(stdout, tc.wantLine) {
				t.Errorf("stdout lacks %q:\n%s", tc.wantLine, stdout)
			}
			if tc.wantErr == "" && stderr != "" {
				t.Errorf("unexpected stderr: %q", stderr)
			}
			if tc.wantErr != "" && !strings.Contains(stderr, tc.wantErr) {
				t.Errorf("stderr lacks %q: %q", tc.wantErr, stderr)
			}
			c2, o2, e2 := runCLI(t, "verify", dir)
			if c2 != code || o2 != stdout || e2 != stderr {
				t.Errorf("verify is not repeatable")
			}
		})
	}
}

// TestAcceptanceWommYAMLShapes covers hand-written womm.yaml variants
// against a machine where every tool is present and satisfying.
func TestAcceptanceWommYAMLShapes(t *testing.T) {
	onMachine(t, machine{"node": present("node", "24.7.0"), "npm": present("npm", "11.2.0")})
	ev := "    evidence: [{source: package.json, field: engines.node, value: x}]\n"
	cases := []struct {
		name     string
		body     string
		wantCode int
		wantOut  string
		wantErr  string
	}{
		{"current schema", "version: 1\nrequirements:\n  - name: node\n    constraint: '>=22'\n" + ev, 0, "PASS        node", ""},
		{"unknown top-level key warns", "version: 1\nfoo: 1\nrequirements:\n  - name: node\n    constraint: '>=22'\n" + ev, 0, "PASS        node", `warning: unknown key "foo"`},
		{"unknown requirement key warns", "version: 1\nrequirements:\n  - name: node\n    constraint: '>=22'\n    extra: true\n" + ev, 0, "PASS        node", `warning: unknown key "extra"`},
		{"future schema", "version: 2\nrequirements: []\n", 3, "", "schema version 2"},
		{"version zero", "version: 0\nrequirements: []\n", 3, "", "obsolete schema version 0"},
		{"missing version", "requirements: []\n", 3, "", "no version field"},
		{"malformed", "version: 1\nrequirements: [\n", 3, "", "not valid YAML"},
		{"duplicate requirement", "version: 1\nrequirements:\n  - name: node\n    constraint: '>=22'\n" + ev + "  - name: node\n    constraint: '>=23'\n" + ev, 3, "", "duplicate name"},
		{"missing evidence", "version: 1\nrequirements:\n  - name: node\n    constraint: '>=22'\n", 3, "", "evidence entry is required"},
		{"evidence without field", "version: 1\nrequirements:\n  - name: node\n    constraint: '>=22'\n    evidence: [{source: a}]\n", 3, "", "evidence[0].field is required"},
		{"invalid name", "version: 1\nrequirements:\n  - name: 'no de'\n    constraint: '>=22'\n" + ev, 3, "", "must not contain whitespace"},
		{"empty constraint", "version: 1\nrequirements:\n  - name: node\n    constraint: ''\n" + ev, 3, "", "constraint is required"},
		{"present constraint", "version: 1\nrequirements:\n  - name: npm\n    constraint: present\n" + ev, 0, "PASS        npm required present; observed 11.2.0", ""},
		{"services section", "version: 1\nrequirements: []\nservices:\n  redis:\n    version: '7'\n", 3, "0 requirements", `service "redis"`},
		{"environment section", "version: 1\nrequirements: []\nenvironment:\n  optional: [DEBUG]\n", 3, "0 requirements", "environment section declares 1 variable"},
		{"numeric constraint scalar", "version: 1\nrequirements:\n  - name: node\n    constraint: 24\n" + ev, 0, "PASS        node required 24; observed 24.7.0", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeWomm(t, dir, tc.body)
			code, stdout, stderr := runCLI(t, "verify", dir)
			if code != tc.wantCode {
				t.Errorf("exit %d, want %d\nstdout %s\nstderr %s", code, tc.wantCode, stdout, stderr)
			}
			if tc.wantOut != "" && !strings.Contains(stdout, tc.wantOut) {
				t.Errorf("stdout lacks %q:\n%s", tc.wantOut, stdout)
			}
			if tc.wantOut == "" && stdout != "" {
				t.Errorf("unexpected stdout: %q", stdout)
			}
			if tc.wantErr != "" && !strings.Contains(stderr, tc.wantErr) {
				t.Errorf("stderr lacks %q: %q", tc.wantErr, stderr)
			}
			if tc.wantErr == "" && stderr != "" {
				t.Errorf("unexpected stderr: %q", stderr)
			}
		})
	}
}
