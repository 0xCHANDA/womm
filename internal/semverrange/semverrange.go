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
//	xr         := "x" | "X" | "*" | nr           (nr: 0 | [1-9][0-9]*, <= 2^53-1)
//	qualifier  := ( "-" pre )? ( "+" build )?   (only on full x.y.z with no wildcard)
//
// Rejected on top of the grammar (see checkPartial): numeric components
// after a wildcard ("x.1.2"), an operator on a wildcard major (">x",
// "^*"), and empty sets ("", ">=22 ||" — npm reads them as "*", which
// would silently accept every version).
package semverrange

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
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

// MaxNumeric is npm's numeric limit (Number.MAX_SAFE_INTEGER, 2^53-1).
// node-semver rejects components above it at parse time and ranges
// whose derived bounds would exceed it; WOMM refuses the limit value
// itself in ranges (covering every derived case) and anything above
// it in observed versions.
const MaxNumeric = 1<<53 - 1

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
)

// Parse validates s against the npm grammar and returns the equivalent
// Masterminds constraint. Whitespace is normalized (one space between
// comparators, " || " between sets) before Masterminds sees it, so
// its own parser never gets a chance to be lenient.
func Parse(s string) (*semver.Constraints, error) {
	joined, err := Normalize(s)
	if err != nil {
		return nil, err
	}
	c, err := semver.NewConstraint(joined)
	if err != nil {
		// The grammar above is stricter than Masterminds', so this
		// should be unreachable; if it is not, still fail closed.
		return nil, fmt.Errorf("%w: %q: %v", ErrSyntax, s, err)
	}
	return c, nil
}

// Normalize validates s against the grammar and returns its canonical
// spelling: comparators separated by one space, sets by " || ", no
// surrounding whitespace, no other whitespace characters. It is the
// form detectors store as a requirement constraint, so a declaration
// containing tabs or newlines can never reach a report verbatim.
func Normalize(s string) (string, error) {
	if len(s) > MaxLen {
		return "", fmt.Errorf("%w: range longer than %d bytes", ErrSyntax, MaxLen)
	}
	if strings.ContainsRune(s, '\u0085') {
		// Go's strings.Fields treats NEL as whitespace; JavaScript's
		// \s does not, so npm reads ">=18\u0085<20" as one garbage
		// token and rejects the range. Splitting on it here would
		// accept what npm refuses.
		return "", fmt.Errorf("%w: U+0085 (NEL) is not a separator for npm", ErrSyntax)
	}
	sets := strings.Split(s, "||")
	normalized := make([]string, 0, len(sets))
	for _, set := range sets {
		n, err := normalizeSet(set)
		if err != nil {
			return "", err
		}
		normalized = append(normalized, n)
	}
	return strings.Join(normalized, " || "), nil
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
			if err := checkPartial("-", p); err != nil {
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
		if err := checkPartial(m[1], m[2]); err != nil {
			return "", err
		}
		out = append(out, tok)
	}
	return strings.Join(out, " "), nil
}

// checkPartial applies the rules the regular expressions cannot: op is
// the comparator's operator ("" for a bare version, "-" for a hyphen
// range end), p the partial version.
//
//   - every numeric component must be at most MaxNumeric (2^53-1, npm's
//     MAX_SAFE_INTEGER): npm rejects larger numbers at parse time;
//   - once a component is a wildcard the rest must be wildcards too
//     ("x.1.2", "1.x.3"): npm cannot parse the first and both are a
//     meaningless spelling that would otherwise evaluate to something;
//   - an operator on a wildcard major (">x", "^*", "x - 2") is refused:
//     npm turns ">x" into "<0.0.0-0" (never satisfied) while
//     Masterminds accepts every version — a false PASS in the making.
//     Spell "*" instead;
//   - a prerelease/build qualifier is only meaningful on a full,
//     wildcard-free x.y.z ("1.x-beta" is refused).
func checkPartial(op, p string) error {
	core, qualifier := p, ""
	if i := strings.IndexAny(p, "-+"); i >= 0 {
		core, qualifier = p[:i], p[i:]
	}
	core = strings.TrimPrefix(core, "v")
	parts := strings.Split(core, ".")
	wildcard := false
	for _, part := range parts {
		switch part {
		case "x", "X", "*":
			wildcard = true
		default:
			if wildcard {
				return fmt.Errorf("%w: numeric component after a wildcard in %q", ErrSyntax, p)
			}
			if n, err := strconv.ParseUint(part, 10, 64); err != nil || n >= MaxNumeric {
				// npm rejects components above MAX_SAFE_INTEGER, and
				// also ranges whose *derived* bound needs
				// MAX_SAFE_INTEGER+1 ("^9007199254740991",
				// "1.9007199254740991.x", "<=9007199254740991"). The
				// literal limit alone let those through; refusing the
				// limit value itself covers every derived case.
				return fmt.Errorf("%w: version component %q in %q reaches npm's limit of %d", ErrSyntax, part, p, MaxNumeric)
			}
		}
	}
	if op == "~" && core == "0.0.0" {
		// Masterminds special-cases "~0.0.0" as ">=0.0.0" (any
		// version); npm reads it as ">=0.0.0 <0.1.0-0". A false PASS
		// in the making — refused; "~0.0" and "^0.0.0" are fine.
		return fmt.Errorf("%w: %q is evaluated differently by npm and the underlying library; spell the range explicitly", ErrSyntax, op+p)
	}
	if op != "" && (parts[0] == "x" || parts[0] == "X" || parts[0] == "*") {
		return fmt.Errorf("%w: operator on a wildcard major version %q (spell \"*\" for any version)", ErrSyntax, op+p)
	}
	if qualifier != "" && (wildcard || len(parts) != 3) {
		return fmt.Errorf("%w: qualifier on a wildcard or partial version %q", ErrSyntax, p)
	}
	return nil
}
