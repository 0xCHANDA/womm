package capture

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/0xCHANDA/womm/internal/core"
	"github.com/0xCHANDA/womm/internal/detectors"
	"github.com/0xCHANDA/womm/internal/schema"
)

func fixture(name string) string { return filepath.Join("testdata", name) }

const wantFull = `version: 1
requirements:
  - name: node
    constraint: 24.7.0
    evidence:
      - source: package.json
        field: engines.node
        value: '>=22 <25'
      - source: .nvmrc
        field: version
        value: v24.7.0
  - name: pnpm
    constraint: 10.15.1
    evidence:
      - source: package.json
        field: packageManager
        value: pnpm@10.15.1+sha1.0123456789012345678901234567890123456789
`

func TestCaptureFixtures(t *testing.T) {
	cases := []struct {
		fixture  string
		wantYAML string
		wantErr  string
	}{
		{fixture: "node-full", wantYAML: wantFull},
		{fixture: "engines-only", wantYAML: "version: 1\nrequirements:\n  - name: node\n    constraint: ^20.10.0 || >=22\n    evidence:\n      - source: package.json\n        field: engines.node\n        value: ^20.10.0 || >=22\n"},
		{fixture: "no-requirements", wantYAML: "version: 1\nrequirements: []\n"},
		{fixture: "empty", wantYAML: "version: 1\nrequirements: []\n"},
		{fixture: "conflict", wantErr: "evidence conflict"},
		{fixture: "malformed", wantErr: "malformed package.json"},
		{fixture: "unsupported-selector", wantErr: `"lts/*" is valid nvm syntax but is not supported`},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			f, err := Capture(context.Background(), fixture(tc.fixture))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
				}
				if f != nil {
					t.Errorf("a failed capture must not return a file: %+v", f)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			data, err := schema.Marshal(f)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tc.wantYAML {
				t.Errorf("captured womm.yaml mismatch\n--- got ---\n%s--- want ---\n%s", data, tc.wantYAML)
			}
		})
	}
}

func TestCaptureIsDeterministic(t *testing.T) {
	var outputs [][]byte
	for i := 0; i < 3; i++ {
		f, err := Capture(context.Background(), fixture("node-full"))
		if err != nil {
			t.Fatal(err)
		}
		data, err := schema.Marshal(f)
		if err != nil {
			t.Fatal(err)
		}
		outputs = append(outputs, data)
	}
	for i := 1; i < len(outputs); i++ {
		if !bytes.Equal(outputs[0], outputs[i]) {
			t.Fatalf("run %d differs from run 0:\n%s\n---\n%s", i, outputs[i], outputs[0])
		}
	}
}

func TestCaptureRejectsNonDirectories(t *testing.T) {
	if _, err := Capture(context.Background(), filepath.Join(fixture("node-full"), "package.json")); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("error = %v, want not a directory", err)
	}
	if _, err := Capture(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error = %v, want ErrNotExist", err)
	}
}

func TestCaptureRefusesSymlinkEscape(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("v24.7.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(project, ".nvmrc")); err != nil {
		t.Fatal(err)
	}
	_, err := Capture(context.Background(), project)
	if err == nil || !strings.Contains(err.Error(), "escapes the project root") {
		t.Fatalf("error = %v, want L0 containment refusal", err)
	}
}

func TestCaptureRefusesBrokenSymlink(t *testing.T) {
	project := t.TempDir()
	if err := os.Symlink(filepath.Join(project, "does-not-exist"), filepath.Join(project, ".nvmrc")); err != nil {
		t.Fatal(err)
	}
	_, err := Capture(context.Background(), project)
	if err == nil || !strings.Contains(err.Error(), "broken symlink") {
		t.Fatalf("error = %v, want broken symlink refusal", err)
	}
}

type fixedDetector struct {
	name string
	reqs []core.Requirement
}

func (d fixedDetector) Name() string { return d.name }
func (d fixedDetector) Detect(context.Context, string) ([]core.Requirement, error) {
	return d.reqs, nil
}

func TestCaptureRefusesDuplicateRequirementNames(t *testing.T) {
	ev := []core.Evidence{{Source: "a", Field: "b", Value: "c"}}
	dets := []detectors.Detector{
		fixedDetector{"one", []core.Requirement{{Name: "node", Constraint: ">=22", Evidence: ev}}},
		fixedDetector{"two", []core.Requirement{{Name: "node", Constraint: ">=23", Evidence: ev}}},
	}
	_, err := captureWith(context.Background(), fixture("empty"), dets)
	if !errors.Is(err, ErrDuplicateRequirement) {
		t.Fatalf("error = %v, want ErrDuplicateRequirement", err)
	}
}

func TestCaptureSortsByNameRegardlessOfDetectorOrder(t *testing.T) {
	ev := []core.Evidence{{Source: "a", Field: "b", Value: "c"}}
	dets := []detectors.Detector{
		fixedDetector{"z", []core.Requirement{{Name: "yarn", Constraint: "present", Evidence: ev}, {Name: "node", Constraint: ">=22", Evidence: ev}}},
		fixedDetector{"a", []core.Requirement{{Name: "npm", Constraint: "present", Evidence: ev}}},
	}
	f, err := captureWith(context.Background(), fixture("empty"), dets)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range f.Requirements {
		names = append(names, r.Name)
	}
	if got := strings.Join(names, ","); got != "node,npm,yarn" {
		t.Errorf("order = %s, want node,npm,yarn", got)
	}
}

func TestCaptureRefusesEvidencelessDetectorOutput(t *testing.T) {
	dets := []detectors.Detector{fixedDetector{"bad", []core.Requirement{{Name: "node", Constraint: ">=22"}}}}
	_, err := captureWith(context.Background(), fixture("empty"), dets)
	if err == nil || !strings.Contains(err.Error(), "evidence") {
		t.Fatalf("error = %v, want evidence validation failure", err)
	}
}

func captured(t *testing.T) *schema.File {
	t.Helper()
	f, err := Capture(context.Background(), fixture("node-full"))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestWriteCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := DefaultOutput(dir)
	data, err := Write(path, captured(t), false)
	if err != nil {
		t.Fatal(err)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != wantFull || !bytes.Equal(onDisk, data) {
		t.Errorf("file content mismatch:\n%s", onDisk)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm()&0o111 != 0 {
		t.Errorf("womm.yaml must not be executable: %v", fi.Mode())
	}
	loaded, _, err := schema.Load(path)
	if err != nil {
		t.Fatalf("written file does not load: %v", err)
	}
	if len(loaded.Requirements) != 2 {
		t.Errorf("loaded %d requirements, want 2", len(loaded.Requirements))
	}
}

func TestWriteRefusesExistingWithoutForce(t *testing.T) {
	dir := t.TempDir()
	path := DefaultOutput(dir)
	if err := os.WriteFile(path, []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Write(path, captured(t), false)
	if !errors.Is(err, ErrOutputExists) {
		t.Fatalf("error = %v, want ErrOutputExists", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "keep me\n" {
		t.Errorf("existing file was modified: %q", got)
	}
}

func TestWriteForceOverwritesRegularFile(t *testing.T) {
	dir := t.TempDir()
	path := DefaultOutput(dir)
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 10_000), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(path, captured(t), true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != wantFull {
		t.Errorf("force overwrite left stale bytes (truncate missing?):\n%s", got)
	}
}

func TestWriteRefusesSymlinkOutput(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-force", true: "force"}[force], func(t *testing.T) {
			victimDir := t.TempDir()
			victim := filepath.Join(victimDir, "victim")
			if err := os.WriteFile(victim, []byte("do not clobber\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			path := DefaultOutput(dir)
			if err := os.Symlink(victim, path); err != nil {
				t.Fatal(err)
			}
			_, err := Write(path, captured(t), force)
			if !errors.Is(err, ErrOutputSymlink) {
				t.Fatalf("error = %v, want ErrOutputSymlink", err)
			}
			if got, _ := os.ReadFile(victim); string(got) != "do not clobber\n" {
				t.Fatalf("symlink target was written through: %q", got)
			}
		})
	}
}

func TestWriteRefusesDanglingSymlinkOutput(t *testing.T) {
	dir := t.TempDir()
	path := DefaultOutput(dir)
	target := filepath.Join(dir, "elsewhere")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(path, captured(t), true); !errors.Is(err, ErrOutputSymlink) {
		t.Fatalf("error = %v, want ErrOutputSymlink", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("dangling symlink target was created: %v", err)
	}
}

func TestWriteRefusesNonRegularOutput(t *testing.T) {
	dir := t.TempDir()
	if _, err := Write(dir, captured(t), true); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("error = %v, want not a regular file", err)
	}
}

func TestWriteRefusesInvalidFile(t *testing.T) {
	dir := t.TempDir()
	path := DefaultOutput(dir)
	_, err := Write(path, &schema.File{Version: 7}, false)
	if err == nil {
		t.Fatal("invalid file written")
	}
	if _, serr := os.Lstat(path); !errors.Is(serr, os.ErrNotExist) {
		t.Errorf("invalid file left an output on disk: %v", serr)
	}
}

// TestWriteForceNeverWritesThroughHardLink: the previous O_TRUNC write
// modified every name of the inode; a womm.yaml hard-linked to another
// file would have rewritten that file. rename replaces the entry only.
func TestWriteForceNeverWritesThroughHardLink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("do not clobber\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := DefaultOutput(dir)
	if err := os.Link(victim, path); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(path, captured(t), true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(victim); string(got) != "do not clobber\n" {
		t.Fatalf("hard-link target was written through: %q", got)
	}
	if got, _ := os.ReadFile(path); string(got) != wantFull {
		t.Errorf("womm.yaml not replaced:\n%s", got)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".womm.yaml.tmp-") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
}

// TestWriteForceUnderSwapRaceNeverHangsOrEscapes hammers Write(force)
// while another goroutine swaps the target between a regular file, a
// symlink to a victim and a FIFO. Whatever the interleaving: no hang,
// the victim is never modified, and nothing is ever written through a
// FIFO (rename replaces the entry, it does not open it).
func TestWriteForceUnderSwapRaceNeverHangsOrEscapes(t *testing.T) {
	dir := t.TempDir()
	path := DefaultOutput(dir)
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("victim\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := captured(t)

	stop := make(chan struct{})
	swapperDone := make(chan struct{})
	go func() {
		defer close(swapperDone)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_ = os.Remove(path)
			switch i % 3 {
			case 0:
				_ = os.WriteFile(path, []byte("x"), 0o644)
			case 1:
				_ = os.Symlink(victim, path)
			case 2:
				_ = syscall.Mkfifo(path, 0o644)
			}
		}
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1500; i++ {
			_, _ = Write(path, f, true)
		}
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Write(force) hung under a swap race")
	}
	close(stop)
	// The swapper must be gone before t.TempDir's cleanup runs, or a
	// file created mid-RemoveAll makes the cleanup itself fail.
	<-swapperDone
	if got, _ := os.ReadFile(victim); string(got) != "victim\n" {
		t.Fatalf("symlink target modified under race: %q", got)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".womm.yaml.tmp-") {
			t.Errorf("temporary file leaked: %s", e.Name())
		}
	}
}

func TestWriteForceRefusesDirectoryTarget(t *testing.T) {
	dir := t.TempDir()
	path := DefaultOutput(dir)
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(path, captured(t), true); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("error = %v", err)
	}
}
