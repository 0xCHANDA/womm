package schema

import (
	"errors"
	"strings"
	"testing"
)

func TestParseValid(t *testing.T) {
	y := `
version: 1

requirements:
  - name: node
    constraint: ">=22 <25"
    evidence:
      - source: package.json
        field: engines.node
        value: ">=22"
  - name: pnpm
    constraint: ">=10"
    evidence:
      - source: package.json
        field: packageManager
        value: "pnpm@10.15.1"

services:
  postgres:
    version: ">=17"
    port: 5432

environment:
  required:
    - DATABASE_URL
  optional:
    - DEBUG
`
	f, warnings, err := Parse([]byte(y))
	if err != nil {
		t.Fatalf("valid schema rejected: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if f.Version != 1 {
		t.Errorf("Version = %d, want 1", f.Version)
	}
	if len(f.Requirements) != 2 {
		t.Fatalf("got %d requirements, want 2", len(f.Requirements))
	}
	node := f.Requirements[0]
	if node.Name != "node" || node.Constraint != ">=22 <25" {
		t.Errorf("unexpected first requirement: %+v", node)
	}
	if len(node.Evidence) != 1 || node.Evidence[0].Source != "package.json" ||
		node.Evidence[0].Field != "engines.node" {
		t.Errorf("unexpected evidence: %+v", node.Evidence)
	}
	if f.Services["postgres"].Port != 5432 {
		t.Errorf("postgres port = %d, want 5432", f.Services["postgres"].Port)
	}
	if len(f.Environment.Required) != 1 || f.Environment.Required[0] != "DATABASE_URL" {
		t.Errorf("unexpected environment: %+v", f.Environment)
	}
}

func TestParseInvalid(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{"malformed yaml", "version: 1\nbad yaml [\n", "not valid YAML"},
		{"missing version", "requirements: []\n", "no version field"},
		{"version < 1", "version: 0\n", "schema version 0"},
		{"version > 1", "version: 2\n", "schema version 2"},
		{"non integer version", "version: \"one\"\n", "must be an integer"},
		{
			"requirement missing name",
			"version: 1\nrequirements:\n  - constraint: '>=22'\n    evidence:\n      - source: a\n        field: b\n",
			"name is required",
		},
		{
			"requirement missing constraint",
			"version: 1\nrequirements:\n  - name: node\n    evidence:\n      - source: a\n        field: b\n",
			"constraint is required",
		},
		{
			"requirement missing evidence",
			"version: 1\nrequirements:\n  - name: node\n    constraint: '>=22'\n",
			"at least one evidence entry",
		},
		{
			"evidence list empty (missing entries)",
			"version: 1\nrequirements:\n  - name: node\n    constraint: '>=22'\n    evidence: []\n",
			"at least one evidence entry",
		},
		{
			"evidence entry missing source",
			"version: 1\nrequirements:\n  - name: node\n    constraint: '>=22'\n    evidence:\n      - field: b\n",
			"source is required",
		},
		{
			"evidence entry missing field",
			"version: 1\nrequirements:\n  - name: node\n    constraint: '>=22'\n    evidence:\n      - source: pkg.json\n",
			"field is required",
		},
		{
			"duplicate requirement names",
			"version: 1\nrequirements:\n  - name: node\n    constraint: '>=22'\n    evidence:\n      - source: a\n        field: b\n  - name: node\n    constraint: '>=20'\n    evidence:\n      - source: c\n        field: d\n",
			"duplicate name",
		},
		{
			"service missing version",
			"version: 1\nservices:\n  postgres:\n    port: 5432\n",
			"version is required",
		},
		{
			"non mapping root",
			"- list\n- not a mapping\n",
			"not valid YAML",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Parse([]byte(tc.yaml))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want containing %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestParseUnknownKeysWarningNotError(t *testing.T) {
	y := `
version: 1
future: metadata
requirements:
  - name: node
    constraint: ">=20"
    evidence:
      - source: package.json
        field: engines.node
        value: ">=20"
      - source: toml.lock
        field: engines.node
        value: ">=20"
        unknown: thing
services:
  postgres:
    version: ">=13"
    foo: bar
environment:
  required: []
  extra: 1
`
	f, warnings, err := Parse([]byte(y))
	if err != nil {
		t.Fatalf("unknown keys must not fail: %v", err)
	}
	if f == nil || len(f.Requirements) != 1 {
		t.Fatalf("file data lost due to unknown keys")
	}
	if len(warnings) == 0 {
		t.Fatal("expected at least one warning")
	}
	for _, w := range warnings {
		if !strings.Contains(w, "unknown key") {
			t.Errorf("warning %q does not mention unknown key", w)
		}
	}
	// warnings must include links per section
	found := map[string]bool{}
	for _, w := range warnings {
		for _, place := range []string{"top level", "evidence entry", `service "postgres"`, "environment"} {
			if strings.Contains(w, place) {
				found[place] = true
			}
		}
	}
	if len(found) != 4 {
		t.Errorf("missing sections in warnings: %v", warnings)
	}
}

func TestVersionErrorPolicy(t *testing.T) {
	// Unknown future version must be an error of the ErrVersion family,
	// not silently accepted and not a generic failure.
	_, _, err := Parse([]byte("version: 99\n"))
	var ve *versionError
	if err == nil || !errors.As(err, &ve) {
		t.Fatalf("expected *versionError, got %v", err)
	}
	if ve.got != 99 {
		t.Errorf("versionError.got = %d, want 99", ve.got)
	}
	if !strings.Contains(err.Error(), "Please upgrade womm") {
		t.Errorf("error must tell the user to upgrade: %v", err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, _, err := Load("/nonexistent/path/womm.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
