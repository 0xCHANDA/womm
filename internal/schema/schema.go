// Package schema loads and validates the portable womm.yaml
// specification (schema v1).
//
// Compatibility policy (see womm.yaml Schema v1 note):
//
//	version == 1      → supported.
//	version >  1      → error; never verify silently against an
//	                    unknown schema.
//	version <  1      → error.
//	version missing   → error.
//	unknown v1 keys   → warning, not error.
//
// EVIDENCE_CONFLICT detection belongs to the capture flow of a later
// PR; never to --force, which only overwrites an existing womm.yaml.
package schema

import "fmt"

// Version is the supported womm.yaml schema version.
const Version = 1

// File is the in-memory representation of a valid womm.yaml.
type File struct {
	Version      int
	Requirements []Requirement
	Services     map[string]Service
	Environment  Environment
}

// Requirement mirrors schema v1 requirements entries.
type Requirement struct {
	Name       string
	Constraint string
	Evidence   []Evidence
}

// Evidence mirrors schema v1 evidence entries.
type Evidence struct {
	Source string
	Field  string
	Value  string
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

type versionError struct {
	got     int
	present bool
}

func (e *versionError) Error() string {
	if e.present {
		return fmt.Sprintf("womm.yaml uses schema version %d; this binary supports v%d. Please upgrade womm.", e.got, Version)
	}
	return "womm.yaml has no version field"
}
