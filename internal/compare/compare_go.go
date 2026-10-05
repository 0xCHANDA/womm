package compare

import (
	"fmt"

	"github.com/0xCHANDA/womm/internal/core"
	"github.com/0xCHANDA/womm/internal/goversion"
)

// goName is the requirement name whose constraints use Go's version
// grammar and order instead of npm's.
const goName = "go"

// compareGo decides a requirement named "go". Its constraint is ">=V"
// (a go.mod `go` directive is a minimum) in Go's version syntax, and the
// order is Go's own (internal/goversion, i.e. go/version): 1.21 <
// 1.21rc1 < 1.21.0, so Go 1.21rc1 satisfies `go 1.21` but not
// `go 1.21.0`. Nothing of semver or semverrange applies, and there is no
// "prerelease against a range" refusal: Go's rule for a release
// candidate is explicit and decided by the same code the go command runs.
// Anything that is not a valid Go version — constraint or observation —
// is StatusUnknown plus a sentinel, never a guess.
//
// The "present" constraint and the absent/contradictory cases were
// already decided by the caller.
func compareGo(match core.Match, req core.Requirement, obs core.Observation) (core.Match, error) {
	min, err := goversion.ParseMin(req.Constraint)
	if err != nil {
		match.Status = core.StatusUnknown
		match.Reason = "requirement constraint is invalid"
		return match, fmt.Errorf("%w %q: %v", ErrInvalidConstraint, req.Constraint, err)
	}
	if !obs.Present {
		match.Status = core.StatusFail
		match.Reason = "required target is absent"
		return match, nil
	}
	if obs.Version == "" {
		match.Status = core.StatusUnknown
		match.Reason = "target is present but its version is unknown"
		return match, nil
	}
	if !goversion.Valid(obs.Version) {
		match.Status = core.StatusUnknown
		match.Reason = "observed version is invalid"
		return match, fmt.Errorf("%w %q: not a Go version", ErrInvalidObservedVersion, obs.Version)
	}
	if goversion.Compare(obs.Version, min) >= 0 {
		match.Status = core.StatusPass
		match.Reason = "observed version satisfies the requirement"
	} else {
		match.Status = core.StatusFail
		match.Reason = "observed version does not satisfy the requirement"
	}
	return match, nil
}
