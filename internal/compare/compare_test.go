package compare

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Masterminds/semver/v3"

	"github.com/0xCHANDA/womm/internal/core"
)

func TestCompare(t *testing.T) {
	tests := []struct {
		name       string
		constraint string
		present    bool
		version    string
		wantStatus core.MatchStatus
		wantErr    error
	}{
		{name: "present exists", constraint: "present", present: true, version: "not-semver", wantStatus: core.StatusPass},
		{name: "present missing", constraint: "present", present: false, wantStatus: core.StatusFail},
		{name: "exact equal", constraint: "10.15.1", present: true, version: "10.15.1", wantStatus: core.StatusPass},
		{name: "exact different", constraint: "10.15.1", present: true, version: "10.15.2", wantStatus: core.StatusFail},
		{name: "exact prerelease equal", constraint: "4.0.0-rc.1", present: true, version: "4.0.0-rc.1", wantStatus: core.StatusPass},
		{name: "exact prerelease different tag", constraint: "4.0.0-rc.1", present: true, version: "4.0.0-rc.2", wantStatus: core.StatusFail},
		{name: "exact release vs prerelease observed", constraint: "24.7.0", present: true, version: "24.7.0-beta.1", wantStatus: core.StatusFail},
		{name: "exact prerelease vs release observed", constraint: "24.7.0-beta.1", present: true, version: "24.7.0", wantStatus: core.StatusFail},
		{name: "exact ignores build metadata", constraint: "24.7.0", present: true, version: "24.7.0+build.5", wantStatus: core.StatusPass},
		{name: "lower range satisfied", constraint: ">=22", present: true, version: "24.7.0", wantStatus: core.StatusPass},
		{name: "lower range below", constraint: ">=22", present: true, version: "20.19.0", wantStatus: core.StatusFail},
		{name: "compound inside", constraint: ">=22 <25", present: true, version: "24.7.0", wantStatus: core.StatusPass},
		{name: "compound lower boundary", constraint: ">=22 <25", present: true, version: "22.0.0", wantStatus: core.StatusPass},
		{name: "compound upper boundary", constraint: ">=22 <25", present: true, version: "25.0.0", wantStatus: core.StatusFail},
		{name: "caret range", constraint: "^20.10.0", present: true, version: "20.19.0", wantStatus: core.StatusPass},
		{name: "caret range next major", constraint: "^20.10.0", present: true, version: "21.0.0", wantStatus: core.StatusFail},
		{name: "x range", constraint: "20.x", present: true, version: "20.19.0", wantStatus: core.StatusPass},
		{name: "or range", constraint: "^18.18.0 || ^20.9.0 || >=21.1.0", present: true, version: "20.19.0", wantStatus: core.StatusPass},
		{name: "prerelease comparator with release observed inside", constraint: ">=24.0.0-0", present: true, version: "24.7.0", wantStatus: core.StatusPass},
		{name: "prerelease comparator with release observed outside", constraint: ">=24.0.0-0 <25.0.0-0", present: true, version: "25.0.0", wantStatus: core.StatusFail},
		{name: "missing observation", constraint: ">=22", present: false, wantStatus: core.StatusFail},
		{name: "unknown observed version", constraint: ">=22", present: true, wantStatus: core.StatusUnknown},
		{name: "unparseable observed version", constraint: ">=22", present: true, version: "v24.7.0", wantStatus: core.StatusUnknown, wantErr: ErrInvalidObservedVersion},
		{name: "short observed version is not coerced", constraint: ">=22", present: true, version: "24", wantStatus: core.StatusUnknown, wantErr: ErrInvalidObservedVersion},
		{name: "invalid requirement constraint", constraint: "definitely-not-semver", present: true, version: "24.7.0", wantStatus: core.StatusUnknown, wantErr: ErrInvalidConstraint},
		{name: "empty requirement constraint", constraint: "", present: true, version: "24.7.0", wantStatus: core.StatusUnknown, wantErr: ErrInvalidConstraint},
		{name: "invalid constraint cannot pass missing target", constraint: "definitely-not-semver", present: false, wantStatus: core.StatusUnknown, wantErr: ErrInvalidConstraint},
		{name: "comma set is not npm syntax", constraint: ">=22, <25", present: true, version: "24.7.0", wantStatus: core.StatusUnknown, wantErr: ErrInvalidConstraint},
		{name: "not-equal is not npm syntax", constraint: "!=24.7.0", present: true, version: "24.8.0", wantStatus: core.StatusUnknown, wantErr: ErrInvalidConstraint},
		{name: "pessimistic operator is not npm syntax", constraint: "~>24.0", present: true, version: "24.7.0", wantStatus: core.StatusUnknown, wantErr: ErrInvalidConstraint},
		{name: "trailing or is not npm syntax", constraint: ">=99 ||", present: true, version: "24.7.0", wantStatus: core.StatusUnknown, wantErr: ErrInvalidConstraint},
		{name: "prerelease observed against ordinary range", constraint: ">=22 <25", present: true, version: "24.7.0-beta.1", wantStatus: core.StatusUnknown, wantErr: ErrPrereleaseRange},
		{name: "prerelease observed against prerelease comparator range", constraint: ">=24.0.0-0", present: true, version: "24.7.0-beta.1", wantStatus: core.StatusUnknown, wantErr: ErrPrereleaseRange},
		{name: "prerelease observed against same-tuple prerelease comparator", constraint: ">=24.7.0-beta.0 <25", present: true, version: "24.7.0-beta.1", wantStatus: core.StatusUnknown, wantErr: ErrPrereleaseRange},
		{name: "prerelease observed against wildcard", constraint: "*", present: true, version: "24.7.0-beta.1", wantStatus: core.StatusUnknown, wantErr: ErrPrereleaseRange},
		{name: "prerelease observed against operator-exact", constraint: "=24.7.0-beta.1", present: true, version: "24.7.0-beta.1", wantStatus: core.StatusUnknown, wantErr: ErrPrereleaseRange},
		{name: "inconsistent observation with range", constraint: ">=22", present: false, version: "24.7.0", wantStatus: core.StatusUnknown, wantErr: ErrInconsistentObservation},
		{name: "inconsistent observation with exact", constraint: "24.7.0", present: false, version: "24.7.0", wantStatus: core.StatusUnknown, wantErr: ErrInconsistentObservation},
		{name: "inconsistent observation with present", constraint: "present", present: false, version: "24.7.0", wantStatus: core.StatusUnknown, wantErr: ErrInconsistentObservation},
		{name: "inconsistent observation beats invalid constraint", constraint: "definitely-not-semver", present: false, version: "garbage", wantStatus: core.StatusUnknown, wantErr: ErrInconsistentObservation},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := core.Requirement{Name: "node", Constraint: tc.constraint}
			obs := core.Observation{Name: "node", Present: tc.present, Version: tc.version}
			originalReq := req
			originalObs := obs

			got, err := Compare(req, obs)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want errors.Is(_, %v)", err, tc.wantErr)
			}
			if tc.wantErr != nil && got.Status != core.StatusUnknown {
				t.Errorf("an error must never come with a deterministic status; got %q", got.Status)
			}
			if got.Status != tc.wantStatus {
				t.Errorf("Status = %q, want %q", got.Status, tc.wantStatus)
			}
			if got.Requirement.Name != req.Name || got.Requirement.Constraint != req.Constraint {
				t.Errorf("Match.Requirement = %#v, want %#v", got.Requirement, req)
			}
			if got.Observation != obs {
				t.Errorf("Match.Observation = %#v, want %#v", got.Observation, obs)
			}
			if got.Reason == "" {
				t.Error("Reason is empty")
			}
			if !reflect.DeepEqual(req, originalReq) || obs != originalObs {
				t.Errorf("Compare mutated inputs: req=%#v obs=%#v", req, obs)
			}
		})
	}
}

func TestCompareRejectsNameMismatch(t *testing.T) {
	req := core.Requirement{Name: "node", Constraint: "present"}
	obs := core.Observation{Name: "npm", Present: true, Version: "11.0.0"}

	got, err := Compare(req, obs)
	if !errors.Is(err, ErrNameMismatch) {
		t.Fatalf("error = %v, want ErrNameMismatch", err)
	}
	if got.Status != core.StatusUnknown {
		t.Errorf("Status = %q, want %q", got.Status, core.StatusUnknown)
	}
}

// TestCompareInconsistentObservationNeverDecides pins the policy for the
// contradictory Observation{Present: false, Version: non-empty}: whatever
// the constraint, the result is StatusUnknown plus ErrInconsistentObservation,
// never a PASS and never a FAIL.
func TestCompareInconsistentObservationNeverDecides(t *testing.T) {
	obs := core.Observation{Name: "node", Present: false, Version: "24.7.0"}
	for _, constraint := range []string{"present", "24.7.0", ">=22", ">=22 <25", "^24.0.0", "*", "not-semver", ""} {
		t.Run(constraint, func(t *testing.T) {
			got, err := Compare(core.Requirement{Name: "node", Constraint: constraint}, obs)
			if !errors.Is(err, ErrInconsistentObservation) {
				t.Fatalf("error = %v, want ErrInconsistentObservation", err)
			}
			if got.Status != core.StatusUnknown {
				t.Errorf("Status = %q, want %q", got.Status, core.StatusUnknown)
			}
		})
	}
}

// TestCompareNeverGuessesPrereleaseRanges is the regression guard for the
// npm prerelease divergence. The first half proves the hazard is real: the
// underlying semver library alone would PASS the mission example, which
// node-semver's documented rule rejects. The second half proves Compare
// does not let that (or any prerelease-against-range pair) become a verdict.
func TestCompareNeverGuessesPrereleaseRanges(t *testing.T) {
	c, err := semver.NewConstraint(">=24.0.0-0")
	if err != nil {
		t.Fatal(err)
	}
	v := semver.MustParse("24.7.0-beta.1")
	if !c.Check(v) {
		t.Fatalf("premise changed: Masterminds/semver no longer admits %s for >=24.0.0-0; re-evaluate the prerelease policy", v)
	}

	pairs := []struct{ constraint, version string }{
		{">=24.0.0-0", "24.7.0-beta.1"},          // Masterminds PASS, node-semver FAIL, npm CLI PASS
		{">=22 <25", "24.7.0-beta.1"},            // Masterminds FAIL, node-semver FAIL, npm CLI PASS
		{">=24.7.0-beta.0 <25", "24.7.0-beta.1"}, // all three PASS, still a range: refused uniformly
		{"~1.2.3-beta.2", "1.2.4-alpha"},         // Masterminds PASS, node-semver FAIL
		{"<25.0.0-0", "24.7.0-beta.1"},           // Masterminds PASS, node-semver FAIL
	}
	for _, p := range pairs {
		t.Run(p.constraint+" vs "+p.version, func(t *testing.T) {
			got, err := Compare(
				core.Requirement{Name: "node", Constraint: p.constraint},
				core.Observation{Name: "node", Present: true, Version: p.version},
			)
			if !errors.Is(err, ErrPrereleaseRange) {
				t.Fatalf("error = %v, want ErrPrereleaseRange", err)
			}
			if got.Status != core.StatusUnknown {
				t.Fatalf("Status = %q, want %q (a prerelease against a range must never become a verdict)", got.Status, core.StatusUnknown)
			}
		})
	}
}

// TestCompareDoesNotAliasEvidence documents that the returned Match carries
// the caller's Requirement by value, including the evidence slice header:
// Compare neither mutates nor copies it. Callers that need an independent
// copy (e.g. a renderer that reorders evidence) must clone it themselves.
func TestCompareDoesNotAliasEvidence(t *testing.T) {
	ev := []core.Evidence{{Source: "package.json", Field: "engines.node", Value: ">=22"}}
	req := core.Requirement{Name: "node", Constraint: ">=22", Evidence: ev}
	obs := core.Observation{Name: "node", Present: true, Version: "24.7.0"}

	got, err := Compare(req, obs)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Requirement.Evidence, ev) {
		t.Fatalf("Evidence = %#v, want %#v", got.Requirement.Evidence, ev)
	}
	if ev[0].Value != ">=22" {
		t.Fatalf("Compare mutated caller evidence: %#v", ev)
	}
}

func TestCompareIsDeterministic(t *testing.T) {
	cases := []struct {
		req core.Requirement
		obs core.Observation
	}{
		{core.Requirement{Name: "node", Constraint: ">=22 <25"}, core.Observation{Name: "node", Present: true, Version: "24.7.0"}},
		{core.Requirement{Name: "node", Constraint: ">=22 <25"}, core.Observation{Name: "node", Present: true, Version: "24.7.0-beta.1"}},
		{core.Requirement{Name: "node", Constraint: ">=22"}, core.Observation{Name: "node", Present: false, Version: "24.7.0"}},
	}
	for _, c := range cases {
		first, firstErr := Compare(c.req, c.obs)
		second, secondErr := Compare(c.req, c.obs)
		if !reflect.DeepEqual(first, second) || errString(firstErr) != errString(secondErr) {
			t.Fatalf("same inputs produced different outputs: (%#v, %v) != (%#v, %v)", first, firstErr, second, secondErr)
		}
	}
}

func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}
