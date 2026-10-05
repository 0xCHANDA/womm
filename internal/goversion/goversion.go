// Package goversion is WOMM's view of Go version syntax and ordering.
//
// GO VERSION SYNTAX IS NOT SEMVER. "1.21" is a language version that
// sorts BEFORE "1.21rc1" and "1.21.0" (so Go 1.21rc1 satisfies `go 1.21`
// but not `go 1.21.0`), "1.20" and "1.20.0" are the same release, and
// "1.21rc1" is a release candidate with no dot. Reusing semverrange or
// Masterminds/semver here would be a lie, so ordering is delegated to the
// standard library's go/version — the same code the go command uses —
// and this package only adds a strict syntax gate in front of it.
//
// The gate is intentionally narrower than go/version: go/version also
// accepts a vendor suffix ("go1.21.0-bigcorp") and ignores it when
// ordering; WOMM treats such a string as not a version at all (an
// explicit refusal, never a guess).
package goversion

import (
	"fmt"
	"go/version"
	"regexp"
	"strings"
)

// maxLen bounds a version string; real ones are under 20 bytes.
const maxLen = 64

// syntax is the grammar the go command accepts for the go directive of
// go.mod ("must match format 1.23.0"): major.minor[.patch][kind N].
//
// One deliberate narrowing: the go command accepts any lowercase "kind"
// ("1.21foo1") and orders kinds as plain strings; only the kinds Go
// actually ships (alpha, beta, rc) are accepted here, so an exotic string
// is an explicit refusal instead of a guessed ordering.
var syntax = regexp.MustCompile(`^([1-9][0-9]*)\.(0|[1-9][0-9]*)(\.(0|[1-9][0-9]*))?((alpha|beta|rc)(0|[1-9][0-9]*))?$`)

// Valid reports whether v ("1.21", "1.21.3", "1.22rc1"; no "go" prefix)
// is a Go version in the strict syntax and also one go/version accepts
// (which refuses, for instance, a prerelease of a patch release such as
// "1.21.0rc1").
func Valid(v string) bool {
	return len(v) <= maxLen && syntax.MatchString(v) && version.IsValid("go"+v)
}

// Compare returns -1, 0 or +1 for a < b, a == b, a > b in Go's order.
// Both must be Valid; an invalid string would compare below every valid
// one, so callers check Valid first.
func Compare(a, b string) int { return version.Compare("go"+a, "go"+b) }

// MinPrefix starts every Go constraint WOMM writes: a go.mod `go`
// directive is a minimum version.
const MinPrefix = ">="

// FormatMin returns the constraint "at least v".
func FormatMin(v string) string { return MinPrefix + v }

// ParseMin extracts the version from a ">=V" constraint. Anything else —
// another operator, whitespace, a range, a "go" prefix — is an error:
// the only constraint WOMM writes for Go is a minimum.
func ParseMin(constraint string) (string, error) {
	v, ok := strings.CutPrefix(constraint, MinPrefix)
	if !ok || !Valid(v) {
		return "", fmt.Errorf("Go constraints are written %q followed by a version such as 1.21 or 1.21.3", MinPrefix)
	}
	return v, nil
}

// ParseVersionOutput extracts the version from `go version` output:
// exactly one non-blank line, "go version go<V> <os>/<arch>", optionally
// with one "X:..." experiment token before the platform. A development
// build ("devel ..."), a vendor suffix, extra text or an invalid version
// yield ok == false: unknown, never a guess.
func ParseVersionOutput(raw string) (string, bool) {
	var lines []string
	for _, l := range strings.Split(raw, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			lines = append(lines, t)
		}
	}
	if len(lines) != 1 {
		return "", false
	}
	f := strings.Fields(lines[0])
	if len(f) == 5 && strings.HasPrefix(f[3], "X:") {
		f = append(f[:3], f[4])
	}
	if len(f) != 4 || f[0] != "go" || f[1] != "version" || !strings.HasPrefix(f[2], "go") || !isPlatform(f[3]) {
		return "", false
	}
	v := strings.TrimPrefix(f[2], "go")
	if !Valid(v) {
		return "", false
	}
	return v, true
}

var platform = regexp.MustCompile(`^[a-z0-9]+/[a-z0-9._]+$`)

func isPlatform(s string) bool { return platform.MatchString(s) }
