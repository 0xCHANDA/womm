// Package inspect defines the machine-inspection boundary of WOMM.
// Implementations execute already-known system tools (L1) — or, in a
// later PR, query running services (L2) — to answer only "what does
// this machine currently have" for one already-declared requirement.
//
// Architecture frontier (see detectors.Detector doc):
//
//	Detector    inspects the project → core.Requirement (L0)
//	Inspector   inspects the machine → core.Observation  (L1/L2)
//	Compare     inspects both        → core.Match         (later PR)
//
// An Inspector must never decide whether an Observation satisfies a
// Requirement — that is core.Match's job — and must never inspect a
// tool that no Requirement asked for: inspection is demand-driven, one
// Requirement at a time, never an enumeration of the machine.
package inspect

import (
	"context"

	"github.com/0xCHANDA/womm/internal/core"
)

// Inspector is the machine-inspection boundary. Implementations must:
//
//   - only ever act on req.Name if it is one of their explicitly
//     supported tools; an unsupported name must never reach exec —
//     Supports exists precisely so callers (and Inspect itself) can
//     refuse before any resolution or execution is attempted;
//   - never execute anything beyond the known tool's fixed
//     version-query invocation (no shell, no project code, no
//     lifecycle hooks, no npx/corepack, no arbitrary command);
//   - resolve the executable from a sanitized system search path,
//     never the inherited process PATH and never a project-relative
//     or current-directory location;
//   - bound both execution time and captured output size;
//   - report a missing binary as Observation{Present: false}, not an
//     error — "not installed" is a legitimate machine state;
//   - report unparseable version output as Observation{Version: ""},
//     never a guessed or fabricated version.
type Inspector interface {
	// Supports reports whether this Inspector knows how to inspect
	// name. Inspect must refuse (not execute anything) when this is
	// false; callers may also use it to skip requirements up front.
	Supports(name string) bool
	// Inspect observes the machine for req.Name and returns the
	// resulting Observation. It returns a non-nil error only for a
	// genuine inspection failure (unsupported tool, execution
	// failure, timeout, cancellation) — never merely because the tool
	// is not installed.
	Inspect(ctx context.Context, req core.Requirement) (core.Observation, error)
}
