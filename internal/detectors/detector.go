// Package detectors defines the boundary between project inspection
// and the canonical domain model. Detectors inspect the project (L0:
// filesystem reads only — no execution, no PATH introspection) and
// produce core.Requirement values with explicit evidence.
//
// Architecture frontier (see Environment/Architecture notes):
//
//	Detector    inspects the project → core.Requirement (L0)
//	Inspector   inspects the machine → core.Observation  (L1/L2, later)
//
// No core type may acquire technology-specific detail.
package detectors

import (
	"context"

	"github.com/0xCHANDA/womm/internal/core"
)

// Detector is the L0 project-inspection boundary. Implementations
// must:
//
//   - only read files under projectRoot (L0);
//   - produce requirements each backed by explicit Evidence;
//   - never execute binaries, scripts or lifecycle hooks;
//   - never print (reporting is a separate concern);
//   - fail with explicit errors on interpretable-but-invalid sources.
//
// A technology with no declared requirements yields an empty slice,
// not an error: absence of evidence is not an error.
type Detector interface {
	Name() string
	Detect(ctx context.Context, projectRoot string) ([]core.Requirement, error)
}
