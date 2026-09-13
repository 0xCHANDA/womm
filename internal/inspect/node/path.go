package node

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// errExecutableNotFound reports that a tool name could not be resolved
// inside the sanitized system search path. It is a sentinel
// distinguishing "not installed" (a legitimate Observation, never an
// error) from a genuine resolution failure.
var errExecutableNotFound = errors.New("executable not found in sanitized system path")

// systemPathDirs is the deterministic, hardcoded allowlist of
// directories NodeInspector will ever resolve executables from. It
// intentionally ignores the process's inherited PATH: an untrusted
// project can shadow system tools by placing entries earlier on PATH
// (./node_modules/.bin, a project-local shim, a bare "."), and WOMM
// must never resolve or execute those. Linux-only for v0.0.1 — extend
// deliberately, with justification, if a later PR adds another OS.
var systemPathDirs = []string{
	"/usr/local/bin",
	"/usr/bin",
	"/bin",
}

// resolveSystemPath resolves name to an absolute executable path using
// ONLY systemPathDirs — never the inherited process PATH, never a
// project-relative or current-directory location, never anything
// derived from caller input beyond the bare name itself.
//
// name must be a bare executable name (no path separator): this
// function maps a known tool name to a system location, it never
// trusts or joins a caller-supplied path. That refusal is also what
// keeps an unsupported/hostile Requirement.Name (e.g. "../../foo",
// "./evil") from ever reaching the filesystem check below.
func resolveSystemPath(name string) (string, error) {
	if name == "" || strings.ContainsRune(name, '/') {
		return "", fmt.Errorf("%w: %q is not a bare executable name", errExecutableNotFound, name)
	}
	for _, dir := range systemPathDirs {
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Mode()&0o111 == 0 {
			// Exists but is not executable by anyone: not a usable
			// system binary, keep looking rather than fail here.
			continue
		}
		return candidate, nil
	}
	return "", fmt.Errorf("%w: %q", errExecutableNotFound, name)
}
