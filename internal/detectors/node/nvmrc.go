package node

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// rangeVersion matches the only `.nvmrc` selector WOMM v0.0.1 supports
// as a verifiable requirement: a fully determined Node version.
var exactVersion = regexp.MustCompile(`^v?\d+\.\d+\.\d+$`)

// nvmKnownSelectors enumerates nvm-syntax values that are VALID for
// nvm but deliberately unsupported by WOMM v0.0.1 (short versions,
// aliases and lts selectors). They must never be reinterpreted (e.g.
// "22" must not become "22.0.0").
var nvmKnownSelectors = regexp.MustCompile(`^(v?\d+(\.\d+){0,2}|lts/\*|lts/[a-z-]+|node|stable|default|latest)$`)

// supportsConstraint reports whether the value can be used under our
// semver policy (any npm-style range; Masterminds/semver decides).
func supportsConstraint(c string) bool {
	_, err := semver.NewConstraint(c)
	return err == nil
}

// unsupportedSelector builds the explicit error for nvm selectors
// outside our v0.0.1 subset. The wording must NOT claim the value is
// invalid: it is valid nvm syntax, just not supported yet.
func unsupportedSelector(selector string) error {
	return fmt.Errorf(".nvmrc selector %q is valid nvm syntax but is not supported by WOMM v0.0.1; use an explicit x.y.z version", selector)
}

// uninterpretableSelector is for content that is neither blank,
// comment, key=value nor any known nvm selector: WOMM refuses to
// guess what the author meant.
func uninterpretableSelector(line string) error {
	return fmt.Errorf(".nvmrc: cannot interpret %q (not a supported selector; refusing to guess)", line)
}

// nvmrcSelector examines one significant line (blank lines and
// comments already filtered). Returns a canonical, deterministic
// representation of the exact version for supported selectors, or an
// explicit error.
//
// Validation is two-layered: the regex is the shape filter for the
// "exact version" branch, and the version itself must survive STRICT
// semver parsing (semver.StrictNewVersion). A formally invalid
// x.y.z-shaped value (e.g. leading zeros: "01.2.3") never produces a
// Requirement.
func nvmrcSelector(line string) (string, error) {
	switch {
	case exactVersion.MatchString(line):
		normalized := strings.TrimPrefix(line, "v")
		v, err := semver.StrictNewVersion(normalized)
		if err != nil {
			return "", fmt.Errorf(".nvmrc selector %q is not a valid exact Node version (strict semver): %w", line, err)
		}
		return v.String(), nil
	case nvmKnownSelectors.MatchString(line):
		return "", unsupportedSelector(line)
	default:
		return "", uninterpretableSelector(line)
	}
}
