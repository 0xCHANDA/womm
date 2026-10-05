package probe

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/0xCHANDA/womm/internal/inspect/toolpath"
)

// Env builds a probe environment: an ALLOWLIST. Only locale settings
// (LANG, LANGUAGE, LC_*) are inherited; forced entries (HOME, tool
// specific switches) are added as given; PATH is computed (see PathFor).
// Everything else the parent process has — including every variable a
// launcher (direnv, make, `npm run`) could set to steer a version-manager
// shim (VOLTA_HOME, MISE_*, ASDF_*, XDG_DATA_HOME...) or a toolchain
// (GOFLAGS, GOTOOLCHAIN, GOENV...) — never reaches the probe.
func Env(execPath string, forced []string) []string {
	out := make([]string, 0, len(forced)+4)
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if key == "LANG" || key == "LANGUAGE" || strings.HasPrefix(key, "LC_") {
			out = append(out, kv)
		}
	}
	out = append(out, forced...)
	return append(out, "PATH="+PathFor(execPath))
}

// PathFor is the PATH of a probe: the executable's own directory when it
// is not a system directory, then the system directories. A launcher
// script from a user directory (npm from nvm, fnm or Volta is
// `#!/usr/bin/env node`) then finds its sibling tools there instead of
// silently running on the system ones. The directory has already been
// vetted by the resolver; one that would split into several PATH entries
// is never emitted. System binaries keep exactly the fixed system PATH.
func PathFor(execPath string) string {
	dirs := append([]string(nil), toolpath.SystemDirs...)
	if execPath != "" {
		dir := filepath.Dir(execPath)
		if strings.ContainsAny(dir, ":\x00\n\r") {
			return strings.Join(dirs, string(os.PathListSeparator))
		}
		system := false
		for _, d := range dirs {
			if d == dir {
				system = true
				break
			}
		}
		if !system {
			dirs = append([]string{dir}, dirs...)
		}
	}
	return strings.Join(dirs, string(os.PathListSeparator))
}

// AccountHome is the home directory of the account WOMM runs as, from
// the user database (never $HOME), or a path that does not exist when
// there is none: cached shims then fail fast and are reported as
// unreachable rather than run from an attacker-chosen directory.
func AccountHome() string {
	if h := toolpath.AccountHome(); h != "" {
		return h
	}
	return "/nonexistent"
}
