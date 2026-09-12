// Package core defines the universal environment model of WOMM.
//
// These types are ecosystem-agnostic: Node, Python, Go, Git, Docker,
// PostgreSQL, Redis and any other detector must be representable in
// them without changes. No core type may depend on a specific
// technology (see the architecture note: DETECTED != REQUIRED).
package core

// Requirement is a single environment requirement declared or detected
// for a project, together with the evidence that backs it.
type Requirement struct {
	// Identifier of the requirement ("node", "pnpm", "postgres").
	Name string
	// Version constraint: a semver range (">=22 <25") or the sentinel
	// "present" (must exist, any version).
	Constraint string
	// Explicit project evidence backing the requirement. A requirement
	// without evidence is invalid (evidence over assumptions).
	Evidence []Evidence
}
