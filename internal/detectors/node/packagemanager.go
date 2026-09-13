package node

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
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

// corepackHashAlgorithms is the explicit set of Corepack integrity-hash
// algorithms WOMM v0.0.1 supports, mapped to the exact hex digest length
// each algorithm produces.
//
// The subset is justified by direct evidence in the current Corepack
// sources (nodejs/corepack, main):
//
//   - sha1 and sha512 are emitted by Corepack itself:
//     sources/npmRegistryUtils.ts (fetchLatestStableVersion) builds
//     `${version}+sha512.<hex>` from the registry integrity field and
//     falls back to `${version}+sha1.${shasum}`; real-world descriptors
//     such as `pnpm@10.15.1+sha1.<hex>` and `npm@<version>+sha1.<hex>`
//     use the sha1 form.
//   - sha224 is the documented example in the Corepack README
//     (`yarn@3.2.3+sha224.953c8233…`).
//   - sha256 and sha384 have NO direct evidence as packageManager
//     checksum forms in Corepack sources or docs (the SHA256 in
//     verifySignature is npm registry signature verification, not a
//     descriptor hash), so they are NOT claimed as supported in
//     v0.0.1. WOMM represents what we know, not what probably works.
//
// `+garbage` suffixes, unknown algorithms and wrong-length digests are
// all explicit errors, never silently stripped: they are not syntax we
// support, so guessing them away would corrupt the declared toolchain
// identity.
var corepackHashAlgorithms = map[string]int{
	"sha1":   40,
	"sha224": 56,
	"sha512": 128,
}

var corepackHexDigest = regexp.MustCompile(`^[0-9a-f]+$`)

// validateCorepackHash validates a Corepack integrity hash WITHOUT the
// leading "+" (e.g. `sha1.0123…`): the algorithm must be in the
// supported v0.0.1 subset and the digest must be lowercase hex of the
// exact length that algorithm produces.
func validateCorepackHash(hash string) error {
	algo, digest, ok := strings.Cut(hash, ".")
	if !ok || algo == "" || digest == "" {
		return fmt.Errorf("integrity hash %q is not a supported Corepack checksum form (expected +<algo>.<hex>; supported algorithms: sha1, sha224, sha512)", hash)
	}
	wantLen, supported := corepackHashAlgorithms[algo]
	if !supported {
		return fmt.Errorf("integrity hash algorithm %q is not supported by WOMM v0.0.1 (supported: sha1, sha224, sha512)", algo)
	}
	if !corepackHexDigest.MatchString(digest) {
		return fmt.Errorf("integrity hash digest %q must be lowercase hexadecimal", digest)
	}
	if len(digest) != wantLen {
		return fmt.Errorf("integrity hash digest for %s must be exactly %d hex characters, got %d", algo, wantLen, len(digest))
	}
	return nil
}

// splitPackageManager parses "<name>@<version>" and validates the
// version as an exact semantic version.
//
//   - STRICT exact semantics: pnpm@10, pnpm@10.15 and pnpm@v10.15.1
//     are explicit errors — no coercion to 10.0.0/10.15.0 (that
//     changed version identity must be chosen by the project, not
//     invented by WOMM). Pre-release exact versions
//     (yarn@4.0.0-rc.1) are valid.
//   - Corepack integrity hashes ("+sha…") are scaffolding for the
//     artifact, not part of the runtime version: the hash is
//     validated (see validateCorepackHash) and stripped from the
//     returned constraint while the caller keeps the full literal as
//     Evidence.Value. Unknown algorithms, malformed hashes or
//     wrong-length digests fail: they are not a supported Corepack
//     form.
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
	// Separate a Corepack integrity hash, validating algorithm and
	// digest length; the remainder must be an exact semver.
	base := version
	if idx := strings.Index(version, "+"); idx >= 0 {
		if err := validateCorepackHash(version[idx+1:]); err != nil {
			return "", "", err
		}
		base = version[:idx]
	}
	v, err := semver.StrictNewVersion(base)
	if err != nil {
		return "", "", fmt.Errorf("version %q must be an exact version (strict semver; short forms like 10 or 10.15 are not coerced, ranges and tags are not accepted)", version)
	}
	// The constraint is the pin WOMM can verify on a machine; canonical
	// form keeps determinism.
	return name, v.String(), nil
}
