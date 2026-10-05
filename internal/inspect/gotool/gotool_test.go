package gotool

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xCHANDA/womm/internal/core"
	"github.com/0xCHANDA/womm/internal/inspect/probe"
	"github.com/0xCHANDA/womm/internal/inspect/toolpath"
)

func fakeGo(t *testing.T, script string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "go")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func inspector(path string) *GoInspector {
	return &GoInspector{
		resolvePath: func(string) (string, error) {
			if path == "" {
				return "", toolpath.ErrNotFound
			}
			return path, nil
		},
		runDir:  "/",
		timeout: probe.DefaultTimeout,
	}
}

func TestSupportsOnlyGo(t *testing.T) {
	g := inspector("")
	for name, want := range map[string]bool{"go": true, "node": false, "gofmt": false, "Go": false, "": false, "../go": false} {
		if g.Supports(name) != want {
			t.Errorf("Supports(%q) = %v, want %v", name, !want, want)
		}
	}
	if _, err := g.Inspect(context.Background(), core.Requirement{Name: "gofmt"}); !errors.Is(err, ErrUnsupportedTool) {
		t.Errorf("error = %v, want ErrUnsupportedTool", err)
	}
}

func TestInspectPresentAbsentAndUnparseable(t *testing.T) {
	req := core.Requirement{Name: "go"}
	good := fakeGo(t, "echo 'go version go1.24.7 linux/amd64'\n")
	obs, err := inspector(good).Inspect(context.Background(), req)
	if err != nil || obs != (core.Observation{Name: "go", Present: true, Version: "1.24.7", Path: good}) {
		t.Fatalf("good: %+v, %v", obs, err)
	}
	obs, err = inspector("").Inspect(context.Background(), req)
	if err != nil || obs != (core.Observation{Name: "go", Present: false}) {
		t.Fatalf("absent: %+v, %v", obs, err)
	}
	for _, out := range []string{"go version devel go1.25-abc123 Mon Jan 1 linux/amd64", "garbage", "go version go1.21.0-bigcorp linux/amd64", "", "go version go1.24.7 linux/amd64\nextra"} {
		p := fakeGo(t, "printf '%s\\n' '"+out+"'\n")
		obs, err := inspector(p).Inspect(context.Background(), req)
		if err != nil || !obs.Present || obs.Version != "" || obs.Path != p {
			t.Errorf("%q: %+v, %v; want present, version unknown, no error, never a guessed version", out, obs, err)
		}
	}
}

func TestInspectFailureKeepsPresenceAndPath(t *testing.T) {
	for name, script := range map[string]string{"exit": "echo boom >&2\nexit 3\n"} {
		p := fakeGo(t, script)
		obs, err := inspector(p).Inspect(context.Background(), core.Requirement{Name: "go"})
		if err == nil || obs != (core.Observation{Name: "go", Present: true, Path: p}) {
			t.Errorf("%s: %+v, %v", name, obs, err)
		}
	}
	p := fakeGo(t, "sleep 30\n")
	g := inspector(p)
	g.timeout = 100 * time.Millisecond
	obs, err := g.Inspect(context.Background(), core.Requirement{Name: "go"})
	if !errors.Is(err, probe.ErrTimeout) || !obs.Present || obs.Path != p {
		t.Errorf("timeout: %+v, %v", obs, err)
	}
}

func TestResolutionFailureIsAnErrorWithNoObservation(t *testing.T) {
	g := inspector("")
	g.resolvePath = func(string) (string, error) { return "", toolpath.ErrUnsafe }
	obs, err := g.Inspect(context.Background(), core.Requirement{Name: "go"})
	if err == nil || obs != (core.Observation{}) {
		t.Errorf("%+v, %v; an unsafe candidate is an operational error, never absence", obs, err)
	}
}

// What the probe runs with: arguments, directory and environment.
func TestProbeArgumentsEnvironmentAndDirectory(t *testing.T) {
	t.Setenv("GOFLAGS", "-toolexec=/evil")
	t.Setenv("GOTOOLCHAIN", "go1.99.0+auto")
	t.Setenv("GOROOT", "/evil/goroot")
	t.Setenv("GOENV", "/evil/env")
	t.Setenv("GOPATH", "/evil/gopath")
	t.Setenv("VOLTA_HOME", "/evil")
	t.Setenv("LANG", "C.UTF-8")
	marker := filepath.Join(t.TempDir(), "seen")
	p := fakeGo(t, "{ echo \"args=$*\"; echo \"pwd=$(pwd)\"; env; } > '"+marker+"'\necho 'go version go1.24.7 linux/amd64'\n")
	if _, err := inspector(p).Inspect(context.Background(), core.Requirement{Name: "go"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(marker)
	seen := string(data)
	for _, want := range []string{"args=version\n", "pwd=/\n", "\nGOTOOLCHAIN=local\n", "\nGOENV=off\n", "\nTEST_TELEMETRY_DIR=/dev/null/", "\nLANG=C.UTF-8\n", "\nPATH=" + filepath.Dir(p) + ":"} {
		if !strings.Contains("\n"+seen, want) {
			t.Errorf("probe did not see %q:\n%s", want, seen)
		}
	}
	for _, banned := range []string{"/evil", "GOFLAGS", "GOROOT", "GOPATH", "go1.99.0"} {
		if strings.Contains(seen, banned) {
			t.Errorf("%q reached the probe:\n%s", banned, seen)
		}
	}
}

func TestGoEnvForcesToolchainLocalAndHome(t *testing.T) {
	env := goEnv("/usr/bin/go", "/home/u")
	want := map[string]bool{"HOME=/home/u": false, "GOTOOLCHAIN=local": false, "GOENV=off": false}
	for _, kv := range env {
		if _, ok := want[kv]; ok {
			want[kv] = true
		}
		if strings.HasPrefix(kv, "PATH=") && kv != "PATH="+strings.Join(toolpath.SystemDirs, ":") {
			t.Errorf("system binary PATH = %q", kv)
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("%s missing from %v", k, env)
		}
	}
}

// With the REAL go command: the version comes out right, and the probe
// writes nothing to the account's home (go 1.23+ otherwise creates
// telemetry counter files there, even for `go version`) — in a fresh
// home, and also when telemetry mode is already "on".
func TestRealGoCommandReportsItsVersionAndWritesNothing(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command on PATH")
	}
	goBin, _ = filepath.EvalSymlinks(goBin)
	want, err := exec.Command(goBin, "env", "GOVERSION").Output()
	if err != nil {
		t.Fatal(err)
	}
	wantV := strings.TrimPrefix(strings.TrimSpace(string(want)), "go")

	for _, mode := range []string{"", "on"} {
		home := t.TempDir()
		if mode != "" {
			cfg := filepath.Join(home, ".config", "go", "telemetry")
			if err := os.MkdirAll(cfg, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(cfg, "mode"), []byte(mode+" 2020-01-01\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		before := listFiles(t, home)
		out, err := probe.Run(context.Background(), probe.Spec{Path: goBin, Args: []string{"version"}, Dir: "/", Env: goEnv(goBin, home), Timeout: 20 * time.Second})
		if err != nil {
			t.Fatalf("real go version: %v", err)
		}
		if got := listFiles(t, home); strings.Join(got, "|") != strings.Join(before, "|") {
			t.Errorf("mode %q: the probe changed the home directory: before %v, after %v", mode, before, got)
		}
		if !strings.Contains(out, "go"+wantV) {
			t.Errorf("output %q does not contain go%s", out, wantV)
		}
	}

	// Through the inspector (home = the account's real home, untouched here).
	g := &GoInspector{resolvePath: func(string) (string, error) { return goBin, nil }, runDir: "/", timeout: 20 * time.Second}
	obs, err := g.Inspect(context.Background(), core.Requirement{Name: "go"})
	if err != nil || obs.Version != wantV || obs.Path != goBin {
		t.Errorf("inspector: %+v, %v; want version %s at %s", obs, err, wantV, goBin)
	}
}

func listFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil {
			out = append(out, p)
		}
		return nil
	})
	return out
}
