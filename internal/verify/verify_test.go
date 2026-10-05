package verify

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/0xCHANDA/womm/internal/compare"
	"github.com/0xCHANDA/womm/internal/core"
	"github.com/0xCHANDA/womm/internal/inspect"
	"github.com/0xCHANDA/womm/internal/schema"
)

// outcome is what a fake inspector answers for one tool name.
type outcome struct {
	obs core.Observation
	err error
}

// fakeInspector supports exactly the names in answers. It records
// which names it was asked about, so tests can prove unsupported
// requirements never reach an inspector.
type fakeInspector struct {
	answers map[string]outcome
	asked   []string
}

func (f *fakeInspector) Supports(name string) bool { _, ok := f.answers[name]; return ok }
func (f *fakeInspector) Inspect(_ context.Context, req core.Requirement) (core.Observation, error) {
	f.asked = append(f.asked, req.Name)
	o := f.answers[req.Name]
	return o.obs, o.err
}

var errProbe = errors.New("version probe timed out after 5s")

func req(name, constraint string) core.Requirement {
	return core.Requirement{Name: name, Constraint: constraint, Evidence: []core.Evidence{{Source: "womm.yaml", Field: "requirements", Value: constraint}}}
}

func file(reqs ...core.Requirement) *schema.File {
	return &schema.File{Version: schema.Version, Requirements: reqs}
}

func statuses(ms []core.Match) []core.MatchStatus {
	out := make([]core.MatchStatus, len(ms))
	for i, m := range ms {
		out[i] = m.Status
	}
	return out
}

func TestVerifyMapsEveryOutcome(t *testing.T) {
	insp := &fakeInspector{answers: map[string]outcome{
		"node": {obs: core.Observation{Name: "node", Present: true, Version: "24.7.0"}},        // PASS
		"npm":  {obs: core.Observation{Name: "npm", Present: true, Version: "9.0.0"}},          // FAIL (range)
		"pnpm": {obs: core.Observation{Name: "pnpm", Present: false}},                          // FAIL (missing binary)
		"yarn": {obs: core.Observation{Name: "yarn", Present: true}},                           // UNKNOWN (version unknown)
		"bun":  {obs: core.Observation{Name: "bun", Present: true}, err: errProbe},             // UNREACHABLE
		"deno": {obs: core.Observation{Name: "deno", Present: true, Version: "not-a-version"}}, // UNKNOWN + compare error
		"php":  {obs: core.Observation{Name: "php", Present: true, Version: "24.7.0-beta.1"}},  // UNKNOWN + prerelease
	}}
	f := file(
		req("yarn", "present"),
		req("node", ">=22 <25"),
		req("npm", ">=10"),
		req("pnpm", "10.15.1"),
		req("bun", ">=1"),
		req("deno", ">=2"),
		req("php", ">=22"),
	)
	// "present" on yarn: present → PASS; make it version-dependent instead.
	f.Requirements[0] = req("yarn", ">=4")

	res := Verify(context.Background(), f, []inspect.Inspector{insp})

	wantOrder := []string{"bun", "deno", "node", "npm", "php", "pnpm", "yarn"}
	var gotOrder []string
	for _, m := range res.Matches {
		gotOrder = append(gotOrder, m.Requirement.Name)
	}
	if !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Fatalf("match order = %v, want %v (requirement-name order)", gotOrder, wantOrder)
	}
	want := []core.MatchStatus{
		core.StatusUnreachable, // bun
		core.StatusUnknown,     // deno
		core.StatusPass,        // node
		core.StatusFail,        // npm
		core.StatusUnknown,     // php
		core.StatusFail,        // pnpm
		core.StatusUnknown,     // yarn
	}
	if got := statuses(res.Matches); !reflect.DeepEqual(got, want) {
		t.Fatalf("statuses = %v, want %v", got, want)
	}

	bun := res.Matches[0]
	if bun.Observation != (core.Observation{Name: "bun", Present: true}) {
		t.Errorf("unreachable match must carry the partial observation, got %#v", bun.Observation)
	}
	if !strings.Contains(bun.Reason, "could not be queried") || !strings.Contains(bun.Reason, errProbe.Error()) {
		t.Errorf("unreachable reason = %q", bun.Reason)
	}
	if !reflect.DeepEqual(bun.Requirement, f.Requirements[4]) {
		t.Errorf("unreachable match lost its requirement/evidence: %#v", bun.Requirement)
	}

	// compare sentinels are diagnostics next to the UNKNOWN match.
	if len(res.Errors) != 2 {
		t.Fatalf("errors = %v, want exactly the two compare diagnostics", res.Errors)
	}
	if !errors.Is(res.Errors[0], compare.ErrInvalidObservedVersion) || !strings.HasPrefix(res.Errors[0].Error(), "deno: ") {
		t.Errorf("errors[0] = %v", res.Errors[0])
	}
	if !errors.Is(res.Errors[1], compare.ErrPrereleaseRange) || !strings.HasPrefix(res.Errors[1].Error(), "php: ") {
		t.Errorf("errors[1] = %v", res.Errors[1])
	}
	if ExitCode(res) != ExitInconclusive {
		t.Errorf("ExitCode = %d, want 3", ExitCode(res))
	}
}

func TestVerifyUnsupportedRequirementIsOperationalError(t *testing.T) {
	insp := &fakeInspector{answers: map[string]outcome{
		"node": {obs: core.Observation{Name: "node", Present: true, Version: "24.7.0"}},
	}}
	res := Verify(context.Background(), file(req("postgres", ">=17"), req("node", ">=22")), []inspect.Inspector{insp})

	if len(res.Matches) != 1 || res.Matches[0].Requirement.Name != "node" || res.Matches[0].Status != core.StatusPass {
		t.Fatalf("matches = %+v, want only node PASS", res.Matches)
	}
	if len(res.Errors) != 1 || !errors.Is(res.Errors[0], ErrUnsupportedRequirement) {
		t.Fatalf("errors = %v, want ErrUnsupportedRequirement", res.Errors)
	}
	if !reflect.DeepEqual(insp.asked, []string{"node"}) {
		t.Errorf("inspector asked about %v; unsupported names must never reach it", insp.asked)
	}
	if ExitCode(res) != ExitInconclusive {
		t.Errorf("ExitCode = %d, want 3 (operational error, even though the only match passed)", ExitCode(res))
	}
}

func TestVerifyInspectionFailureWithoutPresenceIsNotUnreachable(t *testing.T) {
	cases := map[string]outcome{
		"zero observation":            {obs: core.Observation{}, err: errors.New("resolving node: permission denied")},
		"absent with error":           {obs: core.Observation{Name: "node", Present: false}, err: errProbe},
		"wrong name with error":       {obs: core.Observation{Name: "npm", Present: true}, err: errProbe},
		"present with version+error":  {obs: core.Observation{Name: "node", Present: true, Version: "24.7.0"}, err: errProbe},
		"unsupported tool from probe": {obs: core.Observation{}, err: errors.New("unsupported tool for NodeInspector")},
	}
	for name, o := range cases {
		t.Run(name, func(t *testing.T) {
			insp := &fakeInspector{answers: map[string]outcome{"node": o}}
			res := Verify(context.Background(), file(req("node", ">=22")), []inspect.Inspector{insp})
			if len(res.Matches) != 0 {
				t.Fatalf("a failure without established presence must not fabricate a match: %+v", res.Matches)
			}
			if len(res.Errors) != 1 || !errors.Is(res.Errors[0], o.err) {
				t.Fatalf("errors = %v, want the inspection error", res.Errors)
			}
			if ExitCode(res) != ExitInconclusive {
				t.Errorf("ExitCode = %d, want 3", ExitCode(res))
			}
		})
	}
}

func TestVerifyMissingBinaryIsFail(t *testing.T) {
	insp := &fakeInspector{answers: map[string]outcome{"pnpm": {obs: core.Observation{Name: "pnpm", Present: false}}}}
	res := Verify(context.Background(), file(req("pnpm", "10.15.1")), []inspect.Inspector{insp})
	if len(res.Errors) != 0 || len(res.Matches) != 1 || res.Matches[0].Status != core.StatusFail {
		t.Fatalf("result = %+v", res)
	}
	if ExitCode(res) != ExitFail {
		t.Errorf("ExitCode = %d, want 1 (missing binary is a conclusive FAIL)", ExitCode(res))
	}
}

func TestVerifyContextCancellationStopsProbing(t *testing.T) {
	insp := &fakeInspector{answers: map[string]outcome{
		"node": {obs: core.Observation{Name: "node", Present: true, Version: "24.7.0"}},
		"npm":  {obs: core.Observation{Name: "npm", Present: true, Version: "11.0.0"}},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := Verify(ctx, file(req("node", ">=22"), req("npm", ">=10")), []inspect.Inspector{insp})
	if len(insp.asked) != 0 {
		t.Errorf("inspector was asked %v after cancellation", insp.asked)
	}
	if len(res.Matches) != 0 || len(res.Errors) != 2 || !errors.Is(res.Errors[0], context.Canceled) {
		t.Fatalf("result = %+v", res)
	}
	if ExitCode(res) != ExitInconclusive {
		t.Errorf("ExitCode = %d, want 3", ExitCode(res))
	}
}

func TestVerifyNilFile(t *testing.T) {
	res := Verify(context.Background(), nil, nil)
	if len(res.Errors) != 1 || len(res.Matches) != 0 || ExitCode(res) != ExitInconclusive {
		t.Fatalf("result = %+v", res)
	}
}

func TestVerifyDoesNotMutateFile(t *testing.T) {
	insp := &fakeInspector{answers: map[string]outcome{
		"node": {obs: core.Observation{Name: "node", Present: true, Version: "24.7.0"}},
		"npm":  {obs: core.Observation{Name: "npm", Present: true, Version: "11.0.0"}},
	}}
	f := file(req("npm", ">=10"), req("node", ">=22"))
	before := []string{f.Requirements[0].Name, f.Requirements[1].Name}
	Verify(context.Background(), f, []inspect.Inspector{insp})
	after := []string{f.Requirements[0].Name, f.Requirements[1].Name}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("Verify reordered the file's requirements: %v → %v", before, after)
	}
}

func TestVerifyIsDeterministic(t *testing.T) {
	mk := func() *fakeInspector {
		return &fakeInspector{answers: map[string]outcome{
			"node": {obs: core.Observation{Name: "node", Present: true, Version: "24.7.0"}},
			"npm":  {obs: core.Observation{Name: "npm", Present: true}, err: errProbe},
			"pnpm": {obs: core.Observation{Name: "pnpm", Present: true, Version: "x"}},
		}}
	}
	f := file(req("pnpm", ">=10"), req("node", ">=22"), req("npm", ">=10"))
	a := Verify(context.Background(), f, []inspect.Inspector{mk()})
	b := Verify(context.Background(), f, []inspect.Inspector{mk()})
	if !reflect.DeepEqual(a.Matches, b.Matches) {
		t.Errorf("matches differ between runs:\n%+v\n%+v", a.Matches, b.Matches)
	}
	if len(a.Errors) != len(b.Errors) {
		t.Fatalf("error counts differ: %v vs %v", a.Errors, b.Errors)
	}
	for i := range a.Errors {
		if a.Errors[i].Error() != b.Errors[i].Error() {
			t.Errorf("error %d differs: %v vs %v", i, a.Errors[i], b.Errors[i])
		}
	}
}

func TestExitCodeContract(t *testing.T) {
	m := func(s core.MatchStatus) core.Match { return core.Match{Status: s} }
	pass, fail, unknown, unreachable := m(core.StatusPass), m(core.StatusFail), m(core.StatusUnknown), m(core.StatusUnreachable)
	opErr := errors.New("operational")

	cases := []struct {
		name string
		res  Result
		want int
	}{
		{"empty", Result{}, ExitPass},
		{"all pass", Result{Matches: []core.Match{pass, pass}}, ExitPass},
		{"one fail", Result{Matches: []core.Match{pass, fail}}, ExitFail},
		{"all fail", Result{Matches: []core.Match{fail, fail}}, ExitFail},
		{"unknown only", Result{Matches: []core.Match{pass, unknown}}, ExitInconclusive},
		{"unreachable only", Result{Matches: []core.Match{pass, unreachable}}, ExitInconclusive},
		{"fail + unknown → 3", Result{Matches: []core.Match{fail, unknown}}, ExitInconclusive},
		{"fail + unreachable → 3", Result{Matches: []core.Match{fail, unreachable}}, ExitInconclusive},
		{"unknown + fail (order irrelevant)", Result{Matches: []core.Match{unknown, fail}}, ExitInconclusive},
		{"pass + operational error", Result{Matches: []core.Match{pass}, Errors: []error{opErr}}, ExitInconclusive},
		{"fail + operational error → 3", Result{Matches: []core.Match{fail}, Errors: []error{opErr}}, ExitInconclusive},
		{"operational error only", Result{Errors: []error{opErr}}, ExitInconclusive},
		{"foreign status is never conclusive", Result{Matches: []core.Match{fail, m(core.MatchStatus("maybe"))}}, ExitInconclusive},
		{"zero status is never conclusive", Result{Matches: []core.Match{m("")}}, ExitInconclusive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExitCode(tc.res); got != tc.want {
				t.Errorf("ExitCode = %d, want %d", got, tc.want)
			}
		})
	}
	if ExitUsage != 2 {
		t.Errorf("ExitUsage = %d, the public contract says 2", ExitUsage)
	}
}

// TestVerifyDeclaredSectionsWithoutVerifierAreErrors pins that a file
// declaring services or environment variables can never verify clean:
// nothing checks them in v0.1, so the run is inconclusive (3), not 0.
func TestVerifyDeclaredSectionsWithoutVerifierAreErrors(t *testing.T) {
	insp := &fakeInspector{answers: map[string]outcome{
		"node": {obs: core.Observation{Name: "node", Present: true, Version: "24.7.0"}},
	}}
	f := file(req("node", ">=22"))
	f.Services = map[string]schema.Service{"redis": {Version: ">=7"}, "postgres": {Version: ">=16", Port: 5432}}
	f.Environment = schema.Environment{Required: []string{"DATABASE_URL"}, Optional: []string{"DEBUG"}}

	res := Verify(context.Background(), f, []inspect.Inspector{insp})
	if len(res.Matches) != 1 || res.Matches[0].Status != core.StatusPass {
		t.Fatalf("matches = %+v", res.Matches)
	}
	if len(res.Errors) != 3 {
		t.Fatalf("errors = %v, want postgres, redis (sorted) and environment", res.Errors)
	}
	for i, want := range []string{`service "postgres"`, `service "redis"`, "environment section declares 2 variable(s)"} {
		if !errors.Is(res.Errors[i], ErrUnsupportedRequirement) || !strings.Contains(res.Errors[i].Error(), want) {
			t.Errorf("errors[%d] = %v, want ErrUnsupportedRequirement mentioning %q", i, res.Errors[i], want)
		}
	}
	if ExitCode(res) != ExitInconclusive {
		t.Errorf("ExitCode = %d, want 3", ExitCode(res))
	}

	// Sections only, no requirements: still inconclusive, never 0.
	only := &schema.File{Version: schema.Version, Services: map[string]schema.Service{"postgres": {Version: "16"}}}
	if res := Verify(context.Background(), only, nil); ExitCode(res) != ExitInconclusive || len(res.Matches) != 0 {
		t.Errorf("services-only file: exit %d, matches %+v; want 3 and none", ExitCode(res), res.Matches)
	}
}

// cancellingInspector cancels the run from inside its first Inspect and
// returns what a real inspector returns for an interrupted probe: the
// partial "present" observation plus a cancellation error.
type cancellingInspector struct {
	cancel context.CancelFunc
	asked  []string
}

func (c *cancellingInspector) Supports(string) bool { return true }
func (c *cancellingInspector) Inspect(_ context.Context, req core.Requirement) (core.Observation, error) {
	c.asked = append(c.asked, req.Name)
	c.cancel()
	return core.Observation{Name: req.Name, Present: true}, fmt.Errorf("inspecting %s: version probe cancelled: %w", req.Name, context.Canceled)
}

// TestVerifyCancelledProbeIsNeverUnreachable pins that a probe cut short
// by cancellation does not become a machine verdict, the remaining
// requirements are not probed, and Cancelled reports the run.
func TestVerifyCancelledProbeIsNeverUnreachable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	insp := &cancellingInspector{cancel: cancel}
	res := Verify(ctx, file(req("node", ">=22"), req("npm", ">=10")), []inspect.Inspector{insp})
	if len(res.Matches) != 0 {
		t.Fatalf("a cancelled probe produced a match: %+v", res.Matches)
	}
	if len(insp.asked) != 1 {
		t.Fatalf("inspector asked %v; probing must stop after cancellation", insp.asked)
	}
	if len(res.Errors) != 2 || !errors.Is(res.Errors[0], context.Canceled) || !errors.Is(res.Errors[1], context.Canceled) {
		t.Fatalf("errors = %v", res.Errors)
	}
	if !Cancelled(res) || ExitCode(res) != ExitInconclusive {
		t.Errorf("Cancelled = %v, ExitCode = %d", Cancelled(res), ExitCode(res))
	}
	if Cancelled(Result{Errors: []error{errors.New("other")}}) {
		t.Error("Cancelled must only report cancellation errors")
	}
}

// FuzzExitCode pins the aggregation contract for arbitrary mixes of
// statuses and operational errors: any error or any non-verdict status
// forces 3; otherwise any FAIL forces 1; otherwise 0. The order of
// matches never matters.
func FuzzExitCode(f *testing.F) {
	f.Add("pf", 0)
	f.Add("pu", 0)
	f.Add("fr", 1)
	f.Add("", 0)
	f.Add("p", 2)
	f.Add("fx", 0)
	f.Fuzz(func(t *testing.T, statuses string, errs int) {
		var res Result
		var anyFail, anyOther bool
		for _, c := range statuses {
			var s core.MatchStatus
			switch c {
			case 'p':
				s = core.StatusPass
			case 'f':
				s = core.StatusFail
				anyFail = true
			case 'u':
				s = core.StatusUnknown
				anyOther = true
			case 'r':
				s = core.StatusUnreachable
				anyOther = true
			default:
				s = core.MatchStatus(string(c))
				anyOther = true
			}
			res.Matches = append(res.Matches, core.Match{Status: s})
		}
		if errs < 0 {
			errs = -errs
		}
		for i := 0; i < errs%4; i++ {
			res.Errors = append(res.Errors, errors.New("operational"))
		}
		want := ExitPass
		switch {
		case len(res.Errors) > 0 || anyOther:
			want = ExitInconclusive
		case anyFail:
			want = ExitFail
		}
		if got := ExitCode(res); got != want {
			t.Fatalf("statuses %q errors %d: ExitCode = %d, want %d", statuses, len(res.Errors), got, want)
		}
		// Order independence.
		for i, j := 0, len(res.Matches)-1; i < j; i, j = i+1, j-1 {
			res.Matches[i], res.Matches[j] = res.Matches[j], res.Matches[i]
		}
		if got := ExitCode(res); got != want {
			t.Fatalf("ExitCode depends on match order")
		}
	})
}

// TestVerifyKeepsExecutedPathOnEveryMatch pins that the executed path
// reaches the match whatever the verdict — including UNREACHABLE, whose
// whole point is "this binary was there but did not answer" — and that
// it never changes which status or exit code results.
func TestVerifyKeepsExecutedPathOnEveryMatch(t *testing.T) {
	const p = "/home/u/.volta/bin/"
	insp := &fakeInspector{answers: map[string]outcome{
		"node": {obs: core.Observation{Name: "node", Present: true, Version: "24.7.0", Path: p + "node"}},
		"npm":  {obs: core.Observation{Name: "npm", Present: true, Version: "9.0.0", Path: p + "npm"}},
		"yarn": {obs: core.Observation{Name: "yarn", Present: true, Path: p + "yarn"}, err: errProbe},
		"pnpm": {obs: core.Observation{Name: "pnpm", Present: false}},
	}}
	res := Verify(context.Background(), file(req("node", ">=22"), req("npm", ">=10"), req("yarn", "present"), req("pnpm", "present")), []inspect.Inspector{insp})
	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	want := map[string]struct {
		status core.MatchStatus
		path   string
	}{
		"node": {core.StatusPass, p + "node"},
		"npm":  {core.StatusFail, p + "npm"},
		"yarn": {core.StatusUnreachable, p + "yarn"},
		"pnpm": {core.StatusFail, ""},
	}
	for _, m := range res.Matches {
		w := want[m.Requirement.Name]
		if m.Status != w.status || m.Observation.Path != w.path {
			t.Errorf("%s: status %q path %q, want %q %q", m.Requirement.Name, m.Status, m.Observation.Path, w.status, w.path)
		}
	}
	if got := ExitCode(res); got != ExitInconclusive {
		t.Errorf("ExitCode = %d, want %d (unreachable beats fail)", got, ExitInconclusive)
	}
}
