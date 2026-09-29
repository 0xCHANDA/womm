package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Masterminds/semver/v3"

	"github.com/0xCHANDA/womm/internal/core"
	"github.com/0xCHANDA/womm/internal/semverrange"
)

// detectWithin runs the Node detector and fails the test if it does not
// return within d: a blocked read is a hang, not a slow test.
func detectWithin(t *testing.T, dir string, d time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := NewNodeDetector().Detect(context.Background(), dir)
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		t.Fatalf("Detect did not return within %s (blocked read?)", d)
		return nil
	}
}

func TestDetectorRefusesFIFOSource(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "package.json"), 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	err := detectWithin(t, dir, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("error = %v, want a not-a-regular-file refusal", err)
	}
}

func TestDetectorRefusesDirectorySource(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".nvmrc"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := detectWithin(t, dir, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("error = %v, want a not-a-regular-file refusal", err)
	}
}

func TestDetectorRefusesOversizedSource(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Sparse: one byte past the limit without writing the payload.
	if err := f.Truncate(maxSourceBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	err = detectWithin(t, dir, 10*time.Second)
	if !errors.Is(err, errSourceTooLarge) {
		t.Fatalf("error = %v, want errSourceTooLarge", err)
	}

	// Exactly at the limit is read (and then fails as JSON, not size).
	if err := os.Truncate(filepath.Join(dir, "package.json"), maxSourceBytes); err != nil {
		t.Fatal(err)
	}
	err = detectWithin(t, dir, 10*time.Second)
	if err == nil || errors.Is(err, errSourceTooLarge) || !strings.Contains(err.Error(), "malformed package.json") {
		t.Fatalf("error = %v, want a malformed-JSON error at exactly the limit", err)
	}
}

func TestDetectorStripsUTF8BOM(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"engines":{"node":">=22 <25"},"packageManager":"npm@11.2.0"}`)...), 0o644); err != nil {
		t.Fatal(err)
	}
	reqs, err := NewNodeDetector().Detect(context.Background(), dir)
	if err != nil || len(reqs) != 1 || reqs[0].Constraint != ">=22 <25" {
		t.Fatalf("BOM package.json: reqs=%+v err=%v", reqs, err)
	}
	pm, err := NewPackageManagerDetector().Detect(context.Background(), dir)
	if err != nil || len(pm) != 1 || pm[0].Name != "npm" {
		t.Fatalf("BOM package.json (packageManager): reqs=%+v err=%v", pm, err)
	}

	// Only the exact UTF-8 BOM is stripped; other leading bytes stay
	// malformed JSON.
	if err := os.WriteFile(filepath.Join(dir, "package.json"), append([]byte{0xFF, 0xFE}, []byte(`{}`)...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewNodeDetector().Detect(context.Background(), dir); err == nil || !strings.Contains(err.Error(), "malformed package.json") {
		t.Fatalf("UTF-16 BOM: err=%v, want malformed", err)
	}
}

func TestDetectorNormalizesEnginesWhitespace(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(dir, "package.json", "{\"engines\":{\"node\":\"  >=22\\t<25 \\n||\\n ^20.10.0 \"}}"); err != nil {
		t.Fatal(err)
	}
	reqs, err := NewNodeDetector().Detect(context.Background(), dir)
	if err != nil || len(reqs) != 1 {
		t.Fatalf("reqs=%+v err=%v", reqs, err)
	}
	if got, want := reqs[0].Constraint, ">=22 <25 || ^20.10.0"; got != want {
		t.Errorf("constraint = %q, want normalized %q", got, want)
	}
	if got, want := reqs[0].Evidence[0].Value, "  >=22\t<25 \n||\n ^20.10.0 "; got != want {
		t.Errorf("evidence value = %q, want verbatim %q", got, want)
	}
}

func TestDetectorUnreadableSourceIsAnError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read anything; permission refusal cannot be exercised")
	}
	dir := t.TempDir()
	if err := writeFile(dir, ".nvmrc", "v24.7.0\n"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, ".nvmrc"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, ".nvmrc"), 0o644) })
	_, err := NewNodeDetector().Detect(context.Background(), dir)
	if err == nil || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("error = %v, want a permission error (never silent absence)", err)
	}
}

func FuzzNvmrcSelector(f *testing.F) {
	for _, s := range []string{"v24.7.0", "24.7.0", "22", "lts/*", "lts/iron", "node", "01.2.3", "24.7.0-beta.1", "", "v", "24.7.0.1", "≥22"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		got, err := nvmrcSelector(line)
		if err != nil {
			if got != "" {
				t.Fatalf("error with a non-empty result: %q", got)
			}
			return
		}
		// Accepted selectors are canonical strict versions.
		if !exactVersion.MatchString(got) || strings.HasPrefix(got, "v") {
			t.Fatalf("nvmrcSelector(%q) = %q, not a canonical exact version", line, got)
		}
		if again, _ := nvmrcSelector(line); again != got {
			t.Fatalf("not deterministic: %q vs %q", got, again)
		}
	})
}

func FuzzNvmrcFile(f *testing.F) {
	for _, s := range []string{"v24.7.0\n", "# c\n24.7.0\n", "22\n24\n", "KEY=v\n24.7.0\n", "", "\n\n", "lts/*\n", "24.7.0\r\n"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		c, err := nvmrcCandidate(data)
		if err != nil && c != nil {
			t.Fatalf("candidate returned with an error")
		}
		if err == nil && c == nil {
			t.Fatalf("a .nvmrc that yields nothing must be an error, got silent absence for %q", data)
		}
		if err == nil {
			if c.source != ".nvmrc" || c.field != "version" || c.constraint == "" {
				t.Fatalf("malformed candidate: %+v", c)
			}
		}
	})
}

func FuzzSplitPackageManager(f *testing.F) {
	for _, s := range []string{"npm@11.2.0", "pnpm@10.15.1+sha1.0123456789012345678901234567890123456789", "yarn@4.0.0-rc.1", "pnpm@10", "corepack@1", "yarn@https://x", "npm@", "@1.0.0", "npm@latest", "yarn@3.2.3+sha224." + strings.Repeat("a", 56), "npm@1.0.0+md5.abc"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, pm string) {
		name, version, err := splitPackageManager(pm)
		if err != nil {
			if name != "" || version != "" {
				t.Fatalf("error with non-empty results: %q %q", name, version)
			}
			return
		}
		if !packageManagerNames[name] {
			t.Fatalf("accepted unsupported name %q", name)
		}
		if strings.Contains(version, "+") || strings.HasPrefix(version, "v") {
			t.Fatalf("version %q is not canonical", version)
		}
	})
}

func FuzzPackageJSON(f *testing.F) {
	for _, s := range []string{`{}`, `{"engines":{"node":">=22 <25"}}`, `{"packageManager":"npm@11.2.0"}`, "\xef\xbb\xbf{}", `{"engines":{"node":">=22, <25"}}`, `{"engines":`, `[]`, `{"engines":{"node":1}}`, `{"engines":{"node":"lts/*"}}`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "package.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
		for _, d := range []interface {
			Detect(context.Context, string) ([]core.Requirement, error)
		}{NewNodeDetector(), NewPackageManagerDetector()} {
			reqs, err := d.Detect(context.Background(), dir)
			if err != nil {
				if reqs != nil {
					t.Fatalf("requirements returned with an error")
				}
				continue
			}
			for _, r := range reqs {
				if r.Name == "" || r.Constraint == "" || len(r.Evidence) == 0 {
					t.Fatalf("requirement without name/constraint/evidence: %+v", r)
				}
				if _, perr := semverrange.Parse(r.Constraint); perr != nil {
					if _, verr := semver.StrictNewVersion(r.Constraint); verr != nil {
						t.Fatalf("emitted constraint %q is neither a range nor an exact version", r.Constraint)
					}
				}
			}
		}
	})
}
