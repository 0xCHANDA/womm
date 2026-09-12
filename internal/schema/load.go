package schema

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrMalformed wraps YAML syntax failures (exit 2 → 3 territory later).
var ErrMalformed = errors.New("womm.yaml is not valid YAML")

// Load reads, parses and validates a womm.yaml file.
//
// It returns the parsed File, the list of warnings (unknown keys, etc.)
// and an error for malformed YAML, version incompatibility or schema
// violations. Warnings never make Load fail.
func Load(path string) (*File, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	return Parse(data)
}

// Parse validates YAML bytes according to schema v1. It does not touch
// the filesystem and never executes anything (PR 1: filesystem reads
// and parsing only).
func Parse(data []byte) (*File, []string, error) {
	var raw yaml.Node
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	if raw.Kind != yaml.DocumentNode || len(raw.Content) == 0 {
		return nil, nil, ErrMalformed
	}
	root := raw.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, nil, ErrMalformed
	}

	warnings := checkUnknownKeys(root)
	version, err := extractVersion(root)
	if err != nil {
		return nil, warnings, err
	}

	var file File
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, warnings, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	file.Version = version

	if err := validate(&file); err != nil {
		return nil, warnings, err
	}
	return &file, warnings, nil
}

// extractVersion inspects the document root and enforces the version
// compatibility policy exactly. Every failure here belongs to the
// ErrVersion family (see versionError.Unwrap).
func extractVersion(root *yaml.Node) (int, error) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, val := root.Content[i], root.Content[i+1]
		if key.Value != "version" {
			continue
		}
		v, err := strconv.Atoi(strings.TrimSpace(val.Value))
		if err != nil {
			// A stated, non-integer version is a version violation
			// too: it belongs to the ErrVersion family.
			return 0, &versionError{present: true, stated: ""}
		}
		if v != Version {
			// v > 1 → unknown future schema; v < 1 → invalid. Same
			// exit code semantics (error), distinct messages.
			return 0, &versionError{present: true, stated: val.Value}
		}
		return v, nil
	}
	return 0, &versionError{present: false}
}

var topLevelKeys = map[string]bool{
	"version": true, "requirements": true,
	"services": true, "environment": true,
}

var requirementKeys = map[string]bool{"name": true, "constraint": true, "evidence": true}
var evidenceKeys = map[string]bool{"source": true, "field": true, "value": true}
var serviceKeys = map[string]bool{"version": true, "port": true}
var environmentKeys = map[string]bool{"required": true, "optional": true}

// checkUnknownKeys produces warnings for unknown keys at the supported
// levels. Unknown keys never fail Load.
func checkUnknownKeys(root *yaml.Node) []string {
	var warnings []string

	warn := func(where string, node *yaml.Node, allowed map[string]bool) {
		for i := 0; i+1 < len(node.Content); i += 2 {
			k := node.Content[i].Value
			if !allowed[k] {
				warnings = append(warnings, fmt.Sprintf(
					"unknown key %q inside %s (ignored; warning only)", k, where))
			}
		}
	}

	warn("top level", root, topLevelKeys)

	for i := 0; i+1 < len(root.Content); i += 2 {
		key, val := root.Content[i], root.Content[i+1]
		switch key.Value {
		case "requirements":
			for i, item := range val.Content {
				if item.Kind != yaml.MappingNode {
					continue
				}
				warn(fmt.Sprintf("requirements entry %d", i), item, requirementKeys)
				for k := 0; k+1 < len(item.Content); k += 2 {
					if item.Content[k].Value == "evidence" {
						for _, ev := range item.Content[k+1].Content {
							if ev.Kind == yaml.MappingNode {
								warn("evidence entry", ev, evidenceKeys)
							}
						}
					}
				}
			}
		case "services":
			for each := 0; each+1 < len(val.Content); each += 2 {
				name, body := val.Content[each].Value, val.Content[each+1]
				if body.Kind == yaml.MappingNode {
					warn(fmt.Sprintf("service %q", name), body, serviceKeys)
				}
			}
		case "environment":
			if val.Kind == yaml.MappingNode {
				warn("environment", val, environmentKeys)
			}
		}
	}
	sort.Strings(warnings)
	return warnings
}
