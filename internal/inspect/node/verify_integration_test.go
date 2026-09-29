package node

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/0xCHANDA/womm/internal/core"
	"github.com/0xCHANDA/womm/internal/inspect"
	"github.com/0xCHANDA/womm/internal/schema"
	"github.com/0xCHANDA/womm/internal/verify"
)

// TestVerifyThroughRealInspector runs the real NodeInspector (with the
// same-package resolver/timeout overrides) through verify.Verify, so
// the unreachable contract is exercised end to end on genuine probe
// failures rather than on fakes: a hanging binary and a crashing
// binary are UNREACHABLE, an absent one is FAIL, a healthy one PASSes,
// and the exit code follows the 3-over-1 precedence.
func TestVerifyThroughRealInspector(t *testing.T) {
	dir := t.TempDir()
	node := writeFakeTool(t, dir, "node", "echo v24.7.0\n")
	npm := writeFakeTool(t, dir, "npm", "sleep 30\n")
	yarn := writeFakeTool(t, dir, "yarn", "echo broken >&2\nexit 3\n")
	insp := &NodeInspector{
		resolvePath: staticResolver(map[string]string{"node": node, "npm": npm, "yarn": yarn}),
		runDir:      t.TempDir(),
		timeout:     100 * time.Millisecond,
	}
	ev := []core.Evidence{{Source: "womm.yaml", Field: "requirements"}}
	f := &schema.File{Version: schema.Version, Requirements: []core.Requirement{
		{Name: "yarn", Constraint: "present", Evidence: ev},
		{Name: "pnpm", Constraint: "10.15.1", Evidence: ev},
		{Name: "npm", Constraint: ">=10", Evidence: ev},
		{Name: "node", Constraint: ">=22 <25", Evidence: ev},
	}}

	res := verify.Verify(context.Background(), f, []inspect.Inspector{insp})
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected operational errors: %v", res.Errors)
	}
	want := map[string]core.MatchStatus{
		"node": core.StatusPass,
		"npm":  core.StatusUnreachable,
		"pnpm": core.StatusFail,
		"yarn": core.StatusUnreachable,
	}
	if len(res.Matches) != len(want) {
		t.Fatalf("got %d matches, want %d: %+v", len(res.Matches), len(want), res.Matches)
	}
	for _, m := range res.Matches {
		if m.Status != want[m.Requirement.Name] {
			t.Errorf("%s: status %q, want %q (reason: %s)", m.Requirement.Name, m.Status, want[m.Requirement.Name], m.Reason)
		}
		if m.Status == core.StatusUnreachable {
			if !m.Observation.Present || m.Observation.Version != "" {
				t.Errorf("%s: unreachable observation = %#v", m.Requirement.Name, m.Observation)
			}
		}
	}
	npmMatch := res.Matches[1]
	if !strings.Contains(npmMatch.Reason, "timed out") {
		t.Errorf("npm reason should mention the timeout: %q", npmMatch.Reason)
	}
	yarnMatch := res.Matches[3]
	if !strings.Contains(yarnMatch.Reason, "version probe failed") {
		t.Errorf("yarn reason should mention the probe failure: %q", yarnMatch.Reason)
	}
	if code := verify.ExitCode(res); code != verify.ExitInconclusive {
		t.Errorf("ExitCode = %d, want 3 (unreachable beats the pnpm FAIL)", code)
	}
}
