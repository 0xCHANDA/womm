package report

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/0xCHANDA/womm/internal/core"
)

func sampleMatches() []core.Match {
	return []core.Match{
		{
			Requirement: core.Requirement{Name: "pnpm", Constraint: "10.15.1", Evidence: []core.Evidence{{Source: "package.json", Field: "packageManager", Value: "pnpm@10.15.1"}}},
			Observation: core.Observation{Name: "pnpm", Present: false},
			Status:      core.StatusFail,
			Reason:      "required target is absent",
		},
		{
			Requirement: core.Requirement{Name: "node", Constraint: ">=22 <25", Evidence: []core.Evidence{
				{Source: "package.json", Field: "engines.node", Value: ">=22 <25"},
				{Source: ".nvmrc", Field: "version", Value: "v24.7.0"},
			}},
			Observation: core.Observation{Name: "node", Present: true, Version: "24.7.0"},
			Status:      core.StatusPass,
			Reason:      "observed version satisfies the requirement",
		},
		{
			Requirement: core.Requirement{Name: "yarn", Constraint: "present", Evidence: []core.Evidence{{Source: "package.json", Field: "packageManager", Value: "yarn@4.0.0"}}},
			Observation: core.Observation{Name: "yarn", Present: true},
			Status:      core.StatusUnreachable,
			Reason:      "target is present but could not be queried",
		},
		{
			Requirement: core.Requirement{Name: "npm", Constraint: ">=10", Evidence: []core.Evidence{{Source: "package.json", Field: "engines.npm", Value: ">=10"}}},
			Observation: core.Observation{Name: "npm", Present: true, Version: ""},
			Status:      core.StatusUnknown,
			Reason:      "target is present but its version is unknown",
		},
	}
}

const wantSample = `PASS        node required >=22 <25; observed 24.7.0
            observed version satisfies the requirement
            evidence: package.json → engines.node = ">=22 <25"
            evidence: .nvmrc → version = "v24.7.0"
UNKNOWN     npm  required >=10; observed present, version unknown
            target is present but its version is unknown
            evidence: package.json → engines.npm = ">=10"
FAIL        pnpm required 10.15.1; observed absent
            required target is absent
            evidence: package.json → packageManager = "pnpm@10.15.1"
UNREACHABLE yarn required present; observed present, version unknown
            target is present but could not be queried
            evidence: package.json → packageManager = "yarn@4.0.0"

4 requirements: 1 pass, 1 fail, 1 unknown, 1 unreachable
`

func TestRenderGolden(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, sampleMatches()); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != wantSample {
		t.Errorf("Render output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, wantSample)
	}
}

func TestRenderIsDeterministicAndDoesNotMutate(t *testing.T) {
	in := sampleMatches()
	before := make([]core.Match, len(in))
	copy(before, in)

	var a, b bytes.Buffer
	if err := Render(&a, in); err != nil {
		t.Fatal(err)
	}
	if err := Render(&b, in); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Errorf("two renders of equal input differ:\n%s\n---\n%s", a.String(), b.String())
	}
	if !reflect.DeepEqual(in, before) {
		t.Errorf("Render reordered or mutated its input: %#v", in)
	}
}

func TestRenderEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "0 requirements: 0 pass, 0 fail, 0 unknown, 0 unreachable\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderSingular(t *testing.T) {
	var buf bytes.Buffer
	m := sampleMatches()[1]
	m.Requirement.Evidence = nil
	m.Reason = ""
	if err := Render(&buf, []core.Match{m}); err != nil {
		t.Fatal(err)
	}
	want := "PASS        node required >=22 <25; observed 24.7.0\n\n1 requirement: 1 pass, 0 fail, 0 unknown, 0 unreachable\n"
	if got := buf.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderRefusesUnknownStatusBeforeWriting(t *testing.T) {
	matches := sampleMatches()
	matches[2].Status = core.MatchStatus("maybe")

	var buf bytes.Buffer
	err := Render(&buf, matches)
	if !errors.Is(err, ErrUnknownStatus) {
		t.Fatalf("error = %v, want ErrUnknownStatus", err)
	}
	if buf.Len() != 0 {
		t.Errorf("Render wrote %d bytes before failing: %q", buf.Len(), buf.String())
	}
	if !strings.Contains(err.Error(), `"maybe"`) || !strings.Contains(err.Error(), `"yarn"`) {
		t.Errorf("error should name the status and requirement: %v", err)
	}
}

func TestRenderEmptyStatusIsUnknownStatus(t *testing.T) {
	var buf bytes.Buffer
	err := Render(&buf, []core.Match{{Requirement: core.Requirement{Name: "node"}}})
	if !errors.Is(err, ErrUnknownStatus) {
		t.Fatalf("error = %v, want ErrUnknownStatus (a zero status must not render as anything)", err)
	}
}

type failingWriter struct{ err error }

func (f failingWriter) Write([]byte) (int, error) { return 0, f.err }

func TestRenderPropagatesWriteErrors(t *testing.T) {
	sentinel := errors.New("disk full")
	err := Render(failingWriter{sentinel}, sampleMatches())
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want the writer's error", err)
	}
}

func TestDescribeObservation(t *testing.T) {
	cases := []struct {
		obs  core.Observation
		want string
	}{
		{core.Observation{Present: false}, "absent"},
		{core.Observation{Present: true}, "present, version unknown"},
		{core.Observation{Present: true, Version: "24.7.0"}, "24.7.0"},
		{core.Observation{Present: false, Version: "24.7.0"}, "absent (contradictory: version 24.7.0 reported)"},
	}
	for _, c := range cases {
		if got := describeObservation(c.obs); got != c.want {
			t.Errorf("describeObservation(%#v) = %q, want %q", c.obs, got, c.want)
		}
	}
}

func TestSortIsStableAndCopies(t *testing.T) {
	in := []core.Match{
		{Requirement: core.Requirement{Name: "node", Constraint: ">=22"}, Observation: core.Observation{Name: "node"}, Status: core.StatusPass, Reason: "first"},
		{Requirement: core.Requirement{Name: "node", Constraint: ">=22"}, Observation: core.Observation{Name: "node"}, Status: core.StatusFail, Reason: "second"},
		{Requirement: core.Requirement{Name: "node", Constraint: "24.7.0"}, Observation: core.Observation{Name: "node"}, Status: core.StatusPass, Reason: "exact"},
		{Requirement: core.Requirement{Name: "a", Constraint: "present"}, Observation: core.Observation{Name: "a"}, Status: core.StatusPass, Reason: "a"},
	}
	got := Sort(in)
	wantReasons := []string{"a", "exact", "first", "second"}
	for i, r := range wantReasons {
		if got[i].Reason != r {
			t.Fatalf("order[%d] = %q, want %q (full: %+v)", i, got[i].Reason, r, got)
		}
	}
	if in[0].Reason != "first" || in[3].Reason != "a" {
		t.Errorf("Sort mutated its input: %+v", in)
	}
	got[0].Reason = "changed"
	if in[3].Reason == "changed" {
		t.Error("Sort returned a slice aliasing the input")
	}
}

func TestSummarize(t *testing.T) {
	s, err := Summarize(sampleMatches())
	if err != nil {
		t.Fatal(err)
	}
	if want := (Summary{Pass: 1, Fail: 1, Unknown: 1, Unreachable: 1}); s != want {
		t.Errorf("Summarize = %+v, want %+v", s, want)
	}
	if s.Total() != 4 {
		t.Errorf("Total = %d, want 4", s.Total())
	}
	if _, err := Summarize([]core.Match{{Status: "nope"}}); !errors.Is(err, ErrUnknownStatus) {
		t.Errorf("error = %v, want ErrUnknownStatus", err)
	}
}
