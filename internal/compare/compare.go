// Package compare evaluates machine observations against project requirements.
// It is pure domain logic: it performs no inspection, I/O, process execution,
// or presentation.
package compare

import (
	"errors"
	"fmt"

	"github.com/Masterminds/semver/v3"

	"github.com/0xCHANDA/womm/internal/core"
)

var (
	// ErrNameMismatch means the requirement and observation refer to
	// different targets and therefore cannot be compared safely.
	ErrNameMismatch = errors.New("requirement and observation names differ")
	// ErrInvalidConstraint means a non-present requirement is not a valid
	// semver constraint.
	ErrInvalidConstraint = errors.New("invalid requirement constraint")
	// ErrInvalidObservedVersion means a present target reported a non-empty
	// version that is not strict semver.
	ErrInvalidObservedVersion = errors.New("invalid observed version")
)

// Compare determines whether obs satisfies req without mutating either input.
// Expected incompatibility and absence are represented by Match statuses;
// malformed or structurally mismatched inputs additionally return an error.
func Compare(req core.Requirement, obs core.Observation) (core.Match, error) {
	match := core.Match{Requirement: req, Observation: obs}

	if req.Name != obs.Name {
		match.Status = core.StatusUnknown
		match.Reason = "requirement and observation refer to different targets"
		return match, fmt.Errorf("%w: requirement %q, observation %q", ErrNameMismatch, req.Name, obs.Name)
	}

	if req.Constraint == "present" {
		if obs.Present {
			match.Status = core.StatusPass
			match.Reason = "target is present"
		} else {
			match.Status = core.StatusFail
			match.Reason = "required target is absent"
		}
		return match, nil
	}

	constraint, err := semver.NewConstraint(req.Constraint)
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

	version, err := semver.StrictNewVersion(obs.Version)
	if err != nil {
		match.Status = core.StatusUnknown
		match.Reason = "observed version is invalid"
		return match, fmt.Errorf("%w %q: %v", ErrInvalidObservedVersion, obs.Version, err)
	}

	if constraint.Check(version) {
		match.Status = core.StatusPass
		match.Reason = "observed version satisfies the requirement"
	} else {
		match.Status = core.StatusFail
		match.Reason = "observed version does not satisfy the requirement"
	}
	return match, nil
}
