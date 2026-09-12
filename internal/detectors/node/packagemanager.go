package node

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/0xCHANDA/womm/internal/core"
)

// packageManagerNames are the supported values of the declared
// package manager. Other values are an explicit error, not a silent
// pass-through: an uncontrolled name would produce a requirement WOMM
// cannot justify.
var packageManagerNames = map[string]bool{
	"npm": true, "pnpm": true, "yarn": true,
}

// PackageManagerDetector reads the explicit package.json
// packageManager declaration. Lockfiles (package-lock.json,
// pnpm-lock.yaml, yarn.lock) are NOT evidence at this stage —
// explicit evidence only.
//
// "pnpm@10.15.1" produces:
//
//	name: pnpm
//	constraint: 10.15.1
//	evidence:
//	  source: package.json
//	  field: packageManager
//	  value: pnpm@10.15.1   (literal, preserved verbatim)
type PackageManagerDetector struct{}

func NewPackageManagerDetector() PackageManagerDetector { return PackageManagerDetector{} }

func (PackageManagerDetector) Name() string { return "package-manager" }

func (d PackageManagerDetector) Detect(_ context.Context, projectRoot string) ([]core.Requirement, error) {
	pkgPath := filepath.Join(projectRoot, "package.json")
	pkgData, hasPkg, err := readFileIfPresent(pkgPath)
	if err != nil {
		return nil, err
	}
	if !hasPkg {
		// Without package.json there is no explicit package package
		// manager evidence. Lockfile-only projects are not covered
		// yet (inference absent by design).
		return []core.Requirement{}, nil
	}
	var pkg packageJSON
	if err := json.Unmarshal(pkgData, &pkg); err != nil {
		return nil, fmt.Errorf("%s: malformed package.json (declared source, cannot be interpreted safely): %w", pkgPath, err)
	}
	pm := strings.TrimSpace(pkg.PackageManager)
	if pm == "" {
		return []core.Requirement{}, nil
	}

	name, version, err := splitPackageManager(pm)
	if err != nil {
		return nil, fmt.Errorf("package.json → packageManager %q: %w", pm, err)
	}
	return []core.Requirement{{
		Name:       name,
		Constraint: version,
		Evidence: []core.Evidence{{
			Source: "package.json",
			Field:  "packageManager",
			Value:  pm,
		}},
	}}, nil
}

// splitPackageManager parses "<name>@<version>". Scoped names are
// unsupported at this stage; unknown names produce an explicit
// unsupported error because they cannot be justified as requirements.
func splitPackageManager(pm string) (string, string, error) {
	name, version, ok := strings.Cut(pm, "@")
	if !ok || name == "" || version == "" {
		return "", "", fmt.Errorf("invalid format: expected \"<name>@<version>\" (npm/pnpm/yarn)")
	}
	if !packageManagerNames[name] {
		return "", "", fmt.Errorf("unsupported package manager %q (supported: npm, pnpm, yarn)", name)
	}
	return name, version, nil
}
