package schema

import (
	"bytes"
	"strings"
	"testing"

	"github.com/0xCHANDA/womm/internal/core"
)

func sampleFile() *File {
	return &File{
		Version: Version,
		Requirements: []core.Requirement{
			{Name: "node", Constraint: ">=22 <25", Evidence: []core.Evidence{
				{Source: "package.json", Field: "engines.node", Value: ">=22 <25"},
				{Source: ".nvmrc", Field: "version", Value: "v24.7.0"},
			}},
			{Name: "pnpm", Constraint: "10.15.1", Evidence: []core.Evidence{
				{Source: "package.json", Field: "packageManager", Value: "pnpm@10.15.1"},
			}},
		},
	}
}

const wantSampleYAML = `version: 1
requirements:
  - name: node
    constraint: '>=22 <25'
    evidence:
      - source: package.json
        field: engines.node
        value: '>=22 <25'
      - source: .nvmrc
        field: version
        value: v24.7.0
  - name: pnpm
    constraint: 10.15.1
    evidence:
      - source: package.json
        field: packageManager
        value: pnpm@10.15.1
`

func TestMarshalGolden(t *testing.T) {
	got, err := Marshal(sampleFile())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != wantSampleYAML {
		t.Errorf("Marshal output mismatch\n--- got ---\n%s--- want ---\n%s", got, wantSampleYAML)
	}
	if !bytes.HasSuffix(got, []byte("\n")) || bytes.HasSuffix(got, []byte("\n\n")) {
		t.Errorf("output must end with exactly one newline: %q", got)
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	in := sampleFile()
	data, err := Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	out, warnings, err := Parse(data)
	if err != nil {
		t.Fatalf("Marshal produced a document Parse rejects: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("Marshal produced unknown keys: %v", warnings)
	}
	if out.Version != in.Version || len(out.Requirements) != len(in.Requirements) {
		t.Fatalf("round trip changed the file: %+v", out)
	}
	for i := range in.Requirements {
		a, b := in.Requirements[i], out.Requirements[i]
		if a.Name != b.Name || a.Constraint != b.Constraint || len(a.Evidence) != len(b.Evidence) {
			t.Errorf("requirement %d changed: %+v vs %+v", i, a, b)
		}
		for j := range a.Evidence {
			if a.Evidence[j] != b.Evidence[j] {
				t.Errorf("evidence %d/%d changed: %+v vs %+v", i, j, a.Evidence[j], b.Evidence[j])
			}
		}
	}
	again, err := Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, data) {
		t.Errorf("Marshal(Parse(Marshal(f))) != Marshal(f):\n%s\n---\n%s", again, data)
	}
}

func TestMarshalEmptyRequirementsIsExplicit(t *testing.T) {
	for _, reqs := range [][]core.Requirement{nil, {}} {
		got, err := Marshal(&File{Version: Version, Requirements: reqs})
		if err != nil {
			t.Fatal(err)
		}
		if want := "version: 1\nrequirements: []\n"; string(got) != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestMarshalOptionalSectionsAndOrdering(t *testing.T) {
	f := &File{
		Version:      Version,
		Requirements: []core.Requirement{{Name: "node", Constraint: "present", Evidence: []core.Evidence{{Source: "package.json", Field: "engines.node"}}}},
		Services: map[string]Service{
			"redis":    {Version: ">=7"},
			"postgres": {Version: ">=17", Port: 5432},
		},
		Environment: Environment{Required: []string{"DATABASE_URL"}},
	}
	got, err := Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	want := `version: 1
requirements:
  - name: node
    constraint: present
    evidence:
      - source: package.json
        field: engines.node
        value: ""
services:
  postgres:
    version: '>=17'
    port: 5432
  redis:
    version: '>=7'
environment:
  required:
    - DATABASE_URL
`
	if string(got) != want {
		t.Errorf("Marshal output mismatch\n--- got ---\n%s--- want ---\n%s", got, want)
	}
	if _, _, err := Parse(got); err != nil {
		t.Fatalf("round trip rejected: %v", err)
	}
}

func TestMarshalRefusesInvalidFiles(t *testing.T) {
	cases := map[string]*File{
		"nil":                nil,
		"bad version":        {Version: 2},
		"missing evidence":   {Version: Version, Requirements: []core.Requirement{{Name: "node", Constraint: ">=22"}}},
		"empty name":         {Version: Version, Requirements: []core.Requirement{{Constraint: ">=22", Evidence: []core.Evidence{{Source: "a", Field: "b"}}}}},
		"duplicate name":     {Version: Version, Requirements: []core.Requirement{{Name: "node", Constraint: ">=22", Evidence: []core.Evidence{{Source: "a", Field: "b"}}}, {Name: "node", Constraint: ">=23", Evidence: []core.Evidence{{Source: "a", Field: "b"}}}}},
		"service no version": {Version: Version, Services: map[string]Service{"redis": {}}},
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Marshal(f)
			if err == nil {
				t.Fatalf("invalid file serialized: %s", got)
			}
			if !strings.Contains(err.Error(), "refusing to serialize") {
				t.Errorf("error should say it refused: %v", err)
			}
		})
	}
}

func TestMarshalIsDeterministic(t *testing.T) {
	a, _ := Marshal(sampleFile())
	b, _ := Marshal(sampleFile())
	if !bytes.Equal(a, b) {
		t.Errorf("two Marshal calls differ:\n%s\n---\n%s", a, b)
	}
}
