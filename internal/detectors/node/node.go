package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"

	"github.com/0xCHANDA/womm/internal/core"
)

// ErrEvidenceConflict marks incompatible declarations for the same
// requirement name. It must abort detection: WOMM never picks the
// strictest, the newest, or any side of a conflict (--force does not
// belong here). It is a sentinel, not a formatted error.
var ErrEvidenceConflict = errors.New("evidence conflict")

// candNode is a Node requirement candidate awaiting consolidation.
type candNode struct {
	constraint string // normalized constraint ("22.14.0", ">=20 <25")
	source     string
	field      string
	value      string // verbatim declared literal
}

func (c candNode) evidence() core.Evidence {
	return core.Evidence{Source: c.source, Field: c.field, Value: c.value}
}

func (c candNode) asRequirement(name string) core.Requirement {
	return core.Requirement{Name: name, Constraint: c.constraint, Evidence: []core.Evidence{c.evidence()}}
}

// NodeDetector reads the project's explicit Node.js requirements from
// package.json (engines.node) and .nvmrc.
//
// Consolidation policy for v0.0.1 (chosen so the detector can never
// emit a model the schema rejects — duplicate names are invalid):
//
//	engines only              → Requirement{node, engines constraint}
//	.nvmrc only (exact x.y.z) → Requirement{node, exact version}
//	engines + .nvmrc          → engines.Check(exact)?
//	                            yes → ONE Requirement{node, exact version}
//	                                  Evidence: [engines.node, .nvmrc]
//	                            no  → ErrEvidenceConflict
//
// The both-sources result is not "picking .nvmrc": the conjunction
// `Node >=20 AND Node ==22.14.0` is exactly `Node ==22.14.0`.
// The generic range-intersection problem is out of scope for v0.0.1:
// EVIDENCE_CONFLICT means provably incompatible, never "no witness
// found".
//
// L0 only: filesystem reads, JSON parsing, semver checks. No binary
// execution, no PATH introspection.
type NodeDetector struct{}

func NewNodeDetector() NodeDetector { return NodeDetector{} }

func (NodeDetector) Name() string { return "node" }

func (NodeDetector) Detect(_ context.Context, projectRoot string) ([]core.Requirement, error) {
	pkgPath := filepath.Join(projectRoot, "package.json")

	// Source 1: package.json → engines.node
	pkgData, hasPkg, err := readFileIfPresent(projectRoot, "package.json")
	if err != nil {
		return nil, err
	}
	var engines *candNode
	if hasPkg {
		var pkg packageJSON
		if err := json.Unmarshal(pkgData, &pkg); err != nil {
			return nil, fmt.Errorf("%s: malformed package.json (declared source, cannot be interpreted safely): %w", pkgPath, err)
		}
		if enginesNode := strings.TrimSpace(pkg.Engines.Node); enginesNode != "" {
			if !supportsConstraint(enginesNode) {
				return nil, unsupportedConstraint("package.json → engines.node", enginesNode)
			}
			engines = &candNode{constraint: enginesNode, source: "package.json", field: "engines.node", value: pkg.Engines.Node}
		}
	}

	// Source 2: .nvmrc
	nvmData, hasNvm, err := readFileIfPresent(projectRoot, ".nvmrc")
	if err != nil {
		return nil, err
	}
	var nvm *candNode
	if hasNvm {
		c, err := nvmrcCandidate(nvmData)
		if err != nil {
			return nil, err
		}
		if c != nil {
			nvm = c
		}
	}

	switch {
	case engines == nil && nvm == nil:
		return []core.Requirement{}, nil
	case engines == nil:
		return []core.Requirement{nvm.asRequirement("node")}, nil
	case nvm == nil:
		return []core.Requirement{engines.asRequirement("node")}, nil
	}

	// Both sources present: the conjunction rule. EVIDENCE_CONFLICT
	// here is provable incompatibility (Check on concrete versions).
	v, err := semver.NewVersion(nvm.constraint)
	if err != nil {
		return nil, fmt.Errorf(".nvmrc: internal constraint error for %q: %w", nvm.constraint, err)
	}
	ec, err := semver.NewConstraint(engines.constraint)
	if err != nil {
		return nil, unsupportedConstraint("package.json → engines.node", engines.constraint)
	}
	if !ec.Check(v) {
		return nil, fmt.Errorf(
			"%w: node required as %q (by %s) and %q (by %s) cannot both be true; no side is selected — resolve the declarations in the project",
			ErrEvidenceConflict,
			engines.constraint, engines.source+" → "+engines.field,
			nvm.constraint, nvm.source+" → "+nvm.field)
	}
	return []core.Requirement{{
		Name:       "node",
		Constraint: nvm.constraint,
		Evidence:   []core.Evidence{engines.evidence(), nvm.evidence()},
	}}, nil
}

// unsupportedConstraint builds the explicit error for engines.node
// values outside our semver policy: never guessed, never silently
// compatible.
func unsupportedConstraint(source, constraint string) error {
	return fmt.Errorf("%s: unsupported node constraint %q (semver-compatible formats only; 'lts/*'-style values are not interpreted)", source, constraint)
}

// nvmrcCandidate converts .nvmrc content into its single supported
// candidate. Selector semantics live in nvmrc.go; see that file for
// the handled set (comments, KEY=value, exact versions, unsupported
// but valid nvm syntax, multiple selectors).
func nvmrcCandidate(data []byte) (*candNode, error) {
	line, err := nvmrcSignificant(data)
	if err != nil {
		return nil, err
	}
	if line == "" {
		return nil, nil
	}
	version, err := nvmrcSelector(line)
	if err != nil {
		return nil, err
	}
	return &candNode{constraint: version, source: ".nvmrc", field: "version", value: line}, nil
}

// nvmrcSignificant returns the single significant line (non-blank,
// non-comment, non-KEY=value). Multiple real selectors are a file
// error, not a silent first-pick.
func nvmrcSignificant(data []byte) (string, error) {
	var sig []string
	for _, l := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if isKeyValueReserved(t) {
			continue
		}
		if len(sig) == 1 {
			return "", fmt.Errorf(".nvmrc: multiple version selectors (%q and %q); WOMM v0.0.1 supports exactly one explicit version", sig[0], t)
		}
		sig = append(sig, t)
	}
	if len(sig) == 0 {
		return "", nil
	}
	return sig[0], nil
}

// isKeyValueReserved reports lines of the KEY=value form that nvm
// reserves and WOMM ignores for now.
func isKeyValueReserved(t string) bool {
	i := strings.Index(t, "=")
	if i <= 0 {
		return false
	}
	for _, ch := range t[:i] {
		if !(ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9') {
			return false
		}
	}
	return true
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
