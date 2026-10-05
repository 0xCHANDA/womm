package toolpath

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// canon returns the canonical form of a test directory: t.TempDir may
// itself sit behind a symlink (macOS-style /var → /private/var), and
// Resolver paths are canonical.
func canon(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func tempDir(t *testing.T) string { return canon(t, t.TempDir()) }

func writeExe(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho v1.0.0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// withSystemDirs swaps the system allowlist for test directories (the
// real ones cannot be written to in a test). Only the allowlist target
// changes, never the resolution logic being exercised.
func withSystemDirs(t *testing.T, dirs ...string) {
	t.Helper()
	orig := SystemDirs
	SystemDirs = dirs
	t.Cleanup(func() { SystemDirs = orig })
}

func newOrFatal(t *testing.T, cfg Config) *Resolver {
	t.Helper()
	r, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

func dirErr(t *testing.T, cfg Config, wantReason string) {
	t.Helper()
	_, err := New(cfg)
	var de *DirError
	if !errors.As(err, &de) {
		t.Fatalf("New(%+v) error = %v, want *DirError", cfg, err)
	}
	if !strings.Contains(de.Reason, wantReason) {
		t.Errorf("reason %q does not contain %q", de.Reason, wantReason)
	}
}

func TestSystemOnlyResolvesFromSystemDirsInOrder(t *testing.T) {
	a, b := tempDir(t), tempDir(t)
	withSystemDirs(t, a, b)
	writeExe(t, b, "node")
	if got, err := System().Resolve("node"); err != nil || got != filepath.Join(b, "node") {
		t.Fatalf("Resolve = %q, %v; want the second dir", got, err)
	}
	want := writeExe(t, a, "node")
	if got, _ := System().Resolve("node"); got != want {
		t.Errorf("Resolve = %q, want the first system dir %q", got, want)
	}
}

func TestResolveIgnoresInheritedPATH(t *testing.T) {
	sys, evil := tempDir(t), tempDir(t)
	withSystemDirs(t, sys)
	writeExe(t, evil, "node")
	t.Setenv("PATH", evil+string(os.PathListSeparator)+os.Getenv("PATH"))
	if got, err := System().Resolve("node"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve = %q, %v; an inherited PATH entry must never be searched", got, err)
	}
}

func TestResolveRejectsPathLikeNames(t *testing.T) {
	sys := tempDir(t)
	withSystemDirs(t, sys)
	writeExe(t, sys, "node")
	for _, name := range []string{"", ".", "..", "../node", "./node", "a/b", "/etc/passwd", "no\x00de"} {
		if got, err := System().Resolve(name); !errors.Is(err, ErrNotFound) {
			t.Errorf("Resolve(%q) = %q, %v; want ErrNotFound", name, got, err)
		}
	}
}

// Policy: an explicit directory is the user's explicit instruction and
// is consulted BEFORE the system directories. If it were a fallback, a
// system node (older or newer) would silently answer for the binary the
// user pointed at, and the verdict would be about the wrong tool — a
// false PASS relative to what the user actually runs.
func TestExplicitDirsComeBeforeSystemDirs(t *testing.T) {
	sys, user := tempDir(t), tempDir(t)
	withSystemDirs(t, sys)
	writeExe(t, sys, "node")
	want := writeExe(t, user, "node")

	r := newOrFatal(t, Config{ExplicitDirs: []string{user}})
	if got, err := r.Resolve("node"); err != nil || got != want {
		t.Fatalf("Resolve = %q, %v; want the explicit dir's %q", got, err, want)
	}
	// A tool the explicit directory lacks still falls through to the system.
	sysNpm := writeExe(t, sys, "npm")
	if got, err := r.Resolve("npm"); err != nil || got != sysNpm {
		t.Errorf("Resolve(npm) = %q, %v; want the system fallback %q", got, err, sysNpm)
	}
	dirs := r.Dirs()
	if len(dirs) != 2 || dirs[0].Origin != OriginExplicit || dirs[1].Origin != OriginSystem {
		t.Errorf("Dirs = %+v, want [explicit, system]", dirs)
	}
}

func TestMultipleExplicitDirsKeepGivenOrder(t *testing.T) {
	sys, d1, d2, d3 := tempDir(t), tempDir(t), tempDir(t), tempDir(t)
	withSystemDirs(t, sys)
	writeExe(t, d2, "node")
	writeExe(t, d3, "node")
	r := newOrFatal(t, Config{ExplicitDirs: []string{d1, d2, d3}})
	if got, _ := r.Resolve("node"); got != filepath.Join(d2, "node") {
		t.Errorf("Resolve = %q, want the first directory that has it (%s)", got, d2)
	}
	r = newOrFatal(t, Config{ExplicitDirs: []string{d3, d2, d1}})
	if got, _ := r.Resolve("node"); got != filepath.Join(d3, "node") {
		t.Errorf("reversed order: Resolve = %q, want %s", got, d3)
	}
}

func TestDuplicateExplicitDirsCollapse(t *testing.T) {
	sys, d := tempDir(t), tempDir(t)
	withSystemDirs(t, sys)
	link := filepath.Join(tempDir(t), "alias")
	if err := os.Symlink(d, link); err != nil {
		t.Fatal(err)
	}
	r := newOrFatal(t, Config{ExplicitDirs: []string{d, d + "/", d + "/./", link, d}})
	if dirs := r.Dirs(); len(dirs) != 2 || dirs[0].Path != d {
		t.Errorf("Dirs = %+v, want one explicit dir (%s) plus the system dir", dirs, d)
	}
}

func TestRefusedExplicitDirs(t *testing.T) {
	sys, project, outside := tempDir(t), tempDir(t), tempDir(t)
	withSystemDirs(t, sys)

	sub := filepath.Join(project, "tools", "bin")
	nm := filepath.Join(project, "node_modules", ".bin")
	for _, d := range []string{sub, nm} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A directory outside the project that merely looks like node_modules.
	outsideNM := filepath.Join(outside, "node_modules", ".bin")
	if err := os.MkdirAll(outsideNM, 0o755); err != nil {
		t.Fatal(err)
	}
	// A symlink outside the project that points into it.
	intoProject := filepath.Join(outside, "innocent")
	if err := os.Symlink(sub, intoProject); err != nil {
		t.Fatal(err)
	}
	// A symlink inside the project that points outside: the project
	// could re-aim it at any moment, so it is refused lexically too.
	viaProject := filepath.Join(project, "shortcut")
	if err := os.Symlink(outside, viaProject); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(outside, "file")
	if err := os.WriteFile(file, nil, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, dir, reason string
	}{
		{"empty", "", "absolute"},
		{"relative", "bin", "absolute"},
		{"dot-relative", "./bin", "absolute"},
		{"tilde is not expanded", "~/bin", "absolute"},
		{"project root", project, "inside the project"},
		{"project root with slash", project + "/", "inside the project"},
		{"project root via ..", filepath.Join(project, "tools", ".."), "inside the project"},
		{"project subdirectory", sub, "inside the project"},
		{"project node_modules/.bin", nm, "node_modules"},
		{"node_modules/.bin outside the project", outsideNM, "node_modules"},
		{"symlink into project", intoProject, "inside the project"},
		{"project symlink to outside", viaProject, "inside the project"},
		{"missing", filepath.Join(outside, "nope"), "cannot be resolved"},
		{"not a directory", file, "not a directory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dirErr(t, Config{ProjectRoots: []string{project}, ExplicitDirs: []string{tc.dir}}, tc.reason)
		})
	}
}

func TestProjectRootGivenAsRelativeOrSymlinkIsHonoured(t *testing.T) {
	sys, base := tempDir(t), tempDir(t)
	withSystemDirs(t, sys)
	project := filepath.Join(base, "proj")
	bin := filepath.Join(project, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(project, alias); err != nil {
		t.Fatal(err)
	}
	// Root named through a symlink: the canonical form of the tool dir
	// still falls under it.
	dirErr(t, Config{ProjectRoots: []string{alias}, ExplicitDirs: []string{bin}}, "inside the project")

	// Root named relatively: made absolute against the working directory.
	wd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(base); err != nil {
		t.Fatal(err)
	}
	dirErr(t, Config{ProjectRoots: []string{"proj"}, ExplicitDirs: []string{bin}}, "inside the project")
}

func TestWorldWritableExplicitDirRefused(t *testing.T) {
	sys, d := tempDir(t), tempDir(t)
	withSystemDirs(t, sys)
	if err := os.Chmod(d, 0o777); err != nil {
		t.Fatal(err)
	}
	dirErr(t, Config{ExplicitDirs: []string{d}}, "world-writable")
	if err := os.Chmod(d, 0o1777); err != nil { // sticky does not make it safe to trust
		t.Fatal(err)
	}
	dirErr(t, Config{ExplicitDirs: []string{d}}, "world-writable")
	if err := os.Chmod(d, 0o755); err != nil {
		t.Fatal(err)
	}
	newOrFatal(t, Config{ExplicitDirs: []string{d}})
}

func TestForeignOwnerExplicitDirIsAcceptedWithAWarning(t *testing.T) {
	sys, d := tempDir(t), tempDir(t)
	withSystemDirs(t, sys)
	want := writeExe(t, d, "node")

	// Simulate "not mine": as root, really chown to an unrelated uid;
	// otherwise pretend the invoking user is somebody else.
	euid := os.Geteuid()
	if euid == 0 {
		if err := os.Chown(d, 12345, -1); err != nil {
			t.Skipf("cannot chown: %v", err)
		}
	} else {
		euid++
	}
	r, err := build(Config{ExplicitDirs: []string{d}}, euid)
	if err != nil {
		t.Fatalf("a directory owned by another user was refused: %v (Node installed by a build user, run as root, must work)", err)
	}
	w := r.Warnings()
	if len(w) != 1 || !strings.Contains(w[0], d) || !strings.Contains(w[0], "owned by uid") {
		t.Fatalf("Warnings = %q, want one ownership warning naming %s", w, d)
	}
	if got, err := r.Resolve("node"); err != nil || got != want {
		t.Errorf("Resolve = %q, %v; the warning must not change resolution", got, err)
	}

	// Owned by the invoking user: no warning.
	fi, _ := os.Stat(d)
	uid, _ := ownerOf(fi)
	r, err = build(Config{ExplicitDirs: []string{d}}, int(uid))
	if err != nil || len(r.Warnings()) != 0 {
		t.Errorf("own directory: err %v, warnings %q; want none", err, r.Warnings())
	}
}

func TestRootOwnedExplicitDirAccepted(t *testing.T) {
	// /usr is root-owned and not world-writable on every sane system;
	// it must be accepted for an invoking user that is not root.
	fi, err := os.Stat("/usr/bin")
	if err != nil {
		t.Skip("no /usr/bin")
	}
	if uid, _ := ownerOf(fi); uid != 0 {
		t.Skip("/usr/bin is not root-owned here")
	}
	r, err := build(Config{ExplicitDirs: []string{"/usr/bin"}}, os.Geteuid()+1)
	if err != nil || len(r.Warnings()) != 0 {
		t.Errorf("root-owned directory: err %v, warnings %q; want accepted silently", err, r.Warnings())
	}
}

func TestFirstRefusedDirIsReportedDeterministically(t *testing.T) {
	sys, good := tempDir(t), tempDir(t)
	withSystemDirs(t, sys)
	_, err := New(Config{ExplicitDirs: []string{good, "rel-a", "/definitely/not/here", "rel-b"}})
	var de *DirError
	if !errors.As(err, &de) || de.Dir != "rel-a" {
		t.Fatalf("error = %v, want the first refused directory (rel-a)", err)
	}
}

func TestSymlinkCandidateIntoProjectIsUnsafeNotSkipped(t *testing.T) {
	sys, user, project := tempDir(t), tempDir(t), tempDir(t)
	withSystemDirs(t, sys)
	evil := writeExe(t, project, "evil-node")
	legit := writeExe(t, sys, "node") // a later directory that WOULD answer if the bad one were stepped over
	if err := os.Symlink(evil, filepath.Join(user, "node")); err != nil {
		t.Fatal(err)
	}
	r := newOrFatal(t, Config{ProjectRoots: []string{project}, ExplicitDirs: []string{user}})
	got, err := r.Resolve("node")
	if !errors.Is(err, ErrUnsafe) {
		t.Fatalf("Resolve = %q, %v; want ErrUnsafe (never skipped to %s)", got, err, legit)
	}
	if errors.Is(err, ErrNotFound) {
		t.Error("an unsafe candidate must not read as 'not installed'")
	}
}

func TestSymlinkChainIntoProjectIsUnsafe(t *testing.T) {
	sys, user, project, hop := tempDir(t), tempDir(t), tempDir(t), tempDir(t)
	withSystemDirs(t, sys)
	evil := writeExe(t, project, "evil")
	if err := os.Symlink(evil, filepath.Join(hop, "hop")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(hop, "hop"), filepath.Join(user, "npm")); err != nil {
		t.Fatal(err)
	}
	r := newOrFatal(t, Config{ProjectRoots: []string{project}, ExplicitDirs: []string{user}})
	if _, err := r.Resolve("npm"); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("error = %v, want ErrUnsafe through a symlink chain", err)
	}
}

func TestSymlinkIntoProjectFromSystemDirIsUnsafeToo(t *testing.T) {
	sys, project := tempDir(t), tempDir(t)
	withSystemDirs(t, sys)
	evil := writeExe(t, project, "evil")
	if err := os.Symlink(evil, filepath.Join(sys, "node")); err != nil {
		t.Fatal(err)
	}
	r := newOrFatal(t, Config{ProjectRoots: []string{project}})
	if _, err := r.Resolve("node"); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("error = %v, want ErrUnsafe (e.g. `npm link` aimed a global bin at the project)", err)
	}
}

// A shim is started by its own name: Volta's and asdf's shims are
// files, mise's are symlinks to the manager binary, and all of them
// dispatch on argv[0]. Resolving the symlink would run `mise --version`
// and call it node.
func TestSymlinkShimIsReturnedUnresolved(t *testing.T) {
	sys, user, manager := tempDir(t), tempDir(t), tempDir(t)
	withSystemDirs(t, sys)
	mise := writeExe(t, manager, "mise")
	shim := filepath.Join(user, "node")
	if err := os.Symlink(mise, shim); err != nil {
		t.Fatal(err)
	}
	r := newOrFatal(t, Config{ExplicitDirs: []string{user}})
	got, err := r.Resolve("node")
	if err != nil || got != shim {
		t.Fatalf("Resolve = %q, %v; want the shim path itself %q", got, err, shim)
	}
}

func TestVersionManagerStyleFixtures(t *testing.T) {
	sys, home := tempDir(t), tempDir(t)
	withSystemDirs(t, sys)
	volta := filepath.Join(home, ".volta", "bin")                            // Volta: copies of one shim binary
	asdf := filepath.Join(home, ".asdf", "shims")                            // asdf: wrapper scripts
	mise := filepath.Join(home, ".local", "share", "mise", "shims")          // mise: symlinks to mise
	nvm := filepath.Join(home, ".nvm", "versions", "node", "v24.7.0", "bin") // nvm: real tree, no shim
	for _, d := range []string{volta, asdf, mise, nvm} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeExe(t, volta, "node")
	writeExe(t, asdf, "node")
	miseBin := writeExe(t, home, "mise")
	if err := os.Symlink(miseBin, filepath.Join(mise, "node")); err != nil {
		t.Fatal(err)
	}
	writeExe(t, nvm, "node")

	for _, d := range []string{volta, asdf, mise, nvm} {
		r := newOrFatal(t, Config{ExplicitDirs: []string{d}})
		got, err := r.Resolve("node")
		if err != nil || got != filepath.Join(d, "node") {
			t.Errorf("--tool-dir %s: Resolve = %q, %v", d, got, err)
		}
	}
}

func TestMissingToolIsNotFound(t *testing.T) {
	sys, user := tempDir(t), tempDir(t)
	withSystemDirs(t, sys)
	r := newOrFatal(t, Config{ExplicitDirs: []string{user}})
	if got, err := r.Resolve("node"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve = %q, %v; want ErrNotFound", got, err)
	}
}

func TestNonExecutableAndNonRegularCandidatesInSystemDirsAreSkipped(t *testing.T) {
	sys, next := tempDir(t), tempDir(t)
	withSystemDirs(t, sys, next)
	if err := os.WriteFile(filepath.Join(sys, "node"), []byte("x"), 0o644); err != nil { // not executable
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(sys, "npm"), 0o755); err != nil { // a directory
		t.Fatal(err)
	}
	want := writeExe(t, next, "node")
	r := newOrFatal(t, Config{})
	if got, err := r.Resolve("node"); err != nil || got != want {
		t.Errorf("Resolve(node) = %q, %v; want the next directory's %q", got, err, want)
	}
	if _, err := r.Resolve("npm"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Resolve(npm) error = %v, want ErrNotFound", err)
	}
}

func TestDanglingSymlinkInUserDirIsAnErrorNotAbsence(t *testing.T) {
	sys, user := tempDir(t), tempDir(t)
	withSystemDirs(t, sys)
	writeExe(t, sys, "node")
	if err := os.Symlink(filepath.Join(user, "gone"), filepath.Join(user, "node")); err != nil {
		t.Fatal(err)
	}
	r := newOrFatal(t, Config{ExplicitDirs: []string{user}})
	_, err := r.Resolve("node")
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v; a dangling shim is a broken install, not 'not installed' (and must not fall through to the system node)", err)
	}
}

func TestResolveIsDeterministic(t *testing.T) {
	sys, d1, d2 := tempDir(t), tempDir(t), tempDir(t)
	withSystemDirs(t, sys)
	writeExe(t, d1, "node")
	writeExe(t, d2, "node")
	writeExe(t, sys, "node")
	r := newOrFatal(t, Config{ExplicitDirs: []string{d1, d2}})
	first, _ := r.Resolve("node")
	for i := 0; i < 50; i++ {
		if got, _ := r.Resolve("node"); got != first {
			t.Fatalf("run %d: %q != %q", i, got, first)
		}
	}
}
