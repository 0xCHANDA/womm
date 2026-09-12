package node

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xCHANDA/womm/internal/core"
	"github.com/0xCHANDA/womm/internal/schema"
)

func fx(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("testdata", name)
}

// mustFile builds the schema-serializable representation of detector
// requirements, for the mandatory integration contract: a detector
// must never produce a File the schema rejects (duplicate names…).
func mustFile(t *testing.T, reqs []core.Requirement) *schema.File {
	t.Helper()
	return &schema.File{
		Version:      1,
		Requirements: reqs,
		Services:     map[string]schema.Service{},
	}
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
	if ev := r.Evidence[0]; ev.Source != "package.json" || ev.Field != "engines.node" || ev.Value != ">=22 <25" {
		t.Errorf("evidence = %+v", ev)
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

func TestNodeDetectorCompatibleEvidenceConjunction(t *testing.T) {
	// engines >=20 AND .nvmrc 22.14.0 is exactly Node ==22.14.0:
	// exactly ONE requirement named node, constraint 22.14.0, with
	// BOTH evidence entries. Not two requirements, no arbitrary pick.
	reqs, err := NewNodeDetector().Detect(context.Background(), fx(t, "node-compatible-evidence"))
	if err != nil {
		t.Fatalf("compatible evidence rejected: %v", err)
	}
	if len(reqs) != 1 {
		t.Fatalf("got %d requirements, want exactly 1 (no duplicate names)", len(reqs))
	}
	r := reqs[0]
	if r.Name != "node" || r.Constraint != "22.14.0" {
		t.Errorf("requirement = %+v, want node/22.14.0", r)
	}
	if len(r.Evidence) != 2 {
		t.Fatalf("evidence count = %d, want 2 (engines + nvmrc)", len(r.Evidence))
	}
	sources := []string{r.Evidence[0].Source, r.Evidence[1].Source}
	if sources[0] != "package.json" || sources[1] != ".nvmrc" {
		t.Errorf("evidence sources = %v, want [package.json .nvmrc]", sources)
	}
	if r.Evidence[1].Value != "22.14.0" {
		t.Errorf("nvmrc literal = %q, want 22.14.0", r.Evidence[1].Value)
	}
	// Mandatory integration: detector output must always satisfy the
	// schema contract (duplicate names are invalid).
	if err := schema.Validate(mustFile(t, reqs)); err != nil {
		t.Errorf("detector output invalid per schema: %v", err)
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

func TestPackageManagerDetector(t *testing.T) {
	reqs, err := NewPackageManagerDetector().Detect(context.Background(), fx(t, "node-package-manager"))
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(reqs) != 1 {
		t.Fatalf("got %d requirements, want 1", len(reqs))
	}
	r := reqs[0]
	if r.Name != "pnpm" || r.Constraint != "10.15.1" {
		t.Errorf("requirement = %+v", r)
	}
	ev := r.Evidence[0]
	if ev.Source != "package.json" || ev.Field != "packageManager" || ev.Value != "pnpm@10.15.1" {
		t.Errorf("evidence = %+v, want literal packageManager value", ev)
	}
}

func TestPackageManagerDetectorInline(t *testing.T) {
	cases := []struct {
		name      string
		json      string
		wantName  string
		wantConst string
		wantErr   string
	}{
		{"npm", `{"packageManager": "npm@11.2.0"}`, "npm", "11.2.0", ""},
		{"yarn", `{"packageManager": "yarn@4.6.0"}`, "yarn", "4.6.0", ""},
		{"corepack hash keeps literal", `{"packageManager": "yarn@3.2.3+sha224.953c8233f7a92884c3c2b30f2f1c130a80e07a22"}`, "yarn", "3.2.3", ""},
		{"garbage version", `{"packageManager": "pnpm@garbage"}`, "", "", "must be an exact version"},
		{"yarn@ empty version", `{"packageManager": "yarn@"}`, "", "", "invalid format"},
		{"tag not accepted", `{"packageManager": "npm@latest"}`, "", "", "must be an exact version"},
		{"unsupported name", `{"packageManager": "corepack@3.0.0"}`, "", "", "unsupported package manager"},
		{"corepack url", `{"packageManager": "yarn@https://example.com"}`, "", "", "not supported"},
	}
	const hashLiteral = "yarn@3.2.3+sha224.953c8233f7a92884c3c2b30f2f1c130a80e07a22"
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := writeFile(dir, "package.json", tc.json); err != nil {
				t.Fatal(err)
			}
			reqs, err := NewPackageManagerDetector().Detect(context.Background(), dir)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got %+v", tc.wantErr, reqs)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want containing %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			r := reqs[0]
			if r.Name != tc.wantName || r.Constraint != tc.wantConst {
				t.Errorf("requirement mismatch: %+v", r)
			}
			// Evidence.Value must be the verbatim declared literal —
			// for the Corepack hash case the constraint is stripped
			// but the evidence never is.
			wantLiteral := strings.TrimPrefix(tc.json, `{"packageManager": "`)
			wantLiteral = strings.TrimSuffix(wantLiteral, `"}`)
			if r.Evidence[0].Value != wantLiteral {
				t.Errorf("Evidence.Value = %q, want the verbatim %q", r.Evidence[0].Value, wantLiteral)
			}
			if tc.json == `{"packageManager": "`+hashLiteral+`"}` && r.Evidence[0].Value != hashLiteral {
				t.Errorf("hash literal lost: %q", r.Evidence[0].Value)
			}
		})
	}
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

func TestNvmrcParsing(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string // constraint; empty = expect error
		wantSub string // required error substring
	}{
		{"comment then version", "# Node para desarrollo\n22.14.0\n", "22.14.0", ""},
		{"key=value ignored", "FOO=bar\n22.14.0\n", "22.14.0", ""},
		{"v prefix", "v20.5.3\n", "20.5.3", ""},
		{"multiple selectors", "20.0.0\n22.0.0\n", "", "multiple version selectors"},
		{"short version unsupported", "22\n", "", "valid nvm syntax but is not supported"},
		{"partial version unsupported", "22.14\n", "", "valid nvm syntax but is not supported"},
		{"lts/* unsupported", "lts/*\n", "", "valid nvm syntax but is not supported"},
		{"lts alias unsupported", "lts/iron\n", "", "valid nvm syntax but is not supported"},
		{"node alias unsupported", "node\n", "", "valid nvm syntax but is not supported"},
		{"uninterpretable", "garbage-here\n", "", "cannot interpret"},
		{"comment-only file", "#yosolo comentario\n", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := writeFile(dir, ".nvmrc", tc.content); err != nil {
				t.Fatal(err)
			}
			reqs, err := NewNodeDetector().Detect(context.Background(), dir)
			if tc.wantSub != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got %+v", tc.wantSub, reqs)
				}
				if !strings.Contains(err.Error(), tc.wantSub) {
					t.Errorf("error = %q, want containing %q", err.Error(), tc.wantSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := ""
			if len(reqs) == 1 {
				got = reqs[0].Constraint
			}
			if got != tc.want {
				t.Errorf("constraint = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestShortNvmrcNeverSilentlyPinned(t *testing.T) {
	// "22" is valid nvm syntax; it must NOT be silently pinned to
	// 22.0.0 — it must be an explicit unsupported error.
	dir := t.TempDir()
	if err := writeFile(dir, ".nvmrc", "22\n"); err != nil {
		t.Fatal(err)
	}
	_, err := NewNodeDetector().Detect(context.Background(), dir)
	if err == nil {
		t.Fatal("expected explicit unsupported error")
	}
	if strings.Contains(err.Error(), "22.0.0") {
		t.Errorf("must not internpret as exact version: %v", err)
	}
}

func TestSymlinkContainment(t *testing.T) {
	t.Run("external symlink refused", func(t *testing.T) {
		outside := t.TempDir()
		if err := writeFile(outside, "secret-nvmrc", "22.14.0"); err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		if err := writeFile(dir, "package.json", `{"engines": {"node": ">=20"}}`); err != nil {
			t.Fatal(err)
		}
		if err := osSymlink(filepath.Join(outside, "secret-nvmrc"), filepath.Join(dir, ".nvmrc")); err != nil {
			t.Fatal(err)
		}
		_, err := NewNodeDetector().Detect(context.Background(), dir)
		if err == nil {
			t.Fatal("expected error for symlink escaping the project root")
		}
		if !strings.Contains(err.Error(), "escapes the project root") {
			t.Errorf("error = %q", err.Error())
		}
	})

	t.Run("internal symlink allowed", func(t *testing.T) {
		dir := t.TempDir()
		if err := writeFile(dir, "node-version", "22.14.0\n"); err != nil {
			t.Fatal(err)
		}
		if err := writeFile(dir, "package.json", `{"engines": {"node": ">=20"}}`); err != nil {
			t.Fatal(err)
		}
		if err := osSymlink(filepath.Join(dir, "node-version"), filepath.Join(dir, ".nvmrc")); err != nil {
			t.Fatal(err)
		}
		reqs, err := NewNodeDetector().Detect(context.Background(), dir)
		if err != nil {
			t.Fatalf("internal symlink must be allowed: %v", err)
		}
		if len(reqs) != 1 || reqs[0].Constraint != "22.14.0" || len(reqs[0].Evidence) != 2 {
			t.Errorf("requirements = %+v", reqs)
		}
	})

	t.Run("broken symlink treated as absent", func(t *testing.T) {
		dir := t.TempDir()
		if err := osSymlink(filepath.Join(dir, "nothing"), filepath.Join(dir, ".nvmrc")); err != nil {
			t.Fatal(err)
		}
		reqs, err := NewNodeDetector().Detect(context.Background(), dir)
		if err != nil {
			t.Fatalf("broken symlink must be treated as absent: %v", err)
		}
		if len(reqs) != 0 {
			t.Errorf("expected no requirements, got %+v", reqs)
		}
	})
}
