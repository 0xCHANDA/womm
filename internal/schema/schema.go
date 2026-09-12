// Package schema loads and validates the portable womm.yaml
// specification (schema v1).
//
// schema is persistence/serialization infrastructure on top of the
// canonical domain model (internal/core): requirements parsed from
// YAML surface as core.Requirement values, so detectors and the schema
// loader share one model without adapters.
//
// Compatibility policy (see womm.yaml Schema v1 note):
//
//	version == 1      → supported.
//	version >  1      → error; never verify silently against an
//	                    unknown schema.
//	version <  1      → error.
//	version missing   → error.
//	unknown v1 keys   → warning, not error (not preserved, not stored).
//
// EVIDENCE_CONFLICT detection belongs to a later capture flow, never
// to --force, which only overwrites an existing womm.yaml.
package schema

import (
	"fmt"

	"github.com/0xCHANDA/womm/internal/core"
)

// Version is the supported womm.yaml schema version.
const Version = 1

// File is the in-memory representation of a valid womm.yaml.
type File struct {
	Version      int
	Requirements []core.Requirement
	Services     map[string]Service
	Environment  Environment
}

// Service mirrors schema v1 services entries.
type Service struct {
	Version string
	Port    int
}

// Environment mirrors schema v1 environment section. Only variable
// NAMES are stored, never values.
type Environment struct {
	Required []string
	Optional []string
}

// SupportedVersions are the versions this binary can fully understand.
func SupportedVersions() map[int]bool {
	return map[int]bool{1: true}
}

// versionError belongs to the ErrVersion family: every version-policy
// violation (missing, non-integer, too old, too future) unwraps to it
// so callers can check errors.Is(err, schema.ErrVersion).
type versionError struct {
	// present is false when the version key itself is absent.
	present bool
	// stated is the literal version text the file declared, if any.
	stated string
	// obsolete marks a stated but invalid, too-old version (v < 1):
	// the file is wrong, not "too old for this binary". No "upgrade
	// womm" hint; the document itself violates schema v1.
	obsolete bool
}

func (e *versionError) Error() string {
	switch {
	case !e.present:
		return "womm.yaml has no version field"
	case e.stated == "":
		return "womm.yaml version must be an integer"
	case e.obsolete:
		return fmt.Sprintf("womm.yaml uses obsolete schema version %s; this binary supports v%d and does not run on an invalid version.", e.stated, Version)
	}
	return fmt.Sprintf("womm.yaml uses schema version %s; this binary supports v%d. Please upgrade womm.", e.stated, Version)
}

// Unwrap ties every version violation to ErrVersion.
func (e *versionError) Unwrap() error { return ErrVersion }
