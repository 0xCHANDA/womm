package schema

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/0xCHANDA/womm/internal/core"
)

// Marshal serializes f as a schema v1 womm.yaml document.
//
// The output is a pure function of f: same File, same bytes. Keys are
// emitted in declaration order, sequences in the order they hold,
// maps (services) sorted by key, two-space indentation, one trailing
// newline. Empty optional sections are omitted; `requirements` is
// always present (`[]` when empty) so a captured file states
// explicitly that nothing was found.
//
// f is validated first: Marshal never writes a document that Parse
// would reject.
func Marshal(f *File) ([]byte, error) {
	if err := Validate(f); err != nil {
		return nil, fmt.Errorf("refusing to serialize an invalid womm.yaml: %w", err)
	}
	out := *f
	if out.Requirements == nil {
		out.Requirements = []core.Requirement{}
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&out); err != nil {
		return nil, fmt.Errorf("cannot serialize womm.yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("cannot serialize womm.yaml: %w", err)
	}
	return buf.Bytes(), nil
}
