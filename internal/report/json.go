package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/0xCHANDA/womm/internal/core"
)

// JSONSchemaVersion is the version of the machine-readable document
// (`womm verify --format json`). It is part of the document from day
// one. A change that removes or renames a field, or changes the meaning
// of one, increments it; adding a field does not. docs/output-json.md
// is the public description.
const JSONSchemaVersion = 1

// Diagnostic is an operational error to include in the document: a
// failure that produced no verdict for some requirement (or for the
// run). Kind is a stable machine-readable code; Message is for humans
// and is not stable.
type Diagnostic struct {
	Kind    string
	Message string
}

// JSONDocument is everything RenderJSON presents. The report decides
// nothing: Result and ExitCode are values decided by the verify layer
// and presented verbatim.
type JSONDocument struct {
	// Result is the verdict name ("pass", "fail", "inconclusive").
	Result string
	// ExitCode is the process exit code the verdict maps to.
	ExitCode    int
	Matches     []core.Match
	Diagnostics []Diagnostic
}

// The types below are the PUBLIC schema, written out on purpose: the
// document is not a serialization of any internal type, so renaming or
// reshaping core, verify or compare can never change it by accident.
// Every field is always present (absent values are null, lists are
// never null), in this order.
type jsonDocument struct {
	SchemaVersion int               `json:"schemaVersion"`
	Result        string            `json:"result"`
	ExitCode      int               `json:"exitCode"`
	Requirements  []jsonRequirement `json:"requirements"`
	Errors        []jsonError       `json:"errors"`
	Summary       jsonSummary       `json:"summary"`
}

type jsonRequirement struct {
	Name        string          `json:"name"`
	Constraint  string          `json:"constraint"`
	Status      string          `json:"status"`
	Reason      string          `json:"reason"`
	Observation jsonObservation `json:"observation"`
	Evidence    []jsonEvidence  `json:"evidence"`
}

type jsonObservation struct {
	Present bool    `json:"present"`
	Version *string `json:"version"`
	Path    *string `json:"path"`
}

type jsonEvidence struct {
	Source string `json:"source"`
	Field  string `json:"field"`
	Value  string `json:"value"`
}

type jsonError struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type jsonSummary struct {
	Total       int `json:"total"`
	Pass        int `json:"pass"`
	Fail        int `json:"fail"`
	Unknown     int `json:"unknown"`
	Unreachable int `json:"unreachable"`
}

// RenderJSON writes the machine-readable report for doc to w: one JSON
// document, indented by two spaces, ending in a newline.
//
// Deterministic by construction: requirements in presentation order
// (see Sort), errors in the order given, fixed field order, no
// timestamps, host names, identifiers or environment values. Strings
// are valid UTF-8 (invalid bytes become U+FFFD, the only lossy step;
// control characters are escaped) and `<`, `>`, `&` are not escaped, so
// constraints stay readable. Nothing is written when a match has an
// unknown status: it fails before the first byte reaches w.
func RenderJSON(w io.Writer, doc JSONDocument) error {
	summary, err := Summarize(doc.Matches)
	if err != nil {
		return err
	}
	out := jsonDocument{
		SchemaVersion: JSONSchemaVersion,
		Result:        doc.Result,
		ExitCode:      doc.ExitCode,
		Requirements:  make([]jsonRequirement, 0, len(doc.Matches)),
		Errors:        make([]jsonError, 0, len(doc.Diagnostics)),
		Summary: jsonSummary{
			Total: summary.Total(), Pass: summary.Pass, Fail: summary.Fail,
			Unknown: summary.Unknown, Unreachable: summary.Unreachable,
		},
	}
	for _, m := range Sort(doc.Matches) {
		r := jsonRequirement{
			Name:       text(m.Requirement.Name),
			Constraint: text(m.Requirement.Constraint),
			Status:     string(m.Status),
			Reason:     text(m.Reason),
			Observation: jsonObservation{
				Present: m.Observation.Present,
				Version: optional(m.Observation.Version),
				Path:    optional(m.Observation.Path),
			},
			Evidence: make([]jsonEvidence, 0, len(m.Requirement.Evidence)),
		}
		for _, ev := range m.Requirement.Evidence {
			r.Evidence = append(r.Evidence, jsonEvidence{Source: text(ev.Source), Field: text(ev.Field), Value: text(ev.Value)})
		}
		out.Requirements = append(out.Requirements, r)
	}
	for _, d := range doc.Diagnostics {
		out.Errors = append(out.Errors, jsonError{Kind: text(d.Kind), Message: text(d.Message)})
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return err
	}
	_, err = w.Write(escapeUnsafe(buf.Bytes()))
	return err
}

// text makes s valid UTF-8: every invalid byte becomes one U+FFFD, so
// two different byte strings never collapse into the same text because
// of a run length.
func text(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteRune(utf8.RuneError)
		} else {
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}

// escapeUnsafe rewrites, inside an encoded document, every character
// that encoding/json leaves raw but that can drive a terminal or reorder
// text when someone cats the output: DEL, the C1 controls (U+0080-U+009F,
// which include CSI) and Unicode format characters (bidi overrides,
// zero-width and tag characters). JSON structure is pure ASCII, so these
// can only occur inside strings, where \uXXXX is equivalent.
func escapeUnsafe(in []byte) []byte {
	var out bytes.Buffer
	for i := 0; i < len(in); {
		r, size := utf8.DecodeRune(in[i:])
		if unicode.Is(unicode.Cf, r) || (r >= 0x7f && r <= 0x9f) {
			if r1, r2 := utf16.EncodeRune(r); r1 != unicode.ReplacementChar {
				fmt.Fprintf(&out, "\\u%04x\\u%04x", r1, r2)
			} else {
				fmt.Fprintf(&out, "\\u%04x", r)
			}
		} else {
			out.Write(in[i : i+size])
		}
		i += size
	}
	return out.Bytes()
}

// optional maps the empty string to null: "unknown" and "none" are
// states, not empty values.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	t := text(s)
	return &t
}
