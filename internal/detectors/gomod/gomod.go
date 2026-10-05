// Package gomod is the L0 detector for Go projects: it reads the `go`
// directive of go.mod and nothing else. Static only — no `go list`, no
// module download, no network, no execution of any kind.
//
// What becomes a requirement, and why:
//
//   - `go 1.21` is a MINIMUM: with GOTOOLCHAIN=local the go command
//     refuses to run when it is older ("go.mod requires go >= 1.99
//     (running go 1.24.7; GOTOOLCHAIN=local)"). It is emitted as
//     Requirement{go, ">=1.21"} with the line as evidence.
//   - the `toolchain` directive is NOT a requirement: it is a
//     suggestion consumed only by GOTOOLCHAIN=auto switching. Verified
//     against the go command: `go 1.21` + `toolchain go1.99.0` builds
//     with GOTOOLCHAIN=local on an older toolchain. So there is nothing
//     to conflict with the go line, and the directive is ignored rather
//     than guessed into a requirement.
//
// A go.mod without a go directive declares no Go requirement.
package gomod

import (
	"context"
	"errors"
	"fmt"

	"github.com/0xCHANDA/womm/internal/core"
	"github.com/0xCHANDA/womm/internal/detectors/source"
	"github.com/0xCHANDA/womm/internal/goversion"
)

// ErrMalformed marks a go.mod whose go directive cannot be read
// unambiguously (repeated, in a block, wrong arity, invalid version) or
// whose syntax this reader refuses to guess about.
var ErrMalformed = errors.New("malformed go.mod")

// Detector reads the go directive of go.mod.
type Detector struct{}

// New returns the detector.
func New() Detector { return Detector{} }

// Name is the detector's name.
func (Detector) Name() string { return "go" }

// Detect implements detectors.Detector.
func (Detector) Detect(_ context.Context, projectRoot string) ([]core.Requirement, error) {
	data, ok, err := source.ReadIfPresent(projectRoot, "go.mod")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	v, found, err := parseGoDirective(data)
	if err != nil {
		return nil, fmt.Errorf("%w: go.mod: %v", ErrMalformed, err)
	}
	if !found {
		return nil, nil
	}
	return []core.Requirement{{
		Name:       "go",
		Constraint: goversion.FormatMin(v),
		Evidence:   []core.Evidence{{Source: "go.mod", Field: "go", Value: v}},
	}}, nil
}

// token kinds of the go.mod lexer (a reduction of x/mod's).
type tok struct {
	text string
	kind byte // 'w' word, 'q' quoted, 'r' raw, '(' , ')', '\n', '=' for =>
	line int
}

// lex splits go.mod into tokens the way cmd/go's reader does for the
// purposes of finding statements: // comments dropped, "..." and `...`
// kept as single tokens (so a `go` inside a string is not a verb),
// parentheses and newlines significant. /* */ comments are refused, as
// the go command refuses them.
func lex(data []byte) ([]tok, error) {
	var out []tok
	line := 1
	i := 0
	for i < len(data) {
		c := data[i]
		switch {
		case c == '\n':
			out = append(out, tok{"\n", '\n', line})
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '/' && i+1 < len(data) && data[i+1] == '/':
			for i < len(data) && data[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(data) && data[i+1] == '*':
			return nil, fmt.Errorf("line %d: /* */ comments are not allowed; use //", line)
		case c == '(' || c == ')':
			out = append(out, tok{string(c), c, line})
			i++
		case c == '"':
			j := i + 1
			for j < len(data) && data[j] != '"' && data[j] != '\n' {
				if data[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(data) || data[j] != '"' {
				return nil, fmt.Errorf("line %d: unterminated quoted string", line)
			}
			out = append(out, tok{string(data[i : j+1]), 'q', line})
			i = j + 1
		case c == '`':
			j := i + 1
			for j < len(data) && data[j] != '`' && data[j] != '\n' {
				j++
			}
			if j >= len(data) || data[j] != '`' {
				return nil, fmt.Errorf("line %d: unterminated raw string", line)
			}
			out = append(out, tok{string(data[i : j+1]), 'r', line})
			i = j + 1
		default:
			j := i
			for j < len(data) {
				d := data[j]
				if d == ' ' || d == '\t' || d == '\r' || d == '\n' || d == '(' || d == ')' || d == '"' || d == '`' ||
					(d == '/' && j+1 < len(data) && (data[j+1] == '/' || data[j+1] == '*')) {
					break
				}
				j++
			}
			out = append(out, tok{string(data[i:j]), 'w', line})
			i = j
		}
	}
	return out, nil
}

// parseGoDirective returns the version of the go directive. Statements
// are found at depth zero only: a `go 1.99` line inside a `require (...)`
// block is a module path, not the directive.
func parseGoDirective(data []byte) (version string, found bool, err error) {
	toks, err := lex(data)
	if err != nil {
		return "", false, err
	}
	i := 0
	for i < len(toks) {
		// Skip blank lines.
		if toks[i].kind == '\n' {
			i++
			continue
		}
		verb := toks[i]
		// Collect the rest of the statement: to end of line, or a whole
		// parenthesised block.
		i++
		var args []tok
		block := false
		for i < len(toks) && toks[i].kind != '\n' {
			if toks[i].kind == '(' && len(args) == 0 {
				block = true
				i++
				for i < len(toks) && toks[i].kind != ')' {
					i++
				}
				if i >= len(toks) {
					return "", false, fmt.Errorf("line %d: unterminated block", verb.line)
				}
				i++ // the ')'
				break
			}
			args = append(args, toks[i])
			i++
		}
		if verb.kind != 'w' || verb.text != "go" {
			continue
		}
		if block {
			return "", false, fmt.Errorf("line %d: the go directive cannot be a block", verb.line)
		}
		if found {
			return "", false, fmt.Errorf("line %d: repeated go directive", verb.line)
		}
		if len(args) != 1 {
			return "", false, fmt.Errorf("line %d: go directive expects exactly one argument", verb.line)
		}
		a := args[0]
		if a.kind != 'w' || !goversion.Valid(a.text) {
			return "", false, fmt.Errorf("line %d: invalid go version %q: must match format 1.23.0", verb.line, a.text)
		}
		version, found = a.text, true
	}
	return version, found, nil
}
