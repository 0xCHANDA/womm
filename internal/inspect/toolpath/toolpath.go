// Package toolpath resolves the bare name of a tool ("node") to the
// absolute path of the executable an L1 inspector will start.
//
// It is the single place that decides WHICH binary WOMM executes, and
// its one invariant is the project-code invariant: a project must never
// be able to put an executable into the search order and have WOMM run
// it. Concretely:
//
//   - the inherited process PATH is never consulted (a task runner,
//     direnv or `npm run` can prepend directories to it);
//   - the search list is fixed, ordered and deterministic: directories
//     the user typed explicitly (--tool-dir), in the order given, then
//     the system directories (SystemDirs);
//   - a user-supplied directory must be absolute, must exist, and must
//     not be (or sit under) a project root or a node_modules directory
//     or be world-writable; anything else is refused with a
//     deterministic diagnostic, never skipped silently. A directory
//     owned by someone other than root or the invoking user is the
//     user's call — Node installed by a build user and run as root is
//     the common case — and is accepted with a warning;
//   - a candidate that is a symlink resolving into a project root is
//     refused (ErrUnsafe), never executed and never stepped over;
//   - the executable that is started is the candidate's own path, not
//     its symlink target: version-manager shims (mise, asdf, Volta)
//     dispatch on their own file name.
//
// Resolution reads the filesystem (stat only); it executes nothing.
package toolpath

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ErrNotFound reports that no configured directory holds an executable
// of that name. It is the legitimate machine observation "not
// installed", never an internal failure.
var ErrNotFound = errors.New("executable not found in the configured tool directories")

// ErrUnsafe marks a candidate that exists but must not be executed: it
// resolves into project-controlled space. The tool is neither reported
// absent (it is there) nor stepped over (a later directory would then
// answer for a binary the user never meant): the run is inconclusive.
var ErrUnsafe = errors.New("unsafe executable")

// SystemDirs is the deterministic, hardcoded system allowlist. It
// intentionally ignores the process's inherited PATH. Linux-only —
// extend deliberately, with justification, if another OS is added.
var SystemDirs = []string{
	"/usr/local/bin",
	"/usr/bin",
	"/bin",
}

// Origin says why a directory is in the search list.
type Origin int

const (
	// OriginExplicit: the user named it (--tool-dir).
	OriginExplicit Origin = iota
	// OriginSystem: a fixed system directory.
	OriginSystem
)

func (o Origin) String() string {
	switch o {
	case OriginExplicit:
		return "explicit"
	case OriginSystem:
		return "system"
	}
	return "unknown"
}

// Dir is one entry of the resolved, ordered search list.
type Dir struct {
	// Path is absolute and canonical (symlinks resolved) for user
	// directories; the literal allowlist entry for system ones.
	Path   string
	Origin Origin
}

// DirError is a refused user-supplied directory. Its message is
// deterministic.
type DirError struct {
	Dir    string
	Reason string
}

func (e *DirError) Error() string { return fmt.Sprintf("tool directory %q: %s", e.Dir, e.Reason) }

// Config is everything a Resolver is built from. Nothing in it may
// come from the inspected project: ProjectRoots names what must be
// kept out, ExplicitDirs comes from the invoking user's command line.
type Config struct {
	// ProjectRoots are directories whose content is controlled by the
	// project being inspected. Nothing under them is ever searched or
	// executed. Relative entries are made absolute against the working
	// directory.
	ProjectRoots []string
	// ExplicitDirs are the --tool-dir values, in the order given.
	ExplicitDirs []string
}

// Resolver maps tool names to executables using a validated, ordered
// directory list. It is immutable after New and safe for concurrent
// use.
type Resolver struct {
	dirs     []Dir
	projects []string // lexical and canonical forms of every project root
	warnings []string
}

// System returns a Resolver over the system directories only: the
// behaviour WOMM had before user directories existed.
func System() *Resolver { return &Resolver{dirs: systemDirs()} }

// New validates cfg and returns a Resolver. A refused explicit
// directory is a *DirError (the first, in the order given).
func New(cfg Config) (*Resolver, error) { return build(cfg, os.Geteuid()) }

func build(cfg Config, euid int) (*Resolver, error) {
	r := &Resolver{}
	for _, root := range cfg.ProjectRoots {
		r.projects = appendUnique(r.projects, projectForms(root)...)
	}

	seen := map[string]bool{}
	for _, raw := range cfg.ExplicitDirs {
		real, warning, err := r.validateUserDir(raw, euid)
		if err != nil {
			return nil, err
		}
		if seen[real] {
			continue // duplicates collapse onto their first occurrence
		}
		seen[real] = true
		if warning != "" {
			r.warnings = append(r.warnings, warning)
		}
		r.dirs = append(r.dirs, Dir{Path: real, Origin: OriginExplicit})
	}
	r.dirs = append(r.dirs, systemDirs()...)
	return r, nil
}

func systemDirs() []Dir {
	out := make([]Dir, len(SystemDirs))
	for i, d := range SystemDirs {
		out[i] = Dir{Path: d, Origin: OriginSystem}
	}
	return out
}

// Dirs returns the search list in resolution order.
func (r *Resolver) Dirs() []Dir { return append([]Dir(nil), r.dirs...) }

// Warnings returns the deterministic, ordered notes about accepted but
// noteworthy directories. They never change what is resolved.
func (r *Resolver) Warnings() []string { return append([]string(nil), r.warnings...) }

// validateUserDir returns the canonical form of an accepted user
// directory, an optional warning, or a *DirError.
func (r *Resolver) validateUserDir(raw string, euid int) (real, warning string, err error) {
	refuse := func(format string, a ...any) (string, string, error) {
		return "", "", &DirError{Dir: raw, Reason: fmt.Sprintf(format, a...)}
	}
	if raw == "" || !filepath.IsAbs(raw) {
		return refuse("must be an absolute path (the shell, not WOMM, expands ~ and relative paths)")
	}
	lexical := filepath.Clean(raw)
	if hasNodeModules(lexical) {
		return refuse("is inside a node_modules directory, which is project territory")
	}
	if p, ok := r.insideProject(lexical); ok {
		return refuse("is inside the project (%s); WOMM never executes anything the project controls", p)
	}
	real, err = filepath.EvalSymlinks(lexical)
	if err != nil {
		return refuse("cannot be resolved: %s", reasonOf(err))
	}
	if hasNodeModules(real) {
		return refuse("resolves to %s, inside a node_modules directory", real)
	}
	if p, ok := r.insideProject(real); ok {
		return refuse("resolves to %s, inside the project (%s)", real, p)
	}
	fi, err := os.Stat(real)
	if err != nil {
		return refuse("cannot be read: %s", reasonOf(err))
	}
	if !fi.IsDir() {
		return refuse("is not a directory")
	}
	if fi.Mode().Perm()&0o002 != 0 {
		return refuse("is world-writable: any local user could plant an executable in it")
	}
	uid, ok := ownerOf(fi)
	if !ok {
		return refuse("owner cannot be determined")
	}
	if uid != 0 && uid != uint32(euid) {
		warning = fmt.Sprintf("tool directory %s is owned by uid %d, neither root nor the invoking user (uid %d); that user can replace the executables WOMM runs from it", real, uid, euid)
	}
	return real, warning, nil
}

// Resolve returns the absolute path of the executable that name
// designates, searching the directories in order. name must be a bare
// executable name: this maps a known tool name to a configured
// location, it never trusts or joins a caller-supplied path — which is
// also what keeps a hostile Requirement.Name ("../../foo", "./evil")
// away from the filesystem.
func (r *Resolver) Resolve(name string) (string, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsRune(name, '/') || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("%w: %q is not a bare executable name", ErrNotFound, name)
	}
	for _, d := range r.dirs {
		candidate := filepath.Join(d.Path, name)
		ok, err := r.usable(candidate, d.Origin)
		if err != nil {
			return "", err
		}
		if ok {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrNotFound, name)
}

// usable reports whether candidate is an executable regular file that
// may be started. (false, nil) means "keep looking".
func (r *Resolver) usable(candidate string, origin Origin) (bool, error) {
	info, err := os.Stat(candidate)
	if err != nil {
		if origin == OriginSystem || errors.Is(err, fs.ErrNotExist) && !isDanglingLink(candidate) {
			// Not installed here (system directories: any stat failure
			// keeps the v0.1 "keep looking" behaviour).
			return false, nil
		}
		return false, fmt.Errorf("%s: %s", candidate, reasonOf(err))
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		// A directory, a FIFO or a file nobody may execute is not a
		// usable binary: keep looking rather than fail here.
		return false, nil
	}
	real, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return false, fmt.Errorf("%s: %s", candidate, reasonOf(err))
	}
	if p, ok := r.insideProject(real); ok {
		return false, fmt.Errorf("%w: %s resolves to %s, inside the project (%s)", ErrUnsafe, candidate, real, p)
	}
	return true, nil
}

func isDanglingLink(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// insideProject reports whether path (clean, absolute) is a project
// root or lies under one, comparing against both the lexical and the
// canonical form of every root.
func (r *Resolver) insideProject(path string) (string, bool) {
	for _, p := range r.projects {
		if within(path, p) {
			return p, true
		}
	}
	return "", false
}

func projectForms(root string) []string {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	forms := []string{abs}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		forms = append(forms, real)
	}
	return forms
}

func appendUnique(dst []string, more ...string) []string {
	for _, m := range more {
		dup := false
		for _, d := range dst {
			if d == m {
				dup = true
				break
			}
		}
		if !dup {
			dst = append(dst, m)
		}
	}
	return dst
}

// within reports whether path equals root or lies beneath it. Both
// must be clean and absolute.
func within(path, root string) bool {
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func hasNodeModules(path string) bool {
	for _, c := range strings.Split(path, string(filepath.Separator)) {
		if c == "node_modules" {
			return true
		}
	}
	return false
}

func ownerOf(fi fs.FileInfo) (uint32, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Uid, true
}

// reasonOf trims a *PathError to its cause so diagnostics do not
// repeat the path.
func reasonOf(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}
