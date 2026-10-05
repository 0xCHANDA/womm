package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/0xCHANDA/womm/internal/core"
)

var updateGolden = flag.Bool("update", false, "rewrite the JSON golden files")

func ev(source, field, value string) core.Evidence {
	return core.Evidence{Source: source, Field: field, Value: value}
}

func match(name, constraint string, obs core.Observation, status core.MatchStatus, reason string, evidence ...core.Evidence) core.Match {
	obs.Name = name
	return core.Match{Requirement: core.Requirement{Name: name, Constraint: constraint, Evidence: evidence}, Observation: obs, Status: status, Reason: reason}
}

// jsonScenarios are the documents pinned byte for byte under
// testdata/json. Regenerate with `go test ./internal/report -run
// TestRenderJSONGolden -update` and READ the diff: it is the public
// contract.
func jsonScenarios() map[string]JSONDocument {
	pass := match("node", ">=22 <25", core.Observation{Present: true, Version: "24.7.0", Path: "/home/u/.volta/bin/node"}, core.StatusPass,
		"observed version satisfies the requirement", ev("package.json", "engines.node", ">=22 <25"), ev(".nvmrc", "version", "v24.7.0"))
	fail := match("pnpm", "10.15.1", core.Observation{}, core.StatusFail, "required target is absent", ev("package.json", "packageManager", "pnpm@10.15.1"))
	unknown := match("npm", ">=10", core.Observation{Present: true, Path: "/usr/bin/npm"}, core.StatusUnknown, "target is present but its version is unknown", ev("package.json", "engines.npm", ">=10"))
	unreachable := match("yarn", "present", core.Observation{Present: true, Path: "/usr/local/bin/yarn"}, core.StatusUnreachable, "target is present but could not be queried: inspecting yarn: version probe timed out after 5s", ev("package.json", "packageManager", "yarn@4.0.0"))
	return map[string]JSONDocument{
		"pass":        {Result: "pass", ExitCode: 0, Matches: []core.Match{pass}},
		"fail":        {Result: "fail", ExitCode: 1, Matches: []core.Match{fail}},
		"unknown":     {Result: "inconclusive", ExitCode: 3, Matches: []core.Match{unknown}},
		"unreachable": {Result: "inconclusive", ExitCode: 3, Matches: []core.Match{unreachable}},
		"mixed": {Result: "inconclusive", ExitCode: 3, Matches: []core.Match{fail, pass, unreachable, unknown}, Diagnostics: []Diagnostic{
			{Kind: "undecidable", Message: "npm: prerelease observed against a version range: \"11.0.0-rc.1\" against \">=10\""},
		}},
		"empty":       {Result: "pass", ExitCode: 0},
		"errors-only": {Result: "inconclusive", ExitCode: 3, Diagnostics: []Diagnostic{{Kind: "unsupported_requirement", Message: "service \"postgres\": no inspector supports requirement (services are not verified by WOMM v0.1)"}}},
		"contradictory": {Result: "inconclusive", ExitCode: 3, Matches: []core.Match{
			match("node", ">=22", core.Observation{Present: false, Version: "24.7.0"}, core.StatusUnknown, "observation is contradictory: the target is reported absent but with a version", ev("package.json", "engines.node", ">=22")),
		}},
		"escaping": {Result: "inconclusive", ExitCode: 3, Matches: []core.Match{
			match("nöde\"x\\", "<=1 && >2 <3", core.Observation{Present: true, Version: "1.0.0", Path: "/home/ü/𝒳/node\nPASS forged"}, core.StatusUnknown,
				"tab\there\u2028sep\u0000nul\xff\xfeinvalid-utf8 ✓", ev("pa\"ckage.json", "a\\b", "line1\nline2\r\n\x1b[31mred\x1b[0m")),
		}},
	}
}

func renderJSON(t *testing.T, doc JSONDocument) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := RenderJSON(&buf, doc); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestRenderJSONGolden(t *testing.T) {
	for name, doc := range jsonScenarios() {
		t.Run(name, func(t *testing.T) {
			got := renderJSON(t, doc)
			path := filepath.Join("testdata", "json", name+".golden.json")
			if *updateGolden {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("JSON mismatch for %s\n--- got ---\n%s--- want ---\n%s", name, got, want)
			}
			if !json.Valid(got) {
				t.Error("output is not valid JSON")
			}
		})
	}
}

// keysInOrder returns the object keys of the first top-level object
// found by walking the raw token stream, in document order.
func keysInOrder(t *testing.T, raw []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	var keys []string
	depth := 0
	expectKey := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch v := tok.(type) {
		case json.Delim:
			switch v {
			case '{':
				depth++
				expectKey = depth == 1
			case '}', ']':
				depth--
				expectKey = depth == 1
			case '[':
				depth++
				expectKey = false
			}
		case string:
			if depth == 1 && expectKey {
				keys = append(keys, v)
				expectKey = false
			} else if depth == 1 {
				expectKey = true
			}
		default:
			if depth == 1 {
				expectKey = true
			}
		}
	}
	return keys
}

// TestRenderJSONFieldSetIsStable pins the schema-version-1 field names
// and their order at every level. Adding a field is allowed by the
// schema policy but must be a conscious edit of this table (and of
// docs/output-json.md); removing or renaming one needs a new version.
func TestRenderJSONFieldSetIsStable(t *testing.T) {
	raw := renderJSON(t, jsonScenarios()["mixed"])
	if got, want := keysInOrder(t, raw), []string{"schemaVersion", "result", "exitCode", "requirements", "errors", "summary"}; !reflect.DeepEqual(got, want) {
		t.Errorf("top-level fields = %v, want %v", got, want)
	}

	var doc struct {
		SchemaVersion int `json:"schemaVersion"`
		Requirements  []map[string]json.RawMessage
		Errors        []map[string]json.RawMessage
		Summary       map[string]json.RawMessage
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != JSONSchemaVersion || JSONSchemaVersion != 1 {
		t.Errorf("schemaVersion = %d (const %d), want 1", doc.SchemaVersion, JSONSchemaVersion)
	}
	sorted := func(m map[string]json.RawMessage) []string {
		var k []string
		for key := range m {
			k = append(k, key)
		}
		sort.Strings(k)
		return k
	}
	for _, r := range doc.Requirements {
		if got, want := sorted(r), []string{"constraint", "evidence", "name", "observation", "reason", "status"}; !reflect.DeepEqual(got, want) {
			t.Errorf("requirement fields = %v, want %v", got, want)
		}
		var obs map[string]json.RawMessage
		if err := json.Unmarshal(r["observation"], &obs); err != nil {
			t.Fatal(err)
		}
		if got, want := sorted(obs), []string{"path", "present", "version"}; !reflect.DeepEqual(got, want) {
			t.Errorf("observation fields = %v, want %v", got, want)
		}
		var evs []map[string]json.RawMessage
		if err := json.Unmarshal(r["evidence"], &evs); err != nil {
			t.Fatal(err)
		}
		for _, e := range evs {
			if got, want := sorted(e), []string{"field", "source", "value"}; !reflect.DeepEqual(got, want) {
				t.Errorf("evidence fields = %v, want %v", got, want)
			}
		}
	}
	for _, e := range doc.Errors {
		if got, want := sorted(e), []string{"kind", "message"}; !reflect.DeepEqual(got, want) {
			t.Errorf("error fields = %v, want %v", got, want)
		}
	}
	if got, want := sorted(doc.Summary), []string{"fail", "pass", "total", "unknown", "unreachable"}; !reflect.DeepEqual(got, want) {
		t.Errorf("summary fields = %v, want %v", got, want)
	}
}

func TestRenderJSONNullsAndEmptyListsNeverNilOrOmitted(t *testing.T) {
	raw := string(renderJSON(t, JSONDocument{Result: "pass", ExitCode: 0}))
	for _, want := range []string{`"requirements": []`, `"errors": []`} {
		if !strings.Contains(raw, want) {
			t.Errorf("empty document lacks %s:\n%s", want, raw)
		}
	}
	// A requirement without evidence, without version, without path.
	raw = string(renderJSON(t, JSONDocument{Result: "fail", ExitCode: 1, Matches: []core.Match{
		match("node", "present", core.Observation{}, core.StatusFail, "required target is absent"),
	}}))
	for _, want := range []string{`"evidence": []`, `"version": null`, `"path": null`, `"present": false`} {
		if !strings.Contains(raw, want) {
			t.Errorf("document lacks %s:\n%s", want, raw)
		}
	}
	if strings.Contains(raw, "null,\n      \"evidence\": null") || strings.Contains(raw, `"evidence": null`) {
		t.Errorf("evidence must be [] never null:\n%s", raw)
	}
}

func TestRenderJSONIsDeterministicAndOrderIndependent(t *testing.T) {
	doc := jsonScenarios()["mixed"]
	first := renderJSON(t, doc)
	for i := 0; i < 50; i++ {
		if again := renderJSON(t, doc); !bytes.Equal(again, first) {
			t.Fatalf("run %d differs", i)
		}
	}
	// Every permutation of the matches yields the same document: the
	// order is a property of the report, not of the input.
	var perm func(a []core.Match, k int)
	perm = func(a []core.Match, k int) {
		if k == len(a) {
			d := doc
			d.Matches = append([]core.Match(nil), a...)
			if got := renderJSON(t, d); !bytes.Equal(got, first) {
				t.Fatalf("permutation %v changed the document", names(a))
			}
			return
		}
		for i := k; i < len(a); i++ {
			a[k], a[i] = a[i], a[k]
			perm(a, k+1)
			a[k], a[i] = a[i], a[k]
		}
	}
	perm(append([]core.Match(nil), doc.Matches...), 0)

	// The input is not reordered or mutated.
	before := append([]core.Match(nil), doc.Matches...)
	renderJSON(t, doc)
	if !reflect.DeepEqual(before, doc.Matches) {
		t.Error("RenderJSON mutated its input")
	}
}

func names(ms []core.Match) []string {
	var n []string
	for _, m := range ms {
		n = append(n, m.Requirement.Name)
	}
	return n
}

func TestRenderJSONSummaryAndStatusValues(t *testing.T) {
	var d struct {
		Requirements []struct{ Name, Status string }
		Summary      struct{ Total, Pass, Fail, Unknown, Unreachable int }
	}
	if err := json.Unmarshal(renderJSON(t, jsonScenarios()["mixed"]), &d); err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, r := range d.Requirements {
		statuses[r.Name] = r.Status
	}
	want := map[string]string{"node": "pass", "pnpm": "fail", "yarn": "unreachable", "npm": "unknown"}
	if !reflect.DeepEqual(statuses, want) {
		t.Errorf("statuses = %v, want %v", statuses, want)
	}
	if s := d.Summary; s.Total != 4 || s.Pass != 1 || s.Fail != 1 || s.Unknown != 1 || s.Unreachable != 1 {
		t.Errorf("summary = %+v", s)
	}
	// Presentation order is the human report's: by requirement name.
	if got, want := []string{d.Requirements[0].Name, d.Requirements[1].Name, d.Requirements[2].Name, d.Requirements[3].Name}, []string{"node", "npm", "pnpm", "yarn"}; !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// Valid UTF-8 survives byte for byte; control characters and invalid
// bytes are the only strings that change, and only in documented ways.
func TestRenderJSONStringsRoundTrip(t *testing.T) {
	values := []string{"plain", "ünïcödé ✓ 𝒳", `quote " backslash \ slash /`, ">=22 <25 && <=1", "tab\there", "line\nbreak", "\u2028\u2029", "\x1b[31m"}
	for _, v := range values {
		m := match(v, v, core.Observation{Present: true, Version: "1.0.0", Path: "/p/" + v}, core.StatusPass, v, ev(v, v, v))
		var d struct {
			Requirements []struct {
				Name, Constraint, Reason string
				Observation              struct{ Path string }
				Evidence                 []struct{ Source, Field, Value string }
			}
		}
		if err := json.Unmarshal(renderJSON(t, JSONDocument{Result: "pass", Matches: []core.Match{m}}), &d); err != nil {
			t.Fatalf("%q: %v", v, err)
		}
		r := d.Requirements[0]
		if r.Name != v || r.Constraint != v || r.Reason != v || r.Observation.Path != "/p/"+v || r.Evidence[0].Source != v || r.Evidence[0].Field != v || r.Evidence[0].Value != v {
			t.Errorf("%q did not round-trip: %+v", v, r)
		}
	}

	// Invalid UTF-8 becomes U+FFFD (documented, deterministic).
	m := match("n\xffe", ">=1", core.Observation{Present: true}, core.StatusPass, "ok")
	var d struct{ Requirements []struct{ Name string } }
	if err := json.Unmarshal(renderJSON(t, JSONDocument{Result: "pass", Matches: []core.Match{m}}), &d); err != nil {
		t.Fatal(err)
	}
	if d.Requirements[0].Name != "n\uFFFDe" {
		t.Errorf("invalid UTF-8 became %q, want U+FFFD", d.Requirements[0].Name)
	}
}

func TestRenderJSONDoesNotEscapeHTMLButEscapesControlCharacters(t *testing.T) {
	raw := string(renderJSON(t, JSONDocument{Result: "pass", Matches: []core.Match{
		match("node", ">=22 <25 & >1", core.Observation{Present: true, Version: "1.0.0"}, core.StatusPass, "esc\x1b[0m"),
	}}))
	if !strings.Contains(raw, `">=22 <25 & >1"`) {
		t.Errorf("constraint was HTML-escaped:\n%s", raw)
	}
	if strings.ContainsRune(raw, 0x1b) || !strings.Contains(raw, `\u001b`) {
		t.Errorf("a raw control character reached the output:\n%q", raw)
	}
	// A path with a newline cannot add a line to the document's own structure.
	raw = string(renderJSON(t, jsonScenarios()["escaping"]))
	if strings.Contains(raw, "PASS forged\n") || strings.Contains(raw, "\nPASS") {
		t.Errorf("a newline in a value escaped its string:\n%s", raw)
	}
}

func TestRenderJSONRefusesUnknownStatusBeforeWriting(t *testing.T) {
	var buf bytes.Buffer
	err := RenderJSON(&buf, JSONDocument{Result: "pass", Matches: []core.Match{
		match("node", "present", core.Observation{Present: true}, core.StatusPass, "ok"),
		{Requirement: core.Requirement{Name: "x"}, Status: "weird"},
	}})
	if err == nil || buf.Len() != 0 {
		t.Fatalf("err %v, wrote %d bytes; an unknown status must fail before the first byte", err, buf.Len())
	}
}

func TestRenderJSONEndsWithOneNewlineAndIsIndented(t *testing.T) {
	raw := renderJSON(t, jsonScenarios()["pass"])
	if !bytes.HasSuffix(raw, []byte("}\n")) || bytes.HasSuffix(raw, []byte("\n\n")) {
		t.Errorf("document must end with exactly one newline: %q", raw[len(raw)-4:])
	}
	if !utf8.Valid(raw) || !strings.HasPrefix(string(raw), "{\n  \"schemaVersion\": 1,\n") {
		t.Errorf("unexpected framing:\n%s", raw)
	}
}

// Nothing about the machine or the moment is in the document beyond
// what the caller put there on purpose (the executed path). Sentinels
// are planted in the ambient places a careless renderer might read.
func TestRenderJSONContainsNoAmbientData(t *testing.T) {
	t.Setenv("HOME", "/womm-ambient-home-7f3a")
	t.Setenv("USER", "womm-ambient-user-7f3a")
	t.Setenv("HOSTNAME", "womm-ambient-host-7f3a")
	t.Setenv("PATH", "/womm-ambient-path-7f3a")
	raw := strings.ToLower(string(renderJSON(t, jsonScenarios()["mixed"])))
	banned := []string{"womm-ambient", "timestamp", "generatedat", "hostname", "uuid", "machine", "fingerprint"}
	if host, err := os.Hostname(); err == nil && len(host) >= 8 {
		banned = append(banned, strings.ToLower(host))
	}
	for _, b := range banned {
		if strings.Contains(raw, b) {
			t.Errorf("document contains %q", b)
		}
	}
}

func FuzzRenderJSON(f *testing.F) {
	f.Add("node", ">=22", "24.7.0", "/usr/bin/node", "ok", "package.json", "engines.node", ">=22", uint8(0))
	f.Add("n\xffe", "<&>", "", "", "\u2028", "\x00", "a\"b", "\\", uint8(3))
	f.Fuzz(func(t *testing.T, name, constraint, version, path, reason, src, field, value string, st uint8) {
		status := []core.MatchStatus{core.StatusPass, core.StatusFail, core.StatusUnknown, core.StatusUnreachable}[st%4]
		doc := JSONDocument{
			Result: "inconclusive", ExitCode: 3,
			Matches:     []core.Match{match(name, constraint, core.Observation{Present: version != "", Version: version, Path: path}, status, reason, ev(src, field, value))},
			Diagnostics: []Diagnostic{{Kind: "other", Message: reason}},
		}
		var a, b bytes.Buffer
		if err := RenderJSON(&a, doc); err != nil {
			t.Fatal(err)
		}
		if err := RenderJSON(&b, doc); err != nil || !bytes.Equal(a.Bytes(), b.Bytes()) {
			t.Fatalf("not deterministic: %v", err)
		}
		if !json.Valid(a.Bytes()) || !utf8.Valid(a.Bytes()) {
			t.Fatalf("invalid output for %q: %q", name, a.Bytes())
		}
		var d struct {
			Requirements []struct{ Name, Constraint string }
		}
		if err := json.Unmarshal(a.Bytes(), &d); err != nil || len(d.Requirements) != 1 {
			t.Fatalf("decode: %v", err)
		}
		if utf8.ValidString(name) && d.Requirements[0].Name != name {
			t.Fatalf("name %q did not round-trip: %q", name, d.Requirements[0].Name)
		}
		if strings.ContainsRune(a.String(), '\r') {
			t.Fatalf("raw CR in output: %q", a.String())
		}
	})
}

// Characters encoding/json leaves raw but a terminal acts on: C1 (CSI),
// DEL, bidi overrides, zero-width and tag characters. They are escaped in
// the document and still round-trip exactly.
func TestRenderJSONEscapesTerminalActiveCharacters(t *testing.T) {
	values := []string{"c1\u009bcsi", "nel\u0085", "del\x7f", "bidi\u202eevil", "zw\u200bx", "bom\ufeffx", "tag\U000E0001x", "lrm\u200e"}
	for _, v := range values {
		raw := renderJSON(t, JSONDocument{Result: "pass", Matches: []core.Match{
			match("node", ">=1", core.Observation{Present: true, Version: "1.0.0", Path: "/p/" + v}, core.StatusPass, v, ev(v, v, v)),
		}})
		for _, r := range string(raw) {
			if r == 0x7f || (r >= 0x80 && r <= 0x9f) || unicode.Is(unicode.Cf, r) {
				t.Errorf("%q: raw U+%04X reached the output:\n%s", v, r, raw)
			}
		}
		var d struct {
			Requirements []struct {
				Reason      string
				Observation struct{ Path string }
			}
		}
		if err := json.Unmarshal(raw, &d); err != nil {
			t.Fatalf("%q: %v", v, err)
		}
		if d.Requirements[0].Reason != v || d.Requirements[0].Observation.Path != "/p/"+v {
			t.Errorf("%q did not round-trip: %+v", v, d.Requirements[0])
		}
	}
}

// One U+FFFD per invalid byte: two different byte strings must not
// collapse into the same text.
func TestRenderJSONReplacesEachInvalidByte(t *testing.T) {
	for in, want := range map[string]string{"a\xffb": "a\ufffdb", "a\xff\xfe\xfdb": "a\ufffd\ufffd\ufffdb", "\xc3": "\ufffd", "ok": "ok"} {
		if got := text(in); got != want {
			t.Errorf("text(%q) = %q, want %q", in, got, want)
		}
	}
	a := string(renderJSON(t, JSONDocument{Result: "pass", Matches: []core.Match{match("n", "x\xff\xfe", core.Observation{}, core.StatusFail, "r")}}))
	b := string(renderJSON(t, JSONDocument{Result: "pass", Matches: []core.Match{match("n", "x\xff", core.Observation{}, core.StatusFail, "r")}}))
	if a == b {
		t.Error("different invalid byte runs rendered identically")
	}
}

// A failure to write the document is an error, never swallowed (the CLI
// turns it into a non-zero exit even for a FAIL verdict).
func TestRenderJSONPropagatesWriteErrors(t *testing.T) {
	sentinel := errors.New("write failed")
	if err := RenderJSON(failingWriter{sentinel}, jsonScenarios()["pass"]); !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want the writer's error", err)
	}
}

func TestRenderJSONUnknownStatusIsErrUnknownStatus(t *testing.T) {
	err := RenderJSON(&bytes.Buffer{}, JSONDocument{Matches: []core.Match{{Requirement: core.Requirement{Name: "x"}, Status: "weird"}}})
	if !errors.Is(err, ErrUnknownStatus) {
		t.Fatalf("error = %v, want ErrUnknownStatus", err)
	}
}

// The UTF-8 repair applies to EVERY string field, and an absent tool
// that nevertheless has a path keeps it (host evidence is never hidden).
func TestRenderJSONRepairsEveryStringField(t *testing.T) {
	bad := "x\xffy"
	m := match(bad, bad, core.Observation{Present: false, Version: bad, Path: bad}, core.StatusUnknown, bad, ev(bad, bad, bad))
	raw := renderJSON(t, JSONDocument{Result: "inconclusive", ExitCode: 3, Matches: []core.Match{m}, Diagnostics: []Diagnostic{{Kind: bad, Message: bad}}})
	var d struct {
		Requirements []struct {
			Name, Constraint, Reason string
			Observation              struct{ Version, Path *string }
			Evidence                 []struct{ Source, Field, Value string }
		}
		Errors []struct{ Kind, Message string }
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	want := "x�y"
	r := d.Requirements[0]
	got := []string{r.Name, r.Constraint, r.Reason, *r.Observation.Version, *r.Observation.Path, r.Evidence[0].Source, r.Evidence[0].Field, r.Evidence[0].Value, d.Errors[0].Kind, d.Errors[0].Message}
	for i, g := range got {
		if g != want {
			t.Errorf("field %d = %q, want %q", i, g, want)
		}
	}
	if strings.Contains(string(raw), "\\ufffd") || !strings.Contains(string(raw), "x\ufffdy") {
		t.Errorf("replacement should be written as the character itself, not an escape:\n%s", raw)
	}
}
