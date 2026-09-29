package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"

	"github.com/0xCHANDA/womm/internal/core"
	"github.com/0xCHANDA/womm/internal/semverrange"
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
// Consolidation policy for v0.1 (chosen so the detector can never
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
// The generic range-intersection problem is out of scope for v0.1:
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
		pkg, err := parsePackageJSON(pkgPath, pkgData)
		if err != nil {
			return nil, err
		}
		if enginesNode := strings.TrimSpace(pkg.Engines.Node); enginesNode != "" {
			normalized, err := semverrange.Normalize(enginesNode)
			if err != nil {
				return nil, unsupportedConstraint("package.json → engines.node", enginesNode)
			}
			engines = &candNode{constraint: normalized, source: "package.json", field: "engines.node", value: pkg.Engines.Node}
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
	ec, err := semverrange.Parse(engines.constraint)
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
	return fmt.Errorf("%s: unsupported node constraint %q (npm semver range syntax only; 'lts/*'-style values, comma-separated sets and '!=' are not interpreted)", source, constraint)
}

// nvmrcCandidate converts .nvmrc content into its single supported
// candidate, or an explicit error: an existing .nvmrc always declares
// something or is invalid (nvm's rule, and ours). Line semantics live
// in nvmrcSignificant, selector semantics in nvmrc.go.
func nvmrcCandidate(data []byte) (*candNode, error) {
	line, err := nvmrcSignificant(data)
	if err != nil {
		return nil, err
	}
	version, err := nvmrcSelector(line)
	if err != nil {
		return nil, err
	}
	return &candNode{constraint: version, source: ".nvmrc", field: "version", value: line}, nil
}

// nvmrcSignificant returns the single version selector line of a
// .nvmrc, following nvm's own reader (nvm_process_nvmrc_content):
//
//   - "#" starts a comment anywhere in a line; blank lines are ignored;
//   - a file with no remaining line is INVALID for nvm — and a declared
//     source that declares nothing is an explicit error here too, never
//     silent absence;
//   - KEY=value lines are nvm settings: the key "node" is rejected by
//     nvm (a version is not a setting), a duplicated key is rejected,
//     other keys are ignored;
//   - exactly one bare selector line must remain. Several are a file
//     error, not a silent first-pick.
func nvmrcSignificant(data []byte) (string, error) {
	var sig []string
	seenKeys := map[string]bool{}
	for _, l := range strings.Split(string(data), "\n") {
		if i := strings.IndexByte(l, '#'); i >= 0 {
			l = l[:i]
		}
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if key, ok := nvmrcKey(t); ok {
			if key == "node" {
				return "", fmt.Errorf(".nvmrc: %q is invalid for nvm (the version must be a bare line, not a node= setting); refusing to interpret it", t)
			}
			if seenKeys[key] {
				return "", fmt.Errorf(".nvmrc: setting %q appears more than once, which nvm rejects; refusing to interpret it", key)
			}
			seenKeys[key] = true
			continue
		}
		if len(sig) == 1 {
			return "", fmt.Errorf(".nvmrc: multiple version selectors (%q and %q); WOMM v0.1 supports exactly one explicit version", sig[0], t)
		}
		sig = append(sig, t)
	}
	if len(sig) == 0 {
		return "", fmt.Errorf(".nvmrc exists but declares no version (nvm rejects such a file too); add an explicit x.y.z version or remove the file")
	}
	return sig[0], nil
}

// nvmrcKey reports whether t is a KEY=value settings line and returns
// the trimmed key.
func nvmrcKey(t string) (string, bool) {
	i := strings.Index(t, "=")
	if i <= 0 {
		return "", false
	}
	key := strings.TrimSpace(t[:i])
	if key == "" {
		return "", false
	}
	for _, ch := range key {
		if !(ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9') {
			return "", false
		}
	}
	return key, true
}

// utf8BOM is the byte-order mark some editors prepend to JSON files.
// npm's own package.json reader strips it; so does WOMM, explicitly
// and only this exact prefix — everything else must be strict JSON.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// parsePackageJSON decodes the declarative surface of package.json.
// Any syntax problem is an explicit error: a declared source that
// cannot be interpreted is never treated as absent.
func parsePackageJSON(path string, data []byte) (packageJSON, error) {
	var pkg packageJSON
	data = bytes.TrimPrefix(data, utf8BOM)
	if err := json.Unmarshal(data, &pkg); err != nil {
		return packageJSON{}, fmt.Errorf("%s: malformed package.json (declared source, cannot be interpreted safely): %w", path, err)
	}
	return pkg, nil
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
