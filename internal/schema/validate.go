package schema

import (
	"errors"
	"fmt"
	"unicode"
	"unicode/utf8"
)

// ErrVersion identifies the family of version-policy violations:
// missing, non-integer, past or future schema version. Errors (never
// warnings): WOMM must never verify silently against a schema it does
// not understand.
var ErrVersion = errors.New("unsupported womm.yaml schema version")

// validate enforces the v1 contract. A requirement without explicit
// evidence is invalid: no requirement without evidence.
func validate(f *File) error {
	names := map[string]bool{}
	for i := range f.Requirements {
		r := &f.Requirements[i]
		if r.Name == "" {
			return fmt.Errorf("requirements[%d]: name is required", i)
		}
		if !validName(r.Name) {
			return fmt.Errorf("requirements[%d]: name %q must not contain whitespace or control characters", i, r.Name)
		}
		if names[r.Name] {
			return fmt.Errorf("requirements[%d]: duplicate name %q", i, r.Name)
		}
		names[r.Name] = true
		if r.Constraint == "" {
			return fmt.Errorf("requirements[%d] (%s): constraint is required", i, r.Name)
		}
		if len(r.Evidence) == 0 {
			return fmt.Errorf("requirements[%d] (%s): at least one evidence entry is required", i, r.Name)
		}
		for j := range r.Evidence {
			ev := &r.Evidence[j]
			if ev.Source == "" {
				return fmt.Errorf("requirements[%d] (%s): evidence[%d].source is required", i, r.Name, j)
			}
			if ev.Field == "" {
				return fmt.Errorf("requirements[%d] (%s): evidence[%d].field is required", i, r.Name, j)
			}
		}
	}
	for name, s := range f.Services {
		if s.Version == "" {
			return fmt.Errorf("services.%s: version is required", name)
		}
	}
	return nil
}

// Validate is the exported entry point for callers that already hold a
// File (e.g. for round-trip validation).
func Validate(f *File) error {
	if f == nil {
		return errors.New("nil womm.yaml file")
	}
	if !SupportedVersions()[f.Version] {
		return &versionError{present: true, stated: fmt.Sprintf("%d", f.Version), obsolete: f.Version < 1}
	}
	return validate(f)
}

// validName reports whether name can stand as a requirement identifier:
// non-empty, valid UTF-8, no whitespace, no control characters. Names
// are matched against inspector tool names and printed on their own
// report line, so a name that can break a line or hide characters is
// refused.
func validName(name string) bool {
	if name == "" || !utf8.ValidString(name) {
		return false
	}
	for _, r := range name {
		// Whitespace, controls, and format characters (Cf: zero-width
		// joiners, bidi overrides, soft hyphen, BOM) — the last can
		// reorder or hide part of a report line while looking like a
		// plain name.
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}
