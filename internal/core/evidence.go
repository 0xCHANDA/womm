package core

// Evidence links a requirement to its provenance: the exact source
// file and field that justifies it.
type Evidence struct {
	// Relative path of the source file ("package.json").
	Source string `yaml:"source"`
	// Field path inside the source ("engines.node").
	Field string `yaml:"field"`
	// Literal value declared by the field (">=22").
	Value string `yaml:"value"`
}
