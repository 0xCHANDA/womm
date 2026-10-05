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

// maxGoModBytes bounds go.mod. A real one is a few KB (the Go distribution's
// own is under 10 KB); the generic 16 MiB source cap let a hostile file be
// lexed into ~1.9 GB of tokens. A larger go.mod is an explicit error.
const maxGoModBytes = 1 << 20

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
	data, ok, err := source.ReadIfPresentMax(projectRoot, "go.mod", maxGoModBytes)
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

// tok is one token of the go.mod lexer, a reduction of x/mod's reader
// (the go command's own).
type tok struct {
	text string
	kind byte // 'w' word, 'q' "quoted", 'r' `raw`, 'p' punctuation , [ ] { } ( ), '\n' end of line
	line int
}

func isPunct(c byte) bool {
	switch c {
	case ',', '[', ']', '{', '}', '(', ')':
		return true
	}
	return false
}

// lex splits go.mod into tokens the way cmd/go's reader does for the
// purposes of finding statements: // comments dropped, "..." and `...`
// single tokens (so a `go` inside a string is not a verb; a raw string
// cannot span lines), and , [ ] { } ( ) tokens of their own that also end
// a word — `go,` is the verb followed by a comma, not a word "go,".
// /* */ comments are refused, as the go command refuses them.
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
		case isPunct(c):
			out = append(out, tok{string(c), 'p', line})
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
				if d == ' ' || d == '\t' || d == '\r' || d == '\n' || isPunct(d) || d == '"' || d == '`' ||
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
// block is a module path, not the directive. As in the go command, `(`
// opens a block only as the LAST token of a line (a `(` anywhere else is
// an ordinary argument) and a block ends only at a line that STARTS with
// `)`; anything else is the same file the go command would read
// differently, which is exactly the kind of disagreement that would drop
// or invent a requirement.
func parseGoDirective(data []byte) (version string, found bool, err error) {
	toks, err := lex(data)
	if err != nil {
		return "", false, err
	}
	// Split into lines.
	var lines [][]tok
	var cur []tok
	for _, t := range toks {
		if t.kind == '\n' {
			if len(cur) > 0 {
				lines = append(lines, cur)
			}
			cur = nil
			continue
		}
		cur = append(cur, t)
	}
	if len(cur) > 0 {
		lines = append(lines, cur)
	}

	inBlock := false
	blockLine := 0
	for _, l := range lines {
		if inBlock {
			if l[0].kind == 'p' && l[0].text == ")" {
				inBlock = false
			}
			continue
		}
		verb, args := l[0], l[1:]
		isGo := verb.kind == 'w' && verb.text == "go"
		if len(args) == 1 && args[0].kind == 'p' && args[0].text == "(" {
			if isGo {
				return "", false, fmt.Errorf("line %d: the go directive cannot be a block", verb.line)
			}
			inBlock, blockLine = true, verb.line
			continue
		}
		if !isGo {
			continue
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
	if inBlock {
		return "", false, fmt.Errorf("line %d: unterminated block", blockLine)
	}
	return version, found, nil
}
