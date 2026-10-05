package toolpath

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Tests added from the independent test-gap review of this package: each
// pins a behaviour that a mutation of the code left unnoticed.

func TestWithinTable(t *testing.T) {
	cases := []struct {
		path, root string
		want       bool
	}{
		{"/a/proj", "/a/proj", true},
		{"/a/proj/bin", "/a/proj", true},
		{"/a/proj/..bin", "/a/proj", true}, // a directory literally named "..bin" is inside
		{"/a/proj/.hidden/x", "/a/proj", true},
		{"/a/proj-tools", "/a/proj", false}, // sibling sharing a prefix
		{"/a/project", "/a/proj", false},
		{"/a", "/a/proj", false}, // the parent is not inside
		{"/", "/a/proj", false},
		{"/a/proj", "/", true},
		{"/anything", "/", true},
		{"/b/proj", "/a/proj", false},
	}
	for _, c := range cases {
		if got := within(c.path, c.root); got != c.want {
			t.Errorf("within(%q, %q) = %v, want %v", c.path, c.root, got, c.want)
		}
	}
}

func TestNodeModulesIsAWholeComponentMatch(t *testing.T) {
	withSystemDirs(t, tempDir(t))
	for _, name := range []string{"my_node_modules", "node_modules_old", "xnode_modules"} {
		d := filepath.Join(tempDir(t), name, "bin")
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := New(Config{ExplicitDirs: []string{d}}); err != nil {
			t.Errorf("%s was refused as if it were node_modules: %v", name, err)
		}
	}
}

func TestSymlinkToANodeModulesDirectoryElsewhereIsRefused(t *testing.T) {
	withSystemDirs(t, tempDir(t))
	base := tempDir(t)
	target := filepath.Join(base, "node_modules", ".bin")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "innocent")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	dirErr(t, Config{ExplicitDirs: []string{link}}, "node_modules")
}

// The project may re-aim a symlink that lives inside it at any moment,
// so a tool directory reached through one is refused on its LEXICAL path
// too, even when the project root is itself named through an alias.
func TestToolDirThroughASymlinkInsideAnAliasedProjectIsRefused(t *testing.T) {
	withSystemDirs(t, tempDir(t))
	base, outside := tempDir(t), tempDir(t)
	project := filepath.Join(base, "proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(project, "link")); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(project, alias); err != nil {
		t.Fatal(err)
	}
	dirErr(t, Config{ProjectRoots: []string{alias}, ExplicitDirs: []string{filepath.Join(alias, "link")}}, "inside the project")
}

// v0.1 behaviour for system directories: any stat failure means "keep
// looking", including a dangling symlink; only user directories turn it
// into an error.
func TestDanglingSymlinkInASystemDirKeepsLooking(t *testing.T) {
	sys, next := tempDir(t), tempDir(t)
	withSystemDirs(t, sys, next)
	if err := os.Symlink(filepath.Join(sys, "gone"), filepath.Join(sys, "node")); err != nil {
		t.Fatal(err)
	}
	want := writeExe(t, next, "node")
	if got, err := System().Resolve("node"); err != nil || got != want {
		t.Errorf("Resolve = %q, %v; want %q", got, err, want)
	}
}

func TestResolveRefusesNULInUserDirs(t *testing.T) {
	withSystemDirs(t, tempDir(t))
	r := newOrFatal(t, Config{ExplicitDirs: []string{tempDir(t)}})
	if _, err := r.Resolve("no\x00de"); !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestDirsAndWarningsAreCopies(t *testing.T) {
	withSystemDirs(t, tempDir(t))
	home, dirs := shimHome(t, ".volta/bin")
	_ = dirs
	if err := os.Chmod(filepath.Join(home, ".volta", "bin"), 0o777); err != nil {
		t.Fatal(err)
	}
	r := newOrFatal(t, Config{UserHome: home})
	d, w := r.Dirs(), r.Warnings()
	if len(d) == 0 || len(w) == 0 {
		t.Fatalf("need something to mutate: %+v %q", d, w)
	}
	d[0].Path, w[0] = "/hijacked", "hijacked"
	if again := r.Dirs(); again[0].Path == "/hijacked" {
		t.Error("Dirs() exposes the internal slice")
	}
	if again := r.Warnings(); again[0] == "hijacked" {
		t.Error("Warnings() exposes the internal slice")
	}
}

// The Resolver is documented immutable and safe for concurrent use.
func TestConcurrentResolve(t *testing.T) {
	sys, user := tempDir(t), tempDir(t)
	withSystemDirs(t, sys)
	want := writeExe(t, user, "node")
	writeExe(t, sys, "npm")
	r := newOrFatal(t, Config{ExplicitDirs: []string{user}})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if got, err := r.Resolve("node"); err != nil || got != want {
					t.Errorf("Resolve(node) = %q, %v", got, err)
					return
				}
				_ = r.Dirs()
				_ = r.Warnings()
			}
		}()
	}
	wg.Wait()
}

func TestShimDirPathIsCanonicalForASymlinkedHomeAndDanglingShimDirIsSilent(t *testing.T) {
	withSystemDirs(t, tempDir(t))
	real, base := tempDir(t), tempDir(t)
	if err := os.MkdirAll(filepath.Join(real, ".volta", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "homealias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	r := newOrFatal(t, Config{UserHome: alias})
	if d := r.Dirs(); len(d) != 2 || d[1].Path != filepath.Join(real, ".volta", "bin") {
		t.Errorf("Dirs = %+v, want the canonical shim directory", d)
	}
	// A dangling symlink where a shim directory would be is absence.
	home := tempDir(t)
	if err := os.Symlink(filepath.Join(home, "gone"), filepath.Join(home, ".volta")); err != nil {
		t.Fatal(err)
	}
	if r := newOrFatal(t, Config{UserHome: home}); len(r.Warnings()) != 0 || len(r.Dirs()) != 1 {
		t.Errorf("Dirs %+v Warnings %q; a dangling link is just not installed", r.Dirs(), r.Warnings())
	}
}

// Documented decisions, pinned so changing one is a conscious edit.
func TestGroupWritableDirIsAcceptedAndAnyExecBitMakesAFileUsable(t *testing.T) {
	withSystemDirs(t, tempDir(t))
	d := tempDir(t)
	if err := os.Chmod(d, 0o775); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0o100, 0o010, 0o001, 0o755} {
		p := filepath.Join(d, "node")
		_ = os.Remove(p)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		r, err := New(Config{ExplicitDirs: []string{d}})
		if err != nil {
			t.Fatalf("group-writable directory refused: %v", err)
		}
		if got, err := r.Resolve("node"); err != nil || got != p {
			t.Errorf("mode %o: Resolve = %q, %v", mode, got, err)
		}
	}
	p := filepath.Join(d, "node")
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	r, _ := New(Config{ExplicitDirs: []string{d}})
	if _, err := r.Resolve("node"); err == nil {
		t.Error("a file without any exec bit was usable")
	}
}

func FuzzWithin(f *testing.F) {
	f.Add("/a/proj/bin", "/a/proj")
	f.Add("/a/proj-x", "/a/proj")
	f.Add("/", "/a")
	f.Fuzz(func(t *testing.T, path, root string) {
		if !filepath.IsAbs(path) || !filepath.IsAbs(root) {
			return
		}
		path, root = filepath.Clean(path), filepath.Clean(root)
		// Reference: component-wise prefix.
		var want bool
		if root == "/" {
			want = true
		} else {
			pc, rc := strings.Split(path, "/"), strings.Split(root, "/")
			want = len(pc) >= len(rc)
			for i := 0; want && i < len(rc); i++ {
				want = pc[i] == rc[i]
			}
		}
		if got := within(path, root); got != want {
			t.Fatalf("within(%q, %q) = %v, want %v", path, root, got, want)
		}
	})
}
