package compare

import (
	"errors"
	"reflect"
	"testing"

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
		{name: "lower range satisfied", constraint: ">=22", present: true, version: "24.7.0", wantStatus: core.StatusPass},
		{name: "lower range below", constraint: ">=22", present: true, version: "20.19.0", wantStatus: core.StatusFail},
		{name: "compound inside", constraint: ">=22 <25", present: true, version: "24.7.0", wantStatus: core.StatusPass},
		{name: "compound lower boundary", constraint: ">=22 <25", present: true, version: "22.0.0", wantStatus: core.StatusPass},
		{name: "compound upper boundary", constraint: ">=22 <25", present: true, version: "25.0.0", wantStatus: core.StatusFail},
		{name: "missing observation", constraint: ">=22", present: false, wantStatus: core.StatusFail},
		{name: "unknown observed version", constraint: ">=22", present: true, wantStatus: core.StatusUnknown},
		{name: "unparseable observed version", constraint: ">=22", present: true, version: "v24.7.0", wantStatus: core.StatusUnknown, wantErr: ErrInvalidObservedVersion},
		{name: "short observed version is not coerced", constraint: ">=22", present: true, version: "24", wantStatus: core.StatusUnknown, wantErr: ErrInvalidObservedVersion},
		{name: "invalid requirement constraint", constraint: "definitely-not-semver", present: true, version: "24.7.0", wantStatus: core.StatusUnknown, wantErr: ErrInvalidConstraint},
		{name: "invalid constraint cannot pass missing target", constraint: "definitely-not-semver", present: false, wantStatus: core.StatusUnknown, wantErr: ErrInvalidConstraint},
		{name: "prerelease excluded by ordinary range", constraint: ">=22 <25", present: true, version: "24.7.0-beta.1", wantStatus: core.StatusFail},
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

func TestCompareIsDeterministic(t *testing.T) {
	req := core.Requirement{Name: "node", Constraint: ">=22 <25"}
	obs := core.Observation{Name: "node", Present: true, Version: "24.7.0"}

	first, firstErr := Compare(req, obs)
	second, secondErr := Compare(req, obs)
	if !reflect.DeepEqual(first, second) || !errors.Is(firstErr, secondErr) {
		t.Fatalf("same inputs produced different outputs: (%#v, %v) != (%#v, %v)", first, firstErr, second, secondErr)
	}
}
