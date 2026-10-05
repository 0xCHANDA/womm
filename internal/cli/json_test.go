package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/0xCHANDA/womm/internal/core"
	"github.com/0xCHANDA/womm/internal/inspect"
	"github.com/0xCHANDA/womm/internal/inspect/toolpath"
)

// jsonOut mirrors the public schema (docs/output-json.md) for strict
// decoding: any field the CLI adds or renames fails DisallowUnknownFields.
type jsonOut struct {
	SchemaVersion int    `json:"schemaVersion"`
	Result        string `json:"result"`
	ExitCode      int    `json:"exitCode"`
	Requirements  []struct {
		Name        string `json:"name"`
		Constraint  string `json:"constraint"`
		Status      string `json:"status"`
		Reason      string `json:"reason"`
		Observation struct {
			Present bool    `json:"present"`
			Version *string `json:"version"`
			Path    *string `json:"path"`
		} `json:"observation"`
		Evidence []struct {
			Source string `json:"source"`
			Field  string `json:"field"`
			Value  string `json:"value"`
		} `json:"evidence"`
	} `json:"requirements"`
	Errors []struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
	} `json:"errors"`
	Summary struct {
		Total       int `json:"total"`
		Pass        int `json:"pass"`
		Fail        int `json:"fail"`
		Unknown     int `json:"unknown"`
		Unreachable int `json:"unreachable"`
	} `json:"summary"`
}

// decodeJSONOnly proves stdout is exactly one JSON document and nothing
// else: strict field set, no trailing data of any kind.
func decodeJSONOnly(t *testing.T, stdout string) jsonOut {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(stdout))
	dec.DisallowUnknownFields()
	var out jsonOut
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		t.Fatalf("stdout has data after the JSON document (%v):\n%s", err, stdout)
	}
	if !strings.HasSuffix(stdout, "}\n") {
		t.Fatalf("stdout must end with the document and one newline: %q", stdout[max(0, len(stdout)-10):])
	}
	return out
}

var humanSummary = regexp.MustCompile(`(?m)^(\d+) requirements?: (\d+) pass, (\d+) fail, (\d+) unknown, (\d+) unreachable$`)

const emptyReqs = "version: 1\nrequirements: []\n"

func TestVerifyFormatJSONMatchesHumanModeEverywhereElse(t *testing.T) {
	cases := []struct {
		name     string
		yaml     string
		obs      map[string]core.Observation
		err      map[string]error
		wantCode int
		result   string
		errKinds []string
	}{
		{"all pass", twoReqs, map[string]core.Observation{
			"node": {Name: "node", Present: true, Version: "24.7.0", Path: "/usr/bin/node"},
			"pnpm": {Name: "pnpm", Present: true, Version: "10.15.1", Path: "/usr/bin/pnpm"}}, nil, 0, "pass", nil},
		{"one fail", twoReqs, map[string]core.Observation{
			"node": {Name: "node", Present: true, Version: "24.7.0"},
			"pnpm": {Name: "pnpm", Present: false}}, nil, 1, "fail", nil},
		{"unknown version", twoReqs, map[string]core.Observation{
			"node": {Name: "node", Present: true},
			"pnpm": {Name: "pnpm", Present: true, Version: "10.15.1"}}, nil, 3, "inconclusive", nil},
		{"unreachable", twoReqs, map[string]core.Observation{
			"node": {Name: "node", Present: true, Path: "/opt/node/bin/node"},
			"pnpm": {Name: "pnpm", Present: true, Version: "10.15.1"}},
			map[string]error{"node": errors.New("version probe timed out after 5s")}, 3, "inconclusive", nil},
		{"fail and unknown: 3 beats 1", twoReqs, map[string]core.Observation{
			"node": {Name: "node", Present: true},
			"pnpm": {Name: "pnpm", Present: false}}, nil, 3, "inconclusive", nil},
		{"prerelease against a range", twoReqs, map[string]core.Observation{
			"node": {Name: "node", Present: true, Version: "24.7.0-rc.1"},
			"pnpm": {Name: "pnpm", Present: true, Version: "10.15.1"}}, nil, 3, "inconclusive", []string{"undecidable"}},
		{"unsupported requirement", twoReqs, map[string]core.Observation{
			"node": {Name: "node", Present: true, Version: "24.7.0"}}, nil, 3, "inconclusive", []string{"unsupported_requirement"}},
		{"inspection failed without presence", twoReqs, map[string]core.Observation{
			"node": {Name: "node", Present: true, Version: "24.7.0"},
			"pnpm": {}}, map[string]error{"pnpm": errors.New("resolving pnpm: boom")}, 3, "inconclusive", []string{"inspection_failed"}},
		{"zero requirements", emptyReqs, map[string]core.Observation{}, nil, 0, "pass", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useInspector(t, stubInspector{obs: tc.obs, err: tc.err})
			dir := t.TempDir()
			writeWomm(t, dir, tc.yaml)

			hCode, hOut, hErr := runCLI(t, "verify", dir)
			jCode, jOut, jErr := runCLI(t, "verify", dir, "--format", "json")
			if hCode != tc.wantCode || jCode != tc.wantCode {
				t.Fatalf("exit codes human %d, json %d, want %d", hCode, jCode, tc.wantCode)
			}
			if hErr != jErr {
				t.Errorf("stderr differs between formats\nhuman: %q\njson:  %q", hErr, jErr)
			}
			doc := decodeJSONOnly(t, jOut)
			if doc.SchemaVersion != 1 || doc.ExitCode != tc.wantCode || doc.Result != tc.result {
				t.Errorf("schemaVersion %d result %q exitCode %d, want 1 %q %d", doc.SchemaVersion, doc.Result, doc.ExitCode, tc.result, tc.wantCode)
			}
			// The document counts what the human report counts.
			m := humanSummary.FindStringSubmatch(hOut)
			if m == nil {
				t.Fatalf("no summary line in the human report:\n%s", hOut)
			}
			n := func(i int) int { v, _ := strconv.Atoi(m[i]); return v }
			s := doc.Summary
			if s.Total != n(1) || s.Pass != n(2) || s.Fail != n(3) || s.Unknown != n(4) || s.Unreachable != n(5) || len(doc.Requirements) != s.Total {
				t.Errorf("summary %+v / %d requirements disagrees with the human report %q", s, len(doc.Requirements), m[0])
			}
			var kinds []string
			for _, e := range doc.Errors {
				kinds = append(kinds, e.Kind)
				if !strings.Contains(hErr, e.Message) {
					t.Errorf("error %q is in the JSON but not on stderr %q", e.Message, hErr)
				}
			}
			if strings.Join(kinds, ",") != strings.Join(tc.errKinds, ",") {
				t.Errorf("error kinds = %v, want %v", kinds, tc.errKinds)
			}
			// Every requirement the human report shows is in the document, same status, same order.
			for i, r := range doc.Requirements {
				label := strings.ToUpper(r.Status)
				if !regexp.MustCompile(`(?m)^` + label + ` +` + regexp.QuoteMeta(r.Name) + ` required `).MatchString(hOut) {
					t.Errorf("requirement %d %s/%s is not in the human report:\n%s", i, r.Name, r.Status, hOut)
				}
			}
		})
	}
}

func TestVerifyFormatJSONCarriesExecutedPathAndEvidence(t *testing.T) {
	useInspector(t, stubInspector{obs: map[string]core.Observation{
		"node": {Name: "node", Present: true, Version: "24.7.0", Path: "/home/u/.volta/bin/node"},
		"pnpm": {Name: "pnpm", Present: false},
	}})
	dir := t.TempDir()
	writeWomm(t, dir, twoReqs)
	_, stdout, _ := runCLI(t, "verify", dir, "--format=json")
	doc := decodeJSONOnly(t, stdout)
	node, pnpm := doc.Requirements[0], doc.Requirements[1]
	if node.Name != "node" || node.Observation.Path == nil || *node.Observation.Path != "/home/u/.volta/bin/node" || node.Observation.Version == nil || *node.Observation.Version != "24.7.0" {
		t.Errorf("node = %+v", node)
	}
	if len(node.Evidence) != 1 || node.Evidence[0].Source != "package.json" || node.Evidence[0].Field != "engines.node" || node.Evidence[0].Value != ">=22 <25" {
		t.Errorf("evidence = %+v", node.Evidence)
	}
	if pnpm.Observation.Present || pnpm.Observation.Path != nil || pnpm.Observation.Version != nil {
		t.Errorf("absent pnpm = %+v, want present=false, null version and path", pnpm.Observation)
	}
}

func TestVerifyFormatJSONNothingBeforeThePipelineMeansNoStdout(t *testing.T) {
	useInspector(t, stubInspector{obs: map[string]core.Observation{"node": {Name: "node", Present: true, Version: "24.7.0"}}})

	// Malformed womm.yaml: exit 3, stderr as in human mode, stdout empty.
	bad := t.TempDir()
	writeWomm(t, bad, "version: 1\nrequirements: [unclosed\n")
	hCode, _, hErr := runCLI(t, "verify", bad)
	jCode, jOut, jErr := runCLI(t, "verify", bad, "--format", "json")
	if jCode != 3 || hCode != 3 || jOut != "" || jErr != hErr || jErr == "" {
		t.Errorf("malformed: exit %d/%d stdout %q stderr %q vs %q", hCode, jCode, jOut, jErr, hErr)
	}

	// Missing womm.yaml.
	missing := t.TempDir()
	hCode, _, hErr = runCLI(t, "verify", missing)
	jCode, jOut, jErr = runCLI(t, "verify", missing, "--format", "json")
	if jCode != 3 || hCode != 3 || jOut != "" || jErr != hErr {
		t.Errorf("missing: exit %d/%d stdout %q stderr %q vs %q", hCode, jCode, jOut, jErr, hErr)
	}

	// Unsupported schema version.
	future := t.TempDir()
	writeWomm(t, future, "version: 99\nrequirements: []\n")
	if code, out, _ := runCLI(t, "verify", future, "--format", "json"); code != 3 || out != "" {
		t.Errorf("future schema: exit %d stdout %q", code, out)
	}

	// A refused --tool-dir is a usage error before anything runs.
	ok := t.TempDir()
	writeWomm(t, ok, twoReqs)
	if code, out, errb := runCLI(t, "verify", ok, "--format", "json", "--tool-dir", "rel"); code != 2 || out != "" || !strings.Contains(errb, "--tool-dir") {
		t.Errorf("bad tool-dir: exit %d stdout %q stderr %q", code, out, errb)
	}
}

func TestVerifyFormatInvalidValueIsAUsageError(t *testing.T) {
	useInspector(t, stubInspector{obs: map[string]core.Observation{"node": {Name: "node", Present: true, Version: "24.7.0"}}})
	dir := t.TempDir()
	writeWomm(t, dir, twoReqs)
	for _, f := range []string{"yaml", "JSON", "", "json ", "text"} {
		code, stdout, stderr := runCLI(t, "verify", dir, "--format", f)
		if code != 2 || stdout != "" || !strings.Contains(stderr, `invalid argument "`+f+`" for "--format" flag`) {
			t.Errorf("--format %q: exit %d stdout %q stderr %q", f, code, stdout, stderr)
		}
	}
	// The flag is accepted in both spellings and the default is human.
	if code, out, _ := runCLI(t, "verify", dir, "--format", "human"); code != 3 || !strings.HasPrefix(out, "PASS") && !strings.HasPrefix(out, "UNKNOWN") && !strings.HasPrefix(out, "FAIL") {
		t.Errorf("--format human: exit %d\n%s", code, out)
	}
}

func TestVerifyFormatJSONCancelledPrintsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prev := newInspectors
	newInspectors = func(*toolpath.Resolver) []inspect.Inspector { return []inspect.Inspector{cancelStub{cancel}} }
	t.Cleanup(func() { newInspectors = prev })
	dir := t.TempDir()
	writeWomm(t, dir, twoReqs)
	var out, errb bytes.Buffer
	code := run(ctx, []string{"verify", dir, "--format", "json"}, &out, &errb)
	if code != 3 || out.Len() != 0 || errb.String() != "error: verification cancelled; no result\n" {
		t.Errorf("exit %d stdout %q stderr %q; a cancelled run has no document and no verdict", code, out.String(), errb.String())
	}
}

func TestVerifyFormatJSONWarningsStayOnStderr(t *testing.T) {
	useInspector(t, stubInspector{obs: map[string]core.Observation{"node": {Name: "node", Present: true, Version: "24.7.0"}}})
	dir := t.TempDir()
	writeWomm(t, dir, "version: 1\nfoo: 1\nrequirements:\n  - name: node\n    constraint: '>=22'\n    evidence: [{source: package.json, field: engines.node, value: '>=22'}]\n")
	code, stdout, stderr := runCLI(t, "verify", dir, "--format", "json")
	if code != 0 || !strings.Contains(stderr, `warning: unknown key "foo"`) {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	decodeJSONOnly(t, stdout) // stdout is still exactly one document
}

func TestVerifyFormatJSONIsDeterministicAcrossRuns(t *testing.T) {
	useInspector(t, stubInspector{obs: map[string]core.Observation{
		"node": {Name: "node", Present: true, Version: "24.7.0", Path: "/usr/bin/node"},
		"pnpm": {Name: "pnpm", Present: true, Version: "9.0.0", Path: "/usr/bin/pnpm"},
	}})
	dir := t.TempDir()
	writeWomm(t, dir, twoReqs)
	_, first, _ := runCLI(t, "verify", dir, "--format", "json")
	for i := 0; i < 10; i++ {
		if _, again, _ := runCLI(t, "verify", dir, "--format", "json"); again != first {
			t.Fatalf("run %d differs:\n%s\nvs\n%s", i, again, first)
		}
	}
}

// With the real inspector end to end: the path in the document is the
// binary that actually answered.
func TestVerifyFormatJSONWithRealInspector(t *testing.T) {
	project, sys, user := realInspectorProject(t), realDir(t), realDir(t)
	useSystemDirs(t, sys)
	node := fakeTool(t, user, "node", "echo v24.7.0\n")
	code, stdout, stderr := runCLI(t, "verify", project, "--tool-dir", user, "--format", "json")
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	doc := decodeJSONOnly(t, stdout)
	if len(doc.Requirements) != 1 || doc.Requirements[0].Observation.Path == nil || *doc.Requirements[0].Observation.Path != node || doc.Result != "pass" {
		t.Errorf("document = %+v, want one pass at %s", doc, node)
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

// If the report cannot be written the command must not exit 0 — not even
// when the verdict would have been a FAIL (1) or a PASS (0).
func TestVerifyWriteFailureIsNeverExitZero(t *testing.T) {
	useInspector(t, stubInspector{obs: map[string]core.Observation{
		"node": {Name: "node", Present: true, Version: "24.7.0"}, "pnpm": {Name: "pnpm", Present: true, Version: "10.15.1"}}})
	dir := t.TempDir()
	writeWomm(t, dir, twoReqs)
	for _, args := range [][]string{{"verify", dir}, {"verify", dir, "--format", "json"}} {
		var errb bytes.Buffer
		code := run(context.Background(), args, brokenWriter{}, &errb)
		if code != 3 || !strings.Contains(errb.String(), "broken pipe") {
			t.Errorf("%v: exit %d stderr %q; want exit 3 and the write error", args, code, errb.String())
		}
	}
}

// Several operational errors: count, order and text are exactly what
// stderr says, one `error:` line each.
func TestVerifyFormatJSONErrorsAreOrderedAndMatchStderrExactly(t *testing.T) {
	useInspector(t, stubInspector{obs: map[string]core.Observation{}})
	dir := t.TempDir()
	writeWomm(t, dir, "version: 1\nservices:\n  zzz: {version: '1'}\n  bbb: {version: '7'}\nrequirements:\n"+
		"  - {name: aaa, constraint: present, evidence: [{source: a, field: b, value: c}]}\n")
	hCode, _, hErr := runCLI(t, "verify", dir)
	jCode, jOut, jErr := runCLI(t, "verify", dir, "--format", "json")
	doc := decodeJSONOnly(t, jOut)
	if hCode != 3 || jCode != 3 || hErr != jErr || len(doc.Errors) != 3 {
		t.Fatalf("exit %d/%d, %d errors\nstderr %q", hCode, jCode, len(doc.Errors), jErr)
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSuffix(jErr, "\n"), "\n") {
		lines = append(lines, strings.TrimPrefix(l, "error: "))
	}
	for i, e := range doc.Errors {
		if e.Message == "" || e.Message != lines[i] || e.Kind != "unsupported_requirement" {
			t.Errorf("errors[%d] = %+v, want kind unsupported_requirement and message %q", i, e, lines[i])
		}
	}
	// Documented order: services by name, environment, then requirements by name.
	if !strings.Contains(doc.Errors[0].Message, `"bbb"`) || !strings.Contains(doc.Errors[1].Message, `"zzz"`) || !strings.Contains(doc.Errors[2].Message, `"aaa"`) {
		t.Errorf("order = %q", []string{doc.Errors[0].Message, doc.Errors[1].Message, doc.Errors[2].Message})
	}
}

// A bad --format is a usage error before anything else is looked at: a
// missing or malformed womm.yaml must not take precedence.
func TestVerifyBadFormatBeatsMissingOrMalformedFile(t *testing.T) {
	missing, bad := t.TempDir(), t.TempDir()
	writeWomm(t, bad, "version: 1\nrequirements: [unclosed\n")
	for _, dir := range []string{missing, bad} {
		if code, out, errb := runCLI(t, "verify", dir, "--format", "bogus"); code != 2 || out != "" || !strings.Contains(errb, `"--format"`) {
			t.Errorf("exit %d stdout %q stderr %q; want a usage error", code, out, errb)
		}
	}
}
