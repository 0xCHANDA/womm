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
// Accepted shapes:
//
//	npm@11.2.0          → Requirement{npm, 11.2.0}
//	pnpm@10.15.1        → Requirement{pnpm, 10.15.1}
//	yarn@3.2.3+sha224.… → Requirement{yarn, 3.2.3}   (Corepack hash
//	                      is integrity metadata, not the runtime
//	                      version; Evidence.Value keeps the FULL
//	                      literal)
//
// Rejected with explicit errors:
//
//	pnpm@garbage, yarn@ (empty), npm@latest (ranges/tags not machine-
//	comparable yet), corepack@… (unsupported name), yarn@https://…
//	(Corepack URLs unsupported).
type PackageManagerDetector struct{}

func NewPackageManagerDetector() PackageManagerDetector { return PackageManagerDetector{} }

func (PackageManagerDetector) Name() string { return "package-manager" }

func (PackageManagerDetector) Detect(_ context.Context, projectRoot string) ([]core.Requirement, error) {
	path := filepath.Join(projectRoot, "package.json")
	pkgData, hasPkg, err := readFileIfPresent(projectRoot, "package.json")
	if err != nil {
		return nil, err
	}
	if !hasPkg {
		// Without package.json there is no explicit package manager
		// evidence. Lockfile-only projects are not covered yet
		// (inference absent by design).
		return []core.Requirement{}, nil
	}
	var pkg packageJSON
	if err := json.Unmarshal(pkgData, &pkg); err != nil {
		return nil, fmt.Errorf("%s: malformed package.json (declared source, cannot be interpreted safely): %w", path, err)
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
			Value:  pm, // verbatim, including any +sha hash
		}},
	}}, nil
}

// splitPackageManager parses "<name>@<version>" and validates the
// version as an exact machine-comparable version.
//
//   - Ranges and tags are rejected: packageManager declares a pinned
//     toolchain version, not a selectable range. ("npm@latest",
//     "pnpm@garbage", "yarn@")
//   - Corepack integrity hashes ("+sha…") are scaffolding for the
//     artifact, not part of the runtime version: the hash is stripped
//     from the returned constraint while the caller keeps the full
//     literal as Evidence.Value.
//   - Corepack URLs (yarn@https://…) are unsupported at this stage.
func splitPackageManager(pm string) (string, string, error) {
	if strings.Contains(pm, "://") {
		return "", "", fmt.Errorf("corepack URLs are not supported by WOMM v0.0.1")
	}
	name, version, ok := strings.Cut(pm, "@")
	if !ok || name == "" || version == "" {
		return "", "", fmt.Errorf("invalid format: expected \"<name>@<version>\" (npm/pnpm/yarn)")
	}
	if !packageManagerNames[name] {
		return "", "", fmt.Errorf("unsupported package manager %q (supported: npm, pnpm, yarn)", name)
	}
	// Strip Corepack integrity hash; validate what remains as an
	// exact version.
	base := version
	if plus := strings.Index(version, "+"); plus >= 0 {
		base = version[:plus]
	}
	v, err := semver.NewVersion(base)
	if err != nil {
		return "", "", fmt.Errorf("version %q must be an exact version (ranges and tags are not accepted); expected \"<name>@<version>\"", version)
	}
	// The constraint is the pin WOMM can verify on a machine; canonical
	// form of the base version keeps determinism.
	return name, v.String(), nil
}
