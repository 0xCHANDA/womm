package compare

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xCHANDA/womm/internal/core"
)

func goMatch(constraint, version string, present bool) (core.Match, error) {
	return Compare(core.Requirement{Name: "go", Constraint: constraint}, core.Observation{Name: "go", Present: present, Version: version})
}

func TestCompareGoUsesGoOrderNotSemver(t *testing.T) {
	cases := []struct {
		constraint, observed string
		want                 core.MatchStatus
	}{
		{">=1.21", "1.21.0", core.StatusPass},
		{">=1.21", "1.21rc1", core.StatusPass},   // rc1 handles `go 1.21`...
		{">=1.21.0", "1.21rc1", core.StatusFail}, // ...but not `go 1.21.0`
		{">=1.21", "1.20.9", core.StatusFail},
		{">=1.21", "1.22.0", core.StatusPass},
		{">=1.24", "1.24.7", core.StatusPass},
		{">=1.24.8", "1.24.7", core.StatusFail},
		{">=1.9", "1.10.0", core.StatusPass}, // numeric, not lexical
		{">=1.20", "1.20", core.StatusPass},  // pre-1.21 releases have no .0
		{">=1.20.0", "1.20", core.StatusPass},
		{">=1.22rc1", "1.22.0", core.StatusPass},
		{">=1.22rc2", "1.22rc1", core.StatusFail},
		{">=1.22", "1.22beta1", core.StatusPass}, // 1.22 < 1.22beta1 in Go's order
		{">=1.100", "1.99.0", core.StatusFail},
	}
	for _, c := range cases {
		m, err := goMatch(c.constraint, c.observed, true)
		if err != nil || m.Status != c.want {
			t.Errorf("%s vs %s: %q (err %v), want %q", c.constraint, c.observed, m.Status, err, c.want)
		}
		if m.Observation.Version != c.observed || m.Requirement.Constraint != c.constraint {
			t.Errorf("%s vs %s: match does not carry its inputs", c.constraint, c.observed)
		}
	}
}

func TestCompareGoRefusesWhatItCannotDecide(t *testing.T) {
	for _, c := range []string{"1.21", ">= 1.21", ">1.21", "^1.21", ">=1.21 <1.23", ">=go1.21", ">=1", ">=1.21-bigcorp", "=1.21", "", ">=1.21foo1", "20.x", ">=22"} {
		m, err := goMatch(c, "1.24.7", true)
		if m.Status != core.StatusUnknown || !errors.Is(err, ErrInvalidConstraint) {
			t.Errorf("constraint %q: %q, %v; want unknown + ErrInvalidConstraint", c, m.Status, err)
		}
	}
	for _, v := range []string{"go1.24.7", "1.24.7-bigcorp", "1", "v1.24.7", "1.24.7+meta", "devel", "1.21foo1", " 1.24"} {
		m, err := goMatch(">=1.21", v, true)
		if m.Status != core.StatusUnknown || !errors.Is(err, ErrInvalidObservedVersion) {
			t.Errorf("observed %q: %q, %v; want unknown + ErrInvalidObservedVersion", v, m.Status, err)
		}
	}
}

func TestCompareGoAbsentUnknownVersionAndPresent(t *testing.T) {
	if m, err := goMatch(">=1.21", "", false); err != nil || m.Status != core.StatusFail {
		t.Errorf("absent: %q, %v", m.Status, err)
	}
	if m, err := goMatch(">=1.21", "", true); err != nil || m.Status != core.StatusUnknown {
		t.Errorf("unknown version: %q, %v", m.Status, err)
	}
	if m, err := goMatch("present", "", true); err != nil || m.Status != core.StatusPass {
		t.Errorf("present: %q, %v", m.Status, err)
	}
	if m, err := goMatch(">=1.21", "1.24.7", false); !errors.Is(err, ErrInconsistentObservation) || m.Status != core.StatusUnknown {
		t.Errorf("contradictory: %q, %v", m.Status, err)
	}
}

// The name selects the grammar: the same text means different things for
// node and for go, and a go requirement never goes through npm's parser.
func TestCompareGrammarIsChosenByRequirementName(t *testing.T) {
	node, _ := Compare(core.Requirement{Name: "node", Constraint: ">=1.21"}, core.Observation{Name: "node", Present: true, Version: "1.21.0"})
	if node.Status != core.StatusPass {
		t.Fatalf("node baseline: %q", node.Status)
	}
	if m, err := Compare(core.Requirement{Name: "go", Constraint: ">=1.21"}, core.Observation{Name: "go", Present: true, Version: "1.21rc1"}); err != nil || m.Status != core.StatusPass {
		t.Errorf("go rc: %q, %v (npm's prerelease refusal must not apply to Go)", m.Status, err)
	}
	if m, _ := Compare(core.Requirement{Name: "node", Constraint: ">=1.21"}, core.Observation{Name: "node", Present: true, Version: "1.21.0-rc.1"}); m.Status != core.StatusUnknown {
		t.Errorf("node prerelease against a range must stay refused: %q", m.Status)
	}
}

// Differential test against the real go command: for each `go X` line,
// does the installed toolchain (GOTOOLCHAIN=local) accept the module?
// WOMM's verdict for observed = that toolchain's version must agree.
func TestCompareGoAgreesWithTheGoCommandOnItsOwnVersion(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command")
	}
	vout, err := exec.Command(goBin, "env", "GOVERSION").Output()
	if err != nil {
		t.Fatal(err)
	}
	local := strings.TrimPrefix(strings.TrimSpace(string(vout)), "go")
	t.Logf("local toolchain: %s", local)

	var reqs []string
	for minor := 0; minor <= 30; minor++ {
		reqs = append(reqs, fmt.Sprintf("1.%d", minor))
		if minor >= 21 {
			reqs = append(reqs, fmt.Sprintf("1.%d.0", minor), fmt.Sprintf("1.%d.3", minor), fmt.Sprintf("1.%drc1", minor), fmt.Sprintf("1.%dbeta1", minor), fmt.Sprintf("1.%dalpha2", minor))
		}
	}
	reqs = append(reqs, local, local+".0", "1.24.0", "1.24.7", "1.24.8", "1.24rc1", "1.25rc1", "1.99")
	checked, accepted := 0, 0
	for _, r := range reqs {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module m\ngo "+r+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(goBin, "list", "-m")
		cmd.Dir = dir
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "GOTOOLCHAIN=local", "GOFLAGS=", "GOPROXY=off", "GOENV=off", "GOCACHE=" + filepath.Join(dir, ".c"), "GOTELEMETRY=off"}
		var errb bytes.Buffer
		cmd.Stderr = &errb
		runErr := cmd.Run()
		var goAccepts bool
		switch {
		case runErr == nil:
			goAccepts = true
		case strings.Contains(errb.String(), "requires go >="):
			goAccepts = false
		case strings.Contains(errb.String(), "errors parsing go.mod"):
			continue // not a version go.mod accepts at all; WOMM's gate is stricter or equal
		default:
			t.Fatalf("go list -m failed for another reason on go %s: %v\n%s", r, runErr, errb.String())
		}
		checked++
		m, cerr := goMatch(">="+r, local, true)
		if cerr != nil {
			// WOMM refused the spelling (stricter gate): never PASS-guess.
			if m.Status != core.StatusUnknown {
				t.Errorf("go %s: refusal with status %q", r, m.Status)
			}
			continue
		}
		accepted++
		if (m.Status == core.StatusPass) != goAccepts {
			t.Errorf("go %s on %s: WOMM says %q, the go command %s the module", r, local, m.Status, map[bool]string{true: "accepts", false: "rejects"}[goAccepts])
		}
	}
	if checked < 60 || accepted < 60 {
		t.Fatalf("only %d checked / %d decided; the matrix is not exercising anything", checked, accepted)
	}
	t.Logf("%d versions compared against the go command, %d decided by WOMM", checked, accepted)
}
