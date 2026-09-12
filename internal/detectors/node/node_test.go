package node

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func fx(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("testdata", name)
}

func TestNodeDetectorEnginesOnly(t *testing.T) {
	reqs, err := NewNodeDetector().Detect(context.Background(), fx(t, "node-engines"))
	if err != nil {
		t.Fatalf("engines detection failed: %v", err)
	}
	if len(reqs) != 1 {
		t.Fatalf("got %d requirements, want 1", len(reqs))
	}
	r := reqs[0]
	if r.Name != "node" || r.Constraint != ">=22 <25" {
		t.Errorf("requirement = %+v", r)
	}
	if len(r.Evidence) != 1 {
		t.Fatalf("evidence count = %d", len(r.Evidence))
	}
	ev := r.Evidence[0]
	if ev.Source != "package.json" || ev.Field != "engines.node" || ev.Value != ">=22 <25" {
		t.Errorf("evidence = %+v, want package.json/engines.node/\">=22 <25\"", ev)
	}
}

func TestNodeDetectorNvmrcOnly(t *testing.T) {
	reqs, err := NewNodeDetector().Detect(context.Background(), fx(t, "node-nvmrc"))
	if err != nil {
		t.Fatalf("nvmrc detection failed: %v", err)
	}
	if len(reqs) != 1 {
		t.Fatalf("got %d requirements, want 1", len(reqs))
	}
	r := reqs[0]
	if r.Name != "node" || r.Constraint != "22.14.0" {
		t.Errorf("requirement = %+v", r)
	}
	if ev := r.Evidence[0]; ev.Source != ".nvmrc" || ev.Field != "version" || ev.Value != "22.14.0" {
		t.Errorf("evidence = %+v, want .nvmrc/version/22.14.0", ev)
	}
}

func TestPackageManagerDetector(t *testing.T) {
	cases := []struct {
		name      string
		fixture   string
		wantName  string
		wantConst string
	}{
		{"pnpm", "node-package-manager", "pnpm", "10.15.1"},
	}
	// npm and yarn validated through inline fixtures below (one
	// behavior per fixture directory; makeTemp keeps tests cheap).
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reqs, err := NewPackageManagerDetector().Detect(context.Background(), fx(t, tc.fixture))
			if err != nil {
				t.Fatalf("detect: %v", err)
			}
			if len(reqs) != 1 {
				t.Fatalf("got %d requirements, want 1", len(reqs))
			}
			r := reqs[0]
			if r.Name != tc.wantName || r.Constraint != tc.wantConst {
				t.Errorf("requirement = %+v, want %s@%s", r, tc.wantName, tc.wantConst)
			}
			ev := r.Evidence[0]
			if ev.Source != "package.json" || ev.Field != "packageManager" || ev.Value != tc.wantName+"@"+tc.wantConst {
				t.Errorf("evidence = %+v, want literal packageManager value", ev)
			}
		})
	}
}

func TestPackageManagerDetectorInline(t *testing.T) {
	cases := []struct {
		name      string
		json      string
		wantName  string
		wantConst string
		wantErr   bool
	}{
		{"npm", `{"packageManager": "npm@11.2.0"}`, "npm", "11.2.0", false},
		{"yarn", `{"packageManager": "yarn@4.6.0"}`, "yarn", "4.6.0", false},
		{"lockfile-like misleading value", `{"packageManager": "corepack@3.0.0"}`, "", "", true},
		{"invalid format", `{"packageManager": "just-a-name"}`, "", "", true},
		{"unknown manager", `{"packageManager": "bun@1.2.3"}`, "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := writeFile(dir, "package.json", tc.json); err != nil {
				t.Fatal(err)
			}
			reqs, err := NewPackageManagerDetector().Detect(context.Background(), dir)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", reqs)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(reqs) != 1 || reqs[0].Name != tc.wantName || reqs[0].Constraint != tc.wantConst {
				t.Errorf("requirement mismatch: %+v", reqs)
			}
		})
	}
}

func TestNodeDetectorCompatibleEvidence(t *testing.T) {
	// engines.node >=20 and .nvmrc 22.14.0 are semantically compatible;
	// they must NOT be reported as a conflict, and both evidence
	// entries must exist (differing constraints stay separate).
	reqs, err := NewNodeDetector().Detect(context.Background(), fx(t, "node-compatible-evidence"))
	if err != nil {
		t.Fatalf("compatible evidence rejected: %v", err)
	}
	var total int
	for _, r := range reqs {
		if r.Name == "node" {
			total += len(r.Evidence)
		}
	}
	if total != 2 {
		t.Errorf("node evidence count = %d, want 2 (engines + nvmrc)", total)
	}
}

func TestNodeDetectorConflict(t *testing.T) {
	_, err := NewNodeDetector().Detect(context.Background(), fx(t, "node-conflict"))
	if err == nil {
		t.Fatal("expected EVIDENCE_CONFLICT error, got nil")
	}
	if !errors.Is(err, ErrEvidenceConflict) {
		t.Errorf("expected ErrEvidenceConflict family, got %v", err)
	}
	if !strings.Contains(err.Error(), "no side is selected") {
		t.Errorf("conflict error must refuse to pick a side: %v", err)
	}
}

func TestNodeDetectorAdversarialRanges(t *testing.T) {
	// Inline fixtures: one decision per case. These pin the semver
	// compatibility semantics that decide EVIDENCE_CONFLICT.
	cases := []struct {
		name       string
		engines    string
		nvmrc      string
		wantBroken bool // true = must produce conflict / unsupported error
	}{
		{">=20 + 22.14.0", ">=20", "22.14.0", false},
		{">=22 <25 + 24.1.0", ">=22 <25", "24.1.0", false},
		{">=22 + 20.0.0 (conflict)", ">=22", "20.0.0", true},
		{"20.x + 22.0.0 (conflict)", "20.x", "22.0.0", true},
		{"unparseable nvmrc (lts) → explicit error", ">=20", "lts/iron", true},
		{"unparseable engines (weird) → explicit error", "latest", "20.0.0", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := writeFile(dir, "package.json", `{"engines": {"node": "`+tc.engines+`"}}`); err != nil {
				t.Fatal(err)
			}
			if tc.nvmrc != "" {
				if err := writeFile(dir, ".nvmrc", tc.nvmrc+"\n"); err != nil {
					t.Fatal(err)
				}
			}
			_, err := NewNodeDetector().Detect(context.Background(), dir)
			if tc.wantBroken && err == nil {
				t.Error("expected error (conflict or unsupported), got nil")
			}
			if !tc.wantBroken && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestNodeDetectorMissingAndEmptySources(t *testing.T) {
	t.Run("both sources absent", func(t *testing.T) {
		reqs, err := NewNodeDetector().Detect(context.Background(), t.TempDir())
		if err != nil {
			t.Fatalf("absence must not be an error: %v", err)
		}
		if len(reqs) != 0 {
			t.Errorf("expected 0 requirements, got %+v", reqs)
		}
	})
	t.Run("malformed package.json", func(t *testing.T) {
		reqs, err := NewNodeDetector().Detect(context.Background(), fx(t, "node-invalid-package"))
		if err == nil {
			t.Fatalf("malformed declared source must fail, got %+v", reqs)
		}
		if !strings.Contains(err.Error(), "malformed package.json") {
			t.Errorf("error must name the real problem: %v", err)
		}
	})
	t.Run("no explicit node requirements", func(t *testing.T) {
		reqs, err := NewNodeDetector().Detect(context.Background(), fx(t, "node-no-requirements"))
		if err != nil {
			t.Fatalf("no-requirements must not be an error: %v", err)
		}
		if len(reqs) != 0 {
			t.Errorf("expected 0 requirements, got %+v", reqs)
		}
	})
	t.Run("empty and whitespace-only nvmrc", func(t *testing.T) {
		for _, content := range []string{"", "\n\n", "   \n"} {
			dir := t.TempDir()
			if err := writeFile(dir, ".nvmrc", content); err != nil {
				t.Fatal(err)
			}
			reqs, err := NewNodeDetector().Detect(context.Background(), dir)
			if err != nil {
				t.Fatalf("empty/whitespace nvmrc must not error: %v", err)
			}
			if len(reqs) != 0 {
				t.Errorf("expected no requirements for %q, got %+v", content, reqs)
			}
		}
	})
}

func TestPackageManagerMissingSources(t *testing.T) {
	t.Run("no package.json", func(t *testing.T) {
		reqs, err := NewPackageManagerDetector().Detect(context.Background(), t.TempDir())
		if err != nil {
			t.Fatalf("absence must not be an error: %v", err)
		}
		if len(reqs) != 0 {
			t.Errorf("expected 0 requirements, got %+v", reqs)
		}
	})
	t.Run("malformed package.json", func(t *testing.T) {
		dir := fx(t, "node-invalid-package")
		if _, err := NewPackageManagerDetector().Detect(context.Background(), dir); err == nil {
			t.Fatal("malformed package.json must fail")
		}
	})
}
