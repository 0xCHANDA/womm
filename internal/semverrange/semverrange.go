// Package semverrange parses version ranges using npm's range grammar
// and evaluates them with Masterminds/semver.
//
// Masterminds/semver accepts a superset of npm's syntax: comma-
// separated sets (">=22, <25"), the "!=" operator, the "=>" / "=<"
// typos, Ruby's "~>", prerelease tags on partial versions (">=22-0")
// and leading zeros. npm's own parser rejects all of these, and a
// range npm cannot parse never satisfies anything. Feeding such a
// string to Masterminds would let WOMM answer PASS for a declaration
// npm itself treats as unsatisfiable. This package closes that gap:
// it admits exactly the grammar below, normalizes whitespace, and only
// then hands the range to Masterminds.
//
//	range      := set ( "||" set )*
//	set        := hyphen | comparator ( ws comparator )*
//	hyphen     := partial ws "-" ws partial
//	comparator := ( ">=" | "<=" | ">" | "<" | "=" | "^" | "~" )? ws* partial
//	partial    := "v"? xr ( "." xr ( "." xr qualifier? )? )?
//	xr         := "x" | "X" | "*" | nr           (nr: 0 | [1-9][0-9]*)
//	qualifier  := ( "-" pre )? ( "+" build )?   (only on full x.y.z with no wildcard)
//
// Empty sets ("", ">=22 ||") are rejected too: npm reads them as "*",
// which would silently accept every version.
package semverrange

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// ErrSyntax marks a range outside the supported npm grammar. The
// error text names the offending part; the range is never
// reinterpreted.
var ErrSyntax = errors.New("unsupported version range syntax")

// MaxLen bounds the accepted range length; longer inputs are refused
// before any parsing.
const MaxLen = 512

const (
	nr      = `(?:0|[1-9][0-9]*)`
	xr      = `(?:x|X|\*|` + nr + `)`
	ident   = `[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*`
	partial = `v?` + xr + `(?:\.` + xr + `(?:\.` + xr + `(?:-` + ident + `)?(?:\+` + ident + `)?)?)?`
	op      = `(?:>=|<=|>|<|=|\^|~)`
)

var (
	comparatorRe = regexp.MustCompile(`^(` + op + `)?(` + partial + `)$`)
	hyphenRe     = regexp.MustCompile(`^(` + partial + `)\s+-\s+(` + partial + `)$`)
	opOnlyRe     = regexp.MustCompile(`^` + op + `$`)
	// wildcardQualifierRe catches "x.y.z-pre" shapes where a component is
	// a wildcard: syntactically partial-like, semantically meaningless.
	wildcardQualifierRe = regexp.MustCompile(`^v?(?:[^.]*[xX*][^.]*\.|[^.]*\.[^.]*[xX*][^.]*\.|[^.]*\.[^.]*\.[^.]*[xX*])`)
)

// Parse validates s against the npm grammar and returns the equivalent
// Masterminds constraint. Whitespace is normalized (one space between
// comparators, " || " between sets) before Masterminds sees it, so
// its own parser never gets a chance to be lenient.
func Parse(s string) (*semver.Constraints, error) {
	if len(s) > MaxLen {
		return nil, fmt.Errorf("%w: range longer than %d bytes", ErrSyntax, MaxLen)
	}
	sets := strings.Split(s, "||")
	normalized := make([]string, 0, len(sets))
	for _, set := range sets {
		n, err := normalizeSet(set)
		if err != nil {
			return nil, err
		}
		normalized = append(normalized, n)
	}
	joined := strings.Join(normalized, " || ")
	c, err := semver.NewConstraint(joined)
	if err != nil {
		// The grammar above is stricter than Masterminds', so this
		// should be unreachable; if it is not, still fail closed.
		return nil, fmt.Errorf("%w: %q: %v", ErrSyntax, s, err)
	}
	return c, nil
}

// normalizeSet validates one "||"-free set and returns it with
// canonical spacing.
func normalizeSet(set string) (string, error) {
	fields := strings.Fields(set)
	if len(fields) == 0 {
		return "", fmt.Errorf("%w: empty comparator set (npm would read it as \"*\")", ErrSyntax)
	}

	// Hyphen range: exactly "partial - partial".
	if len(fields) == 3 && fields[1] == "-" {
		joined := fields[0] + " - " + fields[2]
		m := hyphenRe.FindStringSubmatch(joined)
		if m == nil {
			return "", fmt.Errorf("%w: hyphen range %q", ErrSyntax, strings.TrimSpace(set))
		}
		for _, p := range []string{m[1], m[2]} {
			if err := checkPartial(p); err != nil {
				return "", err
			}
		}
		return joined, nil
	}

	// Comparators, allowing "op ws partial" as npm does.
	var out []string
	for i := 0; i < len(fields); i++ {
		tok := fields[i]
		if opOnlyRe.MatchString(tok) {
			if i+1 >= len(fields) {
				return "", fmt.Errorf("%w: dangling operator %q", ErrSyntax, tok)
			}
			i++
			tok += fields[i]
		}
		m := comparatorRe.FindStringSubmatch(tok)
		if m == nil {
			return "", fmt.Errorf("%w: comparator %q", ErrSyntax, tok)
		}
		if err := checkPartial(m[2]); err != nil {
			return "", err
		}
		out = append(out, tok)
	}
	return strings.Join(out, " "), nil
}

// checkPartial rejects a prerelease/build qualifier attached to a
// version with a wildcard component ("1.x.3-beta"): npm's grammar
// admits the shape but no version can be meant by it.
func checkPartial(p string) error {
	if strings.ContainsAny(p, "-+") && wildcardQualifierRe.MatchString(p) {
		return fmt.Errorf("%w: qualifier on a wildcard version %q", ErrSyntax, p)
	}
	return nil
}
