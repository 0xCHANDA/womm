// Package compare evaluates machine observations against project requirements.
// It is pure domain logic: it performs no inspection, I/O, process execution,
// or presentation.
//
// Every outcome is either a deterministic core.MatchStatus backed by an
// explicit Reason, or core.StatusUnknown plus a sentinel error. Nothing is
// guessed: an input the engine cannot judge safely is reported as such,
// never coerced into a PASS or a FAIL.
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
	// ErrInconsistentObservation means the observation contradicts itself:
	// it reports a version for a target it also reports as absent. Such an
	// observation is not evidence of anything and must never be resolved
	// into a compatibility verdict.
	ErrInconsistentObservation = errors.New("inconsistent observation: absent target with a version")
	// ErrInvalidConstraint means a non-present requirement is not a valid
	// semver constraint.
	ErrInvalidConstraint = errors.New("invalid requirement constraint")
	// ErrInvalidObservedVersion means a present target reported a non-empty
	// version that is not strict semver.
	ErrInvalidObservedVersion = errors.New("invalid observed version")
	// ErrPrereleaseRange means a prerelease was observed for a requirement
	// expressed as a range (anything other than one exact version). WOMM
	// v0.1 refuses to decide that case instead of evaluating it.
	//
	// Three "npm" answers exist for the same pair, and they disagree:
	//
	//   - node-semver's documented default: a prerelease satisfies a range
	//     only if some comparator with the SAME major.minor.patch tuple also
	//     carries a prerelease. ">=24.0.0-0" does not admit 24.7.0-beta.1;
	//     ">=22 <25" does not admit it either.
	//   - npm's own engines check (npm-install-checks) calls satisfies with
	//     includePrerelease: true, so ">=22 <25" DOES admit 24.7.0-beta.1.
	//   - Masterminds/semver (this module's parser) enables prereleases for
	//     a whole AND-group as soon as any comparator in it has a prerelease
	//     and then compares plainly: ">=24.0.0-0" admits 24.7.0-beta.1,
	//     unlike node-semver's default.
	//
	// Evaluating any of them would silently pick one consumer's semantics
	// and produce a PASS or FAIL another consumer contradicts. WOMM does not
	// guess: the pair is StatusUnknown with this error, and the user decides.
	// Release observations are unaffected: for them all three agree,
	// including ranges whose comparators carry prereleases (plain semver
	// ordering).
	ErrPrereleaseRange = errors.New("prerelease observed against a version range")
)

// Compare determines whether obs satisfies req without mutating either input.
// Expected incompatibility and absence are represented by Match statuses;
// malformed, contradictory or undecidable inputs are StatusUnknown and
// additionally return a sentinel error.
//
// Semantics of Requirement.Constraint:
//
//   - "present": the target must exist; its version is irrelevant.
//   - an exact strict semver version ("24.7.0", "4.0.0-rc.1"): the observed
//     version must be exactly equal (prerelease and all).
//   - any other valid range (">=22 <25", "^20.10.0", "20.x"): evaluated for
//     release versions only. A prerelease observation against a range is
//     StatusUnknown + ErrPrereleaseRange (see that error for why).
func Compare(req core.Requirement, obs core.Observation) (core.Match, error) {
	match := core.Match{Requirement: req, Observation: obs}

	if req.Name != obs.Name {
		match.Status = core.StatusUnknown
		match.Reason = "requirement and observation refer to different targets"
		return match, fmt.Errorf("%w: requirement %q, observation %q", ErrNameMismatch, req.Name, obs.Name)
	}

	// A version can only be observed on a present target. An observation
	// that claims both "absent" and "version X" is contradictory evidence:
	// neither half can be trusted, so no branch below may consume it.
	if !obs.Present && obs.Version != "" {
		match.Status = core.StatusUnknown
		match.Reason = "observation is contradictory: the target is reported absent but with a version"
		return match, fmt.Errorf("%w: %q reported absent with version %q", ErrInconsistentObservation, obs.Name, obs.Version)
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

	// An exact version is decided by equality, which every semver
	// implementation agrees on — prerelease observations included.
	if exact, err := semver.StrictNewVersion(req.Constraint); err == nil {
		if version.Equal(exact) {
			match.Status = core.StatusPass
			match.Reason = "observed version equals the required version"
		} else {
			match.Status = core.StatusFail
			match.Reason = "observed version differs from the required version"
		}
		return match, nil
	}

	if version.Prerelease() != "" {
		match.Status = core.StatusUnknown
		match.Reason = "observed version is a prerelease; whether a prerelease satisfies a version range is not decided by WOMM"
		return match, fmt.Errorf("%w: %q against %q", ErrPrereleaseRange, obs.Version, req.Constraint)
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
