package node

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// versionLiteral finds concrete version tokens inside a constraint
// string ("20", "22.14.0", "v20.1"): candidates for witness analysis.
var versionLiteral = regexp.MustCompile(`(?:^|[^\d.])(v?\d+(?:\.\d+){0,2})(?:[^\d.]|$)`)

// toVersion converts a literal token to a full semver version.
// "22" → 22.0.0, "22.14" → 22.14.0, "v22" → 22.0.0.
func toVersion(tok string) (*semver.Version, error) {
	return semver.NewVersion(strings.TrimPrefix(strings.TrimSpace(tok), "v"))
}

// supportsConstraint reports whether the constraint can be analyzed
// under our semver policy. Formats the semver library cannot parse
// (nvm-style "lts/*", "lts/iron", "node") are unsupported, never
// guessed.
func supportsConstraint(c string) bool {
	_, err := semver.NewConstraint(c)
	return err == nil
}

// compatible decides, deterministically, whether two constraints can
// both be satisfied by some version.
//
// Method (documented heuristic scope): the candidate-witness set is
// every concrete version literal appearing in either constraint. The
// ranges are compatible iff at least one candidate satisfies both.
//
// Limits: a range intersection that contains no boundary literal of
// either side would be misreported as a conflict. This is
// deterministic and acceptable for v0: conflicts FAIL loudly (never
// silently compatible), so misreported conflicts are visible to the
// user with both evidence listings. Full range-intersection algebra
// would be a later refinement.
func compatible(a, b string) (bool, error) {
	ca, err := semver.NewConstraint(a)
	if err != nil {
		return false, fmt.Errorf("unsupported constraint %q: %w", a, err)
	}
	cb, err := semver.NewConstraint(b)
	if err != nil {
		return false, fmt.Errorf("unsupported constraint %q: %w", b, err)
	}
	for _, lit := range append(literalsOf(a), literalsOf(b)...) {
		if ca.Check(lit) && cb.Check(lit) {
			return true, nil
		}
	}
	return false, nil
}

// literalsOf extracts the concrete versions referenced by a
// constraint, deterministically sorted.
func literalsOf(c string) []*semver.Version {
	var out []*semver.Version
	for _, m := range versionLiteral.FindAllStringSubmatch(c, -1) {
		v, err := toVersion(m[1])
		if err != nil {
			continue
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LessThan(out[j]) })
	return out
}

// unsupportedConstraint builds the explicit error for formats outside
// our semver policy: never guessed, never silently compatible.
func unsupportedConstraint(source, constraint string) error {
	return fmt.Errorf("%s: unsupported node constraint %q (semver-compatible formats only; 'lts/*'-style values are not interpreted)", source, constraint)
}
