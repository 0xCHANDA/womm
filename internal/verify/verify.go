// Package verify orchestrates the WOMM pipeline for one womm.yaml:
//
//	requirements → inspection → comparison / unreachable mapping → matches
//
// It owns exactly one piece of logic that no other layer may own: the
// mapping of an inspection failure on a target that is known to be
// present onto core.StatusUnreachable. Compare stays pure (no error
// mapping), inspectors stay judgment-free (they report what they saw),
// reporting stays presentation-only. Verify also decides nothing about
// process exit; ExitCode is a pure function callers apply to the result.
package verify

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/0xCHANDA/womm/internal/compare"
	"github.com/0xCHANDA/womm/internal/core"
	"github.com/0xCHANDA/womm/internal/inspect"
	"github.com/0xCHANDA/womm/internal/schema"
)

// ErrUnsupportedRequirement marks a declaration this WOMM build cannot
// observe: a requirement no configured inspector supports, or a
// womm.yaml section (services, environment) with no verifier yet. It
// is an operational failure of the build, not a statement about the
// machine: no match is fabricated and the run is inconclusive.
var ErrUnsupportedRequirement = errors.New("no inspector supports requirement")

// Exit codes of `womm verify`. The full contract lives in ExitCode.
const (
	ExitPass         = 0
	ExitFail         = 1
	ExitUsage        = 2
	ExitInconclusive = 3
)

// Result is the outcome of verifying one file.
type Result struct {
	// Matches holds one core.Match per requirement that could be
	// taken through inspection, in requirement-name order.
	Matches []core.Match
	// Errors holds operational failures that produced no match:
	// unsupported requirements, inspection failures where presence
	// was never established, cancellation. In requirement-name order.
	// They are diagnostics, never verdicts.
	Errors []error
}

// Verify inspects every requirement of f with the first inspector that
// supports it and compares the outcome. It never stops early on a
// failed requirement: the caller gets every match and every
// operational error. Context cancellation is the exception: once ctx
// is done, remaining requirements are not probed, a probe interrupted
// by the cancellation is an operational error (never an unreachable
// verdict), and Cancelled(res) reports the situation to callers.
//
// Mapping rules, applied per requirement:
//
//   - no supporting inspector             → Errors (ErrUnsupportedRequirement)
//   - inspection ok                       → compare.Compare; the match is
//     kept whatever its status, and a compare sentinel (malformed or
//     undecidable input) is additionally recorded in Errors
//   - inspection failed, presence known   → StatusUnreachable match
//     (Observation{Present: true} came back with the error)
//   - inspection failed, presence unknown → Errors only (no match)
func Verify(ctx context.Context, f *schema.File, inspectors []inspect.Inspector) Result {
	var res Result
	if f == nil {
		res.Errors = append(res.Errors, errors.New("nil womm.yaml file"))
		return res
	}

	// Declared sections this build cannot verify are operational
	// errors, never silently skipped: a file that declares only
	// services or environment variables must not verify as PASS.
	names := make([]string, 0, len(f.Services))
	for name := range f.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		res.Errors = append(res.Errors, fmt.Errorf("%w: service %q (services are not verified by WOMM v0.1)", ErrUnsupportedRequirement, name))
	}
	if n := len(f.Environment.Required) + len(f.Environment.Optional); n > 0 {
		res.Errors = append(res.Errors, fmt.Errorf("%w: environment section declares %d variable(s) (environment is not verified by WOMM v0.1)", ErrUnsupportedRequirement, n))
	}

	reqs := make([]core.Requirement, len(f.Requirements))
	copy(reqs, f.Requirements)
	sort.SliceStable(reqs, func(i, j int) bool { return reqs[i].Name < reqs[j].Name })

	for _, req := range reqs {
		if err := ctx.Err(); err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("%s: verification cancelled before inspection: %w", req.Name, err))
			continue
		}
		insp := supporting(inspectors, req.Name)
		if insp == nil {
			res.Errors = append(res.Errors, fmt.Errorf("%w: %q", ErrUnsupportedRequirement, req.Name))
			continue
		}

		obs, err := insp.Inspect(ctx, req)
		if err != nil {
			if cerr := ctx.Err(); cerr != nil {
				// The probe did not fail; WOMM was told to stop. That
				// says nothing about the machine, so it is never an
				// unreachable verdict — an operational error, and the
				// loop above records the rest as cancelled too.
				res.Errors = append(res.Errors, fmt.Errorf("%s: verification cancelled during inspection: %w", req.Name, cerr))
				continue
			}
			if obs.Present && obs.Name == req.Name && obs.Version == "" {
				// Presence established, query failed: the target is
				// there but WOMM could not ask it anything. That is
				// the definition of unreachable, and it is a verdict
				// about the machine, so it becomes a match.
				res.Matches = append(res.Matches, core.Match{
					Requirement: req,
					Observation: obs,
					Status:      core.StatusUnreachable,
					Reason:      "target is present but could not be queried: " + err.Error(),
				})
				continue
			}
			// Nothing was established about the target; the failure is
			// WOMM's, not the machine's. No match is fabricated.
			res.Errors = append(res.Errors, fmt.Errorf("%s: inspection failed: %w", req.Name, err))
			continue
		}

		match, cerr := compare.Compare(req, obs)
		res.Matches = append(res.Matches, match)
		if cerr != nil {
			res.Errors = append(res.Errors, fmt.Errorf("%s: %w", req.Name, cerr))
		}
	}
	return res
}

func supporting(inspectors []inspect.Inspector, name string) inspect.Inspector {
	for _, i := range inspectors {
		if i.Supports(name) {
			return i
		}
	}
	return nil
}

// ExitCode maps a Result onto the public exit-code contract of
// `womm verify`:
//
//	0  every match is PASS and there are no operational errors
//	1  at least one FAIL, and no UNKNOWN, UNREACHABLE or operational
//	   error at all (a conclusive incompatibility)
//	3  anything inconclusive: an UNKNOWN or UNREACHABLE match, or any
//	   operational error — even alongside FAILs (3 wins over 1)
//
// 2 (usage error) is never produced here; it belongs to the CLI. An
// empty result (nothing to verify, nothing failed) is 0.
func ExitCode(r Result) int {
	if len(r.Errors) > 0 {
		return ExitInconclusive
	}
	fail := false
	for _, m := range r.Matches {
		switch m.Status {
		case core.StatusPass:
		case core.StatusFail:
			fail = true
		default:
			// StatusUnknown, StatusUnreachable and any status this
			// build does not know: never conclusive.
			return ExitInconclusive
		}
	}
	if fail {
		return ExitFail
	}
	return ExitPass
}

// Cancelled reports whether r carries a cancellation error: the run was
// interrupted and its matches are partial. Callers must not present
// them as a verdict.
func Cancelled(r Result) bool {
	for _, err := range r.Errors {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return true
		}
	}
	return false
}
