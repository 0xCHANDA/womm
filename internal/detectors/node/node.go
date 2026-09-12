package node

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"

	"github.com/0xCHANDA/womm/internal/core"
)

// ErrEvidenceConflict marks incompatible declarations for the same
// requirement name. It must abort detection: WOMM never picks the
// strictest, the newest, or any side of a conflict (--force does not
// belong here).
var ErrEvidenceConflict = fmt.Errorf("evidence conflict")

// candNode is a Node requirement candidate awaiting consolidation.
type candNode struct {
	constraint string
	source     string
	field      string
	value      string
}

func (c candNode) evidence() core.Evidence {
	return core.Evidence{Source: c.source, Field: c.field, Value: c.value}
}

// NodeDetector reads the project's explicit Node.js requirements from
// package.json (engines.node) and .nvmrc.
//
// Detection semantics:
//
//   - Each evidence source produces a candidate with the literal
//     value preserved in Evidence.Value.
//   - Candidates sharing identical constraint strings merge into one
//     requirement carrying the union of evidence (the constraint is
//     the same string both sources declared: no selection occurred).
//   - Candidates with different constraints are checked for semantic
//     compatibility. Incompatible same-name requirements abort
//     detection with ErrEvidenceConflict — no picking, no averaging,
//     no strict-side preference.
//   - Compatible distinct constraints stay separate requirements;
//     collapsing them into a synthetic range would invent semantics
//     the project never declared.
//
// L0 only: filesystem reads, JSON parsing, semver analysis. No binary
// execution, no PATH introspection.
type NodeDetector struct{}

func NewNodeDetector() NodeDetector { return NodeDetector{} }

func (NodeDetector) Name() string { return "node" }

func (NodeDetector) Detect(_ context.Context, projectRoot string) ([]core.Requirement, error) {
	var cands []candNode

	// Source 1: package.json → engines.node
	pkgPath := filepath.Join(projectRoot, "package.json")
	pkgData, hasPkg, err := readFileIfPresent(pkgPath)
	if err != nil {
		return nil, err
	}
	if hasPkg {
		var pkg packageJSON
		if err := json.Unmarshal(pkgData, &pkg); err != nil {
			return nil, fmt.Errorf("%s: malformed package.json (declared source, cannot be interpreted safely): %w", pkgPath, err)
		}
		if enginesNode := strings.TrimSpace(pkg.Engines.Node); enginesNode != "" {
			if !supportsConstraint(enginesNode) {
				return nil, unsupportedConstraint("package.json → engines.node", enginesNode)
			}
			cands = append(cands, candNode{
				constraint: enginesNode, source: "package.json",
				field: "engines.node", value: pkg.Engines.Node,
			})
		}
	}

	// Source 2: .nvmrc
	nvmData, hasNvm, err := readFileIfPresent(filepath.Join(projectRoot, ".nvmrc"))
	if err != nil {
		return nil, err
	}
	if hasNvm {
		c, err := nvmrcCandidate(nvmData)
		if err != nil {
			return nil, err
		}
		if c != nil {
			cands = append(cands, *c)
		}
	}

	return consolidate(cands)
}

// nvmrcCandidate converts .nvmrc content into a candidate.
//
// File handling policy:
//
//	missing / whitespace-only   → no candidate, no error (absence of
//	                              requirement is not an error)
//	"22.14.0" / "v22.14.0" /
//	"22.14" / "22"              → exact version constraint
//	"lts/*", "lts/iron", "node",
//	uninterpretable garbage     → explicit error (never guessed, never
//	                              a silent warning)
//
// The first non-empty line is authoritative (nvm semantics);
// whitespace and trailing newlines are noise.
func nvmrcCandidate(data []byte) (*candNode, error) {
	line := nvmrcLine(data)
	if line == "" {
		return nil, nil
	}
	version := strings.TrimPrefix(line, "v")
	if _, err := semver.NewVersion(version); err != nil {
		return nil, fmt.Errorf(".nvmrc: cannot interpret %q as a Node version (lts-style values are not supported yet): %w", line, err)
	}
	return &candNode{constraint: version, source: ".nvmrc", field: "version", value: line}, nil
}

func nvmrcLine(data []byte) string {
	for _, l := range strings.Split(string(data), "\n") {
		if t := strings.TrimSpace(l); t != "" {
			return t
		}
	}
	return ""
}

// consolidate merges candidates and enforces the conflict policy.
func consolidate(cands []candNode) ([]core.Requirement, error) {
	type group struct {
		constraint string
		evidence   []core.Evidence
	}
	var groups []group
	for _, c := range cands {
		merged := false
		for i := range groups {
			if groups[i].constraint == c.constraint {
				groups[i].evidence = append(groups[i].evidence, c.evidence())
				merged = true
				break
			}
		}
		if !merged {
			groups = append(groups, group{constraint: c.constraint, evidence: []core.Evidence{c.evidence()}})
		}
	}

	// Semantic compatibility across distinct constraints of the same
	// requirement name. Any conflict aborts detection: EVIDENCE_
	// CONFLICT is a failure, never a silent pick.
	for i := 0; i < len(groups); i++ {
		for j := i + 1; j < len(groups); j++ {
			ok, err := compatible(groups[i].constraint, groups[j].constraint)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, fmt.Errorf("%w: node required as %q (by %s) and %q (by %s) cannot both be true; no side is selected — resolve the declarations in the project",
					ErrEvidenceConflict,
					groups[i].constraint,
					evidenceRef(groups[i].evidence),
					groups[j].constraint,
					evidenceRef(groups[j].evidence))
			}
		}
	}

	out := make([]core.Requirement, 0, len(groups))
	for _, g := range groups {
		out = append(out, core.Requirement{
			Name:       "node",
			Constraint: g.constraint,
			Evidence:   g.evidence,
		})
	}
	return out, nil
}

// evidenceRef renders a deterministic reference of the evidence
// backing one side of a conflict.
func evidenceRef(evs []core.Evidence) string {
	var parts []string
	for _, e := range evs {
		parts = append(parts, e.Source+" → "+e.Field)
	}
	return strings.Join(parts, ", ")
}

// packageJSON is the minimal declarative surface read from
// package.json. Only explicit fields are read; unknown fields are
// ignored by design.
type packageJSON struct {
	Engines struct {
		Node string `json:"node"`
	} `json:"engines"`
	PackageManager string `json:"packageManager"`
}
