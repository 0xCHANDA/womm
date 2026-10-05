package toolpath

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shimHome builds a fake account home with the given shim directories
// (relative to it), each holding an executable `node`, and returns the
// home and the absolute path of each directory in the order given.
func shimHome(t *testing.T, rels ...string) (string, []string) {
	t.Helper()
	home := tempDir(t)
	var dirs []string
	for _, rel := range rels {
		d := filepath.Join(home, rel)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		writeExe(t, d, "node")
		dirs = append(dirs, d)
	}
	return home, dirs
}

func TestUserShimDirsListIsTheDocumentedFixedSet(t *testing.T) {
	want := []string{".volta/bin", ".asdf/shims", ".local/share/mise/shims", ".local/bin"}
	if strings.Join(UserShimDirs, "|") != strings.Join(want, "|") {
		t.Fatalf("UserShimDirs = %v, want %v (changing it is a security decision, not a cleanup)", UserShimDirs, want)
	}
}

func TestUserShimDirsAreSearchedAfterSystemInFixedOrder(t *testing.T) {
	sys := tempDir(t)
	withSystemDirs(t, sys)
	home, dirs := shimHome(t, UserShimDirs...)
	r := newOrFatal(t, Config{UserHome: home})

	got := r.Dirs()
	if len(got) != 1+len(UserShimDirs) || got[0].Origin != OriginSystem {
		t.Fatalf("Dirs = %+v, want the system directory first", got)
	}
	for i, d := range dirs {
		if got[1+i].Path != d || got[1+i].Origin != OriginUserShim {
			t.Errorf("Dirs[%d] = %+v, want user-shim %s", 1+i, got[1+i], d)
		}
	}
	if w := r.Warnings(); len(w) != 0 {
		t.Errorf("Warnings = %q, want none", w)
	}

	// A system tool always wins: nothing changes for machines that have one.
	sysNode := writeExe(t, sys, "node")
	if p, err := r.Resolve("node"); err != nil || p != sysNode {
		t.Fatalf("Resolve = %q, %v; want the system node %q", p, err, sysNode)
	}
	if err := os.Remove(sysNode); err != nil {
		t.Fatal(err)
	}
	// Without it the shim directories answer in their fixed order.
	for i, d := range dirs {
		if p, err := r.Resolve("node"); err != nil || p != filepath.Join(d, "node") {
			t.Fatalf("step %d: Resolve = %q, %v; want %s", i, p, err, d)
		}
		if err := os.Remove(filepath.Join(d, "node")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Resolve("node"); !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound once every candidate is gone", err)
	}
}

func TestExplicitDirBeatsSystemAndShimDirs(t *testing.T) {
	sys, user := tempDir(t), tempDir(t)
	withSystemDirs(t, sys)
	writeExe(t, sys, "node")
	home, _ := shimHome(t, ".volta/bin")
	want := writeExe(t, user, "node")
	r := newOrFatal(t, Config{UserHome: home, ExplicitDirs: []string{user}})
	if p, _ := r.Resolve("node"); p != want {
		t.Errorf("Resolve = %q, want the explicit dir's %q", p, want)
	}
}

func TestMissingShimDirsAreSilent(t *testing.T) {
	withSystemDirs(t, tempDir(t))
	r := newOrFatal(t, Config{UserHome: tempDir(t)}) // an empty home
	if len(r.Warnings()) != 0 || len(r.Dirs()) != 1 {
		t.Errorf("Dirs %+v, Warnings %q; an absent shim directory is normal and must not be reported", r.Dirs(), r.Warnings())
	}
	// A path component that is a file (~/.local is a file) is silent too.
	home := tempDir(t)
	if err := os.WriteFile(filepath.Join(home, ".local"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	r = newOrFatal(t, Config{UserHome: home})
	if len(r.Warnings()) != 0 {
		t.Errorf("Warnings = %q, want none", r.Warnings())
	}
}

func TestNoHomeDisablesShimDirs(t *testing.T) {
	withSystemDirs(t, tempDir(t))
	// A real, safe shim directory exists — but only reachable through a
	// relative "home", which must never be used (the working directory
	// is not trusted; the home comes from the user database).
	base := tempDir(t)
	if err := os.MkdirAll(filepath.Join(base, "home", ".volta", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(base); err != nil {
		t.Fatal(err)
	}
	for _, home := range []string{"", "home", "./home", "~"} {
		r := newOrFatal(t, Config{UserHome: home})
		if len(r.Dirs()) != 1 || len(r.Warnings()) != 0 {
			t.Errorf("UserHome %q: Dirs %+v, Warnings %q; want system only and silence", home, r.Dirs(), r.Warnings())
		}
	}
}

func TestUnsafeShimDirsAreSkippedWithAWarning(t *testing.T) {
	sys := tempDir(t)
	withSystemDirs(t, sys)
	home, dirs := shimHome(t, ".volta/bin", ".asdf/shims", ".local/bin")
	volta, asdf, local := dirs[0], dirs[1], dirs[2]
	if err := os.Chmod(volta, 0o777); err != nil { // world-writable
		t.Fatal(err)
	}
	// .asdf/shims is a file, not a directory.
	if err := os.RemoveAll(asdf); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(asdf, nil, 0o755); err != nil {
		t.Fatal(err)
	}

	r := newOrFatal(t, Config{UserHome: home})
	w := r.Warnings()
	if len(w) != 2 {
		t.Fatalf("Warnings = %q, want two (volta, asdf)", w)
	}
	if !strings.Contains(w[0], volta) || !strings.Contains(w[0], "skipped") || !strings.Contains(w[0], "world-writable") {
		t.Errorf("warning 0 = %q", w[0])
	}
	if !strings.Contains(w[1], asdf) || !strings.Contains(w[1], "not a directory") {
		t.Errorf("warning 1 = %q", w[1])
	}
	// The unsafe directories are not searched; the safe one still answers.
	if p, err := r.Resolve("node"); err != nil || p != filepath.Join(local, "node") {
		t.Errorf("Resolve = %q, %v; want %s", p, err, filepath.Join(local, "node"))
	}
	for _, d := range r.Dirs() {
		if d.Path == volta || d.Path == asdf {
			t.Errorf("unsafe directory %s is in the search list", d.Path)
		}
	}
}

func TestShimDirOwnedBySomeoneElseIsSkipped(t *testing.T) {
	withSystemDirs(t, tempDir(t))
	home, dirs := shimHome(t, ".volta/bin")
	euid := os.Geteuid()
	if euid == 0 {
		if err := os.Chown(dirs[0], 12345, -1); err != nil {
			t.Skipf("cannot chown: %v", err)
		}
	} else {
		euid++
	}
	r, err := build(Config{UserHome: home}, euid)
	if err != nil {
		t.Fatal(err)
	}
	if w := r.Warnings(); len(w) != 1 || !strings.Contains(w[0], "owned by uid") || !strings.Contains(w[0], "skipped") {
		t.Fatalf("Warnings = %q, want one ownership skip", w)
	}
	if _, err := r.Resolve("node"); !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v; a foreign-owned shim directory must not be searched", err)
	}
	// The same directory, typed explicitly, is only a warning.
	r, err = build(Config{ExplicitDirs: []string{dirs[0]}}, euid)
	if err != nil {
		t.Fatalf("explicit foreign-owned directory refused: %v", err)
	}
	if _, err := r.Resolve("node"); err != nil {
		t.Errorf("explicit directory not searched: %v", err)
	}
}

func TestShimDirsInsideTheProjectAreSkipped(t *testing.T) {
	withSystemDirs(t, tempDir(t))

	// The project IS the home directory (womm run from ~): everything
	// under it is project-controlled.
	home, _ := shimHome(t, ".volta/bin", ".local/bin")
	r := newOrFatal(t, Config{UserHome: home, ProjectRoots: []string{home}})
	if len(r.Warnings()) != 2 || len(r.Dirs()) != 1 {
		t.Errorf("project == home: Dirs %+v, Warnings %q; want both shim dirs skipped", r.Dirs(), r.Warnings())
	}
	for _, w := range r.Warnings() {
		if !strings.Contains(w, "inside the project") {
			t.Errorf("warning %q does not say why", w)
		}
	}

	// A shim directory that is a symlink into the project.
	home = tempDir(t)
	project := tempDir(t)
	inProject := filepath.Join(project, "bin")
	if err := os.MkdirAll(inProject, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExe(t, inProject, "node")
	if err := os.MkdirAll(filepath.Join(home, ".local"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(inProject, filepath.Join(home, ".local", "bin")); err != nil {
		t.Fatal(err)
	}
	r = newOrFatal(t, Config{UserHome: home, ProjectRoots: []string{project}})
	if w := r.Warnings(); len(w) != 1 || !strings.Contains(w[0], "inside the project") {
		t.Fatalf("Warnings = %q, want the symlink-into-project skip", w)
	}
	if _, err := r.Resolve("node"); !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v; the project's node must never be found through a shim directory", err)
	}
}

func TestShimCandidateSymlinkIntoProjectIsUnsafe(t *testing.T) {
	withSystemDirs(t, tempDir(t))
	home, dirs := shimHome(t, ".volta/bin")
	project := tempDir(t)
	evil := writeExe(t, project, "evil")
	if err := os.Remove(filepath.Join(dirs[0], "node")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(evil, filepath.Join(dirs[0], "node")); err != nil {
		t.Fatal(err)
	}
	r := newOrFatal(t, Config{UserHome: home, ProjectRoots: []string{project}})
	if _, err := r.Resolve("node"); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("error = %v, want ErrUnsafe", err)
	}
}

func TestShimDirsUnderNodeModulesAreSkipped(t *testing.T) {
	withSystemDirs(t, tempDir(t))
	base := tempDir(t)
	home := filepath.Join(base, "node_modules", "home")
	if err := os.MkdirAll(filepath.Join(home, ".volta", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := newOrFatal(t, Config{UserHome: home})
	if w := r.Warnings(); len(w) != 1 || !strings.Contains(w[0], "node_modules") {
		t.Errorf("Warnings = %q, want a node_modules skip", w)
	}
}

func TestShimDirEqualToExplicitDirIsNotSearchedTwice(t *testing.T) {
	withSystemDirs(t, tempDir(t))
	home, dirs := shimHome(t, ".volta/bin")
	r := newOrFatal(t, Config{UserHome: home, ExplicitDirs: []string{dirs[0]}})
	n := 0
	for _, d := range r.Dirs() {
		if d.Path == dirs[0] {
			n++
			if d.Origin != OriginExplicit {
				t.Errorf("origin = %v, want explicit (the typed position wins)", d.Origin)
			}
		}
	}
	if n != 1 {
		t.Errorf("directory listed %d times", n)
	}
}

func TestVoltaAsdfMiseShapedShimsEndToEnd(t *testing.T) {
	withSystemDirs(t, tempDir(t))
	home := tempDir(t)
	mise := filepath.Join(home, ".local", "share", "mise", "shims")
	if err := os.MkdirAll(mise, 0o755); err != nil {
		t.Fatal(err)
	}
	manager := writeExe(t, home, "mise")
	shim := filepath.Join(mise, "node")
	if err := os.Symlink(manager, shim); err != nil {
		t.Fatal(err)
	}
	r := newOrFatal(t, Config{UserHome: home})
	if p, err := r.Resolve("node"); err != nil || p != shim {
		t.Fatalf("Resolve = %q, %v; want the mise shim itself, not its target", p, err)
	}
}

// --- findings of the independent reviews of this slice ----------------

// A ':' in a directory name splits the probe PATH: "/x/a:/x/evil" puts
// /x/evil on it, a directory nobody vetted.
func TestDirsContainingPathSeparatorsOrControlCharactersAreRefused(t *testing.T) {
	sys, base := tempDir(t), tempDir(t)
	withSystemDirs(t, sys)
	for _, name := range []string{"a:b", "a:", ":a", "tab\there", "nl\nx", "esc\x1b[2J"} {
		d := filepath.Join(base, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := New(Config{ExplicitDirs: []string{d}})
		var de *DirError
		if !errors.As(err, &de) || !strings.Contains(de.Reason, "PATH") {
			t.Errorf("%q: error = %v, want a refusal explaining it cannot go on a PATH", name, err)
		}
		// The same name as the account home disables the shim directories with a warning.
		home := filepath.Join(base, "home-"+name)
		if err := os.MkdirAll(filepath.Join(home, ".volta", "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		r := newOrFatal(t, Config{UserHome: home})
		if len(r.Dirs()) != 1 || len(r.Warnings()) != 1 {
			t.Errorf("home %q: Dirs %+v Warnings %q; want the shim dir skipped with a warning", name, r.Dirs(), r.Warnings())
		}
	}
}

// A user directory that has an entry of the right name which cannot be
// run is a broken install. Stepping over it would answer for a
// different binary (the system's) without a word.
func TestUnusableCandidateInAUserDirIsAnErrorNotAFallThrough(t *testing.T) {
	sys := tempDir(t)
	withSystemDirs(t, sys)
	writeExe(t, sys, "node")
	for name, make := range map[string]func(p string) error{
		"not executable": func(p string) error { return os.WriteFile(p, []byte("x"), 0o644) },
		"directory":      func(p string) error { return os.Mkdir(p, 0o755) },
		"fifo":           func(p string) error { return mkfifo(p) },
	} {
		t.Run(name, func(t *testing.T) {
			user := tempDir(t)
			if err := make(filepath.Join(user, "node")); err != nil {
				t.Fatal(err)
			}
			r := newOrFatal(t, Config{ExplicitDirs: []string{user}})
			got, err := r.Resolve("node")
			if err == nil || errors.Is(err, ErrNotFound) {
				t.Fatalf("Resolve = %q, %v; want an operational error, not a silent step to the system node", got, err)
			}
			// The same entry in an implicit shim directory, with no system
			// node in the way (the system directories are searched first).
			withSystemDirs(t, tempDir(t))
			home, _ := shimHome(t)
			if err := os.MkdirAll(filepath.Join(home, ".volta", "bin"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := make(filepath.Join(home, ".volta", "bin", "node")); err != nil {
				t.Fatal(err)
			}
			r = newOrFatal(t, Config{UserHome: home})
			if got, err := r.Resolve("node"); err == nil || errors.Is(err, ErrNotFound) {
				t.Fatalf("shim dir: Resolve = %q, %v; want an operational error", got, err)
			}
			withSystemDirs(t, sys)
		})
	}
	// System directories keep the v0.1 behaviour: keep looking.
	r := newOrFatal(t, Config{})
	if err := os.Remove(filepath.Join(sys, "node")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sys, "node"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve("node"); !errors.Is(err, ErrNotFound) {
		t.Errorf("system dir: error = %v, want ErrNotFound (unchanged v0.1 behaviour)", err)
	}
}

// An implicit directory nobody named that cannot even be searched is
// skipped with a warning; it must not turn "no node" (a FAIL) into an
// operational error.
func TestUnsearchableShimDirIsSkippedWithAWarning(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can search any directory")
	}
	withSystemDirs(t, tempDir(t))
	home, dirs := shimHome(t, ".volta/bin")
	if err := os.Chmod(dirs[0], 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dirs[0], 0o755) })
	r := newOrFatal(t, Config{UserHome: home})
	if w := r.Warnings(); len(w) != 1 || !strings.Contains(w[0], "cannot be searched") {
		t.Fatalf("Warnings = %q", w)
	}
	if _, err := r.Resolve("node"); !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

// Messages carry user-chosen path text: it must be quoted so a hostile
// directory name cannot forge a line or drive the terminal.
func TestMessagesQuoteHostilePaths(t *testing.T) {
	withSystemDirs(t, tempDir(t))
	base := tempDir(t)
	hostile := filepath.Join(base, "evil\x1b[2J\nerror: forged")
	if err := os.MkdirAll(hostile, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(hostile, 0o777); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "h\x1b[2J\nwarning: forged")
	if err := os.MkdirAll(filepath.Join(home, ".volta", "bin"), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(home, ".volta", "bin"), 0o777); err != nil {
		t.Fatal(err)
	}
	_, err := New(Config{ExplicitDirs: []string{hostile}})
	r := newOrFatal(t, Config{UserHome: home})
	msgs := append([]string{}, r.Warnings()...)
	if err != nil {
		msgs = append(msgs, err.Error())
	}
	if len(msgs) != 2 {
		t.Fatalf("messages = %q, want the refusal and one warning", msgs)
	}
	for _, m := range msgs {
		if strings.ContainsAny(m, "\x1b\n\r") {
			t.Errorf("a hostile path reached a message raw: %q", m)
		}
	}
}
