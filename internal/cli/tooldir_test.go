package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xCHANDA/womm/internal/core"
	"github.com/0xCHANDA/womm/internal/inspect"
	"github.com/0xCHANDA/womm/internal/inspect/toolpath"
)

const nodeReq = `version: 1
requirements:
  - name: node
    constraint: ">=22 <25"
    evidence:
      - source: package.json
        field: engines.node
        value: ">=22 <25"
`

// realDir is a canonical temp directory (tool directories are
// canonicalized, so expected paths must be too).
func realDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func fakeTool(t *testing.T, dir, name, script string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// useSystemDirs swaps the system allowlist for a test directory and
// restores it afterwards.
func useSystemDirs(t *testing.T, dirs ...string) {
	t.Helper()
	orig := toolpath.SystemDirs
	toolpath.SystemDirs = dirs
	t.Cleanup(func() { toolpath.SystemDirs = orig })
}

func TestVerifyToolDirInvalidIsUsageError(t *testing.T) {
	useInspector(t, stubInspector{obs: map[string]core.Observation{"node": {Name: "node", Present: true, Version: "24.7.0"}}})
	project := realDir(t)
	writeWomm(t, project, nodeReq)
	if err := os.MkdirAll(filepath.Join(project, "node_modules", ".bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	world := realDir(t)
	if err := os.Chmod(world, 0o777); err != nil {
		t.Fatal(err)
	}

	cases := []struct{ name, dir, want string }{
		{"relative", "bin", "must be an absolute path"},
		{"project root", project, "inside the project"},
		{"project subdirectory", filepath.Join(project, "tools"), "inside the project"},
		{"node_modules/.bin", filepath.Join(project, "node_modules", ".bin"), "node_modules"},
		{"missing", filepath.Join(realDir(t), "nope"), "cannot be resolved"},
		{"world-writable", world, "world-writable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runCLI(t, "verify", project, "--tool-dir", tc.dir)
			if code != 2 {
				t.Fatalf("exit %d, want 2 (usage)\nstderr: %s", code, stderr)
			}
			if stdout != "" {
				t.Errorf("a refused flag must produce no report, got stdout %q", stdout)
			}
			if !strings.Contains(stderr, `invalid argument "`+tc.dir+`" for "--tool-dir" flag`) || !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr lacks the flag diagnostic %q:\n%s", tc.want, stderr)
			}
		})
	}
}

func TestVerifyToolDirRefusesTheDirectoryOfTheVerifiedFile(t *testing.T) {
	useInspector(t, stubInspector{obs: map[string]core.Observation{"node": {Name: "node", Present: true, Version: "24.7.0"}}})
	elsewhere, tools := realDir(t), realDir(t)
	cfgDir := realDir(t)
	file := writeWomm(t, cfgDir, nodeReq)
	// -f points at a womm.yaml in cfgDir: that directory is as
	// project-controlled as the project argument.
	if code, _, stderr := runCLI(t, "verify", elsewhere, "-f", file, "--tool-dir", cfgDir); code != 2 || !strings.Contains(stderr, "inside the project") {
		t.Errorf("exit %d, stderr %q; want a usage error for the file's own directory", code, stderr)
	}
	if code, _, stderr := runCLI(t, "verify", elsewhere, "-f", file, "--tool-dir", tools); code != 0 {
		t.Errorf("exit %d, stderr %q; an unrelated directory must be accepted", code, stderr)
	}
}

func TestVerifyToolDirOrderAndDuplicatesReachTheResolver(t *testing.T) {
	project, d1, d2 := realDir(t), realDir(t), realDir(t)
	writeWomm(t, project, nodeReq)
	useSystemDirs(t, "/usr/bin")

	var got []toolpath.Dir
	prev := newInspectors
	newInspectors = func(r *toolpath.Resolver) []inspect.Inspector {
		got = r.Dirs()
		return []inspect.Inspector{stubInspector{obs: map[string]core.Observation{"node": {Name: "node", Present: true, Version: "24.7.0"}}}}
	}
	t.Cleanup(func() { newInspectors = prev })

	if code, _, stderr := runCLI(t, "verify", project, "--tool-dir", d2, "--tool-dir", d1, "--tool-dir", d2+"/"); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	want := []toolpath.Dir{{Path: d2, Origin: toolpath.OriginExplicit}, {Path: d1, Origin: toolpath.OriginExplicit}, {Path: "/usr/bin", Origin: toolpath.OriginSystem}}
	if len(got) != len(want) {
		t.Fatalf("Dirs = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Dirs[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// The matrix below runs the REAL inspector against fake tools, end to
// end through the CLI.

func realInspectorProject(t *testing.T) string {
	t.Helper()
	project := realDir(t)
	writeWomm(t, project, nodeReq)
	return project
}

func TestVerifyOldSystemNodeVersusNewerExplicitNode(t *testing.T) {
	project, sys, user := realInspectorProject(t), realDir(t), realDir(t)
	useSystemDirs(t, sys)
	fakeTool(t, sys, "node", "echo v12.22.0\n")
	userNode := fakeTool(t, user, "node", "echo v24.7.0\n")

	// Without the flag: the system node answers, and the report says which.
	code, stdout, _ := runCLI(t, "verify", project)
	if code != 1 || !strings.Contains(stdout, "FAIL        node required >=22 <25; observed 12.22.0 at "+filepath.Join(sys, "node")+"\n") {
		t.Fatalf("without --tool-dir: exit %d\n%s", code, stdout)
	}
	// With it: the explicit directory wins deterministically.
	code, stdout, stderr := runCLI(t, "verify", project, "--tool-dir", user)
	if code != 0 || !strings.Contains(stdout, "PASS        node required >=22 <25; observed 24.7.0 at "+userNode+"\n") {
		t.Fatalf("with --tool-dir: exit %d\nstdout %s\nstderr %s", code, stdout, stderr)
	}
}

func TestVerifyToolDirFallsThroughToSystemForOtherTools(t *testing.T) {
	project, sys, user := realDir(t), realDir(t), realDir(t)
	useSystemDirs(t, sys)
	writeWomm(t, project, "version: 1\nrequirements:\n"+
		"  - {name: node, constraint: '>=22', evidence: [{source: package.json, field: engines.node, value: '>=22'}]}\n"+
		"  - {name: npm, constraint: '>=10', evidence: [{source: package.json, field: engines.npm, value: '>=10'}]}\n")
	userNode := fakeTool(t, user, "node", "echo v24.7.0\n")
	sysNpm := fakeTool(t, sys, "npm", "echo 10.9.2\n")

	code, stdout, stderr := runCLI(t, "verify", project, "--tool-dir", user)
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	for _, want := range []string{"observed 24.7.0 at " + userNode + "\n", "observed 10.9.2 at " + sysNpm + "\n"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}

func TestVerifyToolDirMissingToolStillFails(t *testing.T) {
	project, sys, user := realInspectorProject(t), realDir(t), realDir(t)
	useSystemDirs(t, sys)
	code, stdout, _ := runCLI(t, "verify", project, "--tool-dir", user)
	if code != 1 || !strings.Contains(stdout, "FAIL        node required >=22 <25; observed absent\n") {
		t.Fatalf("exit %d\n%s", code, stdout)
	}
}

func TestVerifyToolDirUnreachableAndUnknownUnchanged(t *testing.T) {
	project, sys, user := realInspectorProject(t), realDir(t), realDir(t)
	useSystemDirs(t, sys)

	tool := fakeTool(t, user, "node", "echo boom >&2\nexit 3\n")
	code, stdout, _ := runCLI(t, "verify", project, "--tool-dir", user)
	if code != 3 || !strings.Contains(stdout, "UNREACHABLE node required >=22 <25; observed present at "+tool+", version unknown\n") {
		t.Fatalf("unreachable: exit %d\n%s", code, stdout)
	}

	fakeTool(t, user, "node", "echo not-a-version\n")
	code, stdout, _ = runCLI(t, "verify", project, "--tool-dir", user)
	if code != 3 || !strings.HasPrefix(stdout, "UNKNOWN") {
		t.Fatalf("unknown: exit %d\n%s", code, stdout)
	}
}

// A shim that points into the project is the attack this whole package
// exists to stop. It must be refused — not executed, and not stepped
// over to a system node that would then answer in its place.
func TestVerifyToolDirSymlinkIntoProjectIsRefusedAndNeverRuns(t *testing.T) {
	project, sys, user := realInspectorProject(t), realDir(t), realDir(t)
	useSystemDirs(t, sys)
	fakeTool(t, sys, "node", "echo v24.7.0\n") // would PASS if the bad shim were skipped
	marker := filepath.Join(project, "pwned")
	evil := fakeTool(t, project, "evil-node", "touch '"+marker+"'\necho v24.7.0\n")
	if err := os.Symlink(evil, filepath.Join(user, "node")); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLI(t, "verify", project, "--tool-dir", user)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the project's binary was executed through a user-directory symlink")
	}
	if code != 3 {
		t.Errorf("exit %d, want 3 (inconclusive)", code)
	}
	if strings.Contains(stdout, "PASS") || strings.Contains(stdout, "observed 24.7.0") {
		t.Errorf("a verdict was produced for a refused binary:\n%s", stdout)
	}
	if !strings.Contains(stderr, "unsafe executable") || !strings.Contains(stderr, "inside the project") {
		t.Errorf("stderr lacks the diagnostic:\n%s", stderr)
	}
}

func TestVerifyIgnoresInheritedPATHEvenWithToolDir(t *testing.T) {
	project, sys, user, evilDir := realInspectorProject(t), realDir(t), realDir(t), realDir(t)
	useSystemDirs(t, sys)
	marker := filepath.Join(evilDir, "pwned")
	fakeTool(t, evilDir, "node", "touch '"+marker+"'\necho v24.7.0\n")
	fakeTool(t, user, "node", "echo v24.7.0\n")
	t.Setenv("PATH", evilDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if code, _, _ := runCLI(t, "verify", project, "--tool-dir", user); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("an inherited PATH entry was executed")
	}
}

func TestVerifyToolDirOutputIsDeterministic(t *testing.T) {
	project, sys, user := realInspectorProject(t), realDir(t), realDir(t)
	useSystemDirs(t, sys)
	fakeTool(t, user, "node", "echo v24.7.0\n")
	_, first, _ := runCLI(t, "verify", project, "--tool-dir", user)
	for i := 0; i < 5; i++ {
		if _, again, _ := runCLI(t, "verify", project, "--tool-dir", user); again != first {
			t.Fatalf("run %d differs:\n%s\nvs\n%s", i, again, first)
		}
	}
}

// Node installed by a build user and verified as root is the most common
// real-world shape: the directory is accepted, the verdict is produced,
// and stderr says whose it is.
func TestVerifyToolDirOwnedByAnotherUserWarnsButWorks(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to create a directory owned by another uid")
	}
	project, sys, user := realInspectorProject(t), realDir(t), realDir(t)
	useSystemDirs(t, sys)
	tool := fakeTool(t, user, "node", "echo v24.7.0\n")
	if err := os.Chown(user, 12345, -1); err != nil {
		t.Skipf("cannot chown: %v", err)
	}
	code, stdout, stderr := runCLI(t, "verify", project, "--tool-dir", user)
	if code != 0 || !strings.Contains(stdout, "observed 24.7.0 at "+tool+"\n") {
		t.Fatalf("exit %d\nstdout %s\nstderr %s", code, stdout, stderr)
	}
	if want := "warning: tool directory " + user + " is owned by uid 12345"; !strings.Contains(stderr, want) {
		t.Errorf("stderr lacks %q:\n%s", want, stderr)
	}
}
