package schema

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/0xCHANDA/womm/internal/core"
)

func loadWithin(t *testing.T, path string, d time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, _, err := Load(path)
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		t.Fatalf("Load did not return within %s", d)
		return nil
	}
}

func TestLoadRefusesNonRegularFiles(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "womm.yaml")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	if err := loadWithin(t, fifo, 5*time.Second); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("FIFO: error = %v", err)
	}
	if err := loadWithin(t, dir, 5*time.Second); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("directory: error = %v", err)
	}
}

func TestLoadRefusesOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "womm.yaml")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := loadWithin(t, path, 10*time.Second); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("error = %v, want ErrTooLarge", err)
	}
}

func TestNamesMustBePlainIdentifiers(t *testing.T) {
	bad := []string{"no de", "no\nde", "no\tde", "node\x00", "\x1b[31mnode", " node", "no de"}
	for _, name := range bad {
		t.Run(name, func(t *testing.T) {
			y := "version: 1\nrequirements:\n  - name: " + yamlQuote(name) + "\n    constraint: '>=22'\n    evidence: [{source: a, field: b}]\n"
			_, _, err := Parse([]byte(y))
			if err == nil || !strings.Contains(err.Error(), "must not contain whitespace or control characters") {
				t.Fatalf("Parse accepted name %q: %v", name, err)
			}
			f := &File{Version: Version, Requirements: []core.Requirement{{Name: name, Constraint: ">=22", Evidence: []core.Evidence{{Source: "a", Field: "b"}}}}}
			if _, err := Marshal(f); err == nil {
				t.Fatalf("Marshal accepted name %q", name)
			}
		})
	}
	// YAML escapes always decode to valid UTF-8, so raw invalid bytes
	// can only arrive through the in-memory API: Marshal must refuse
	// them too.
	if validName("\xff\xfe") {
		t.Error("validName accepted invalid UTF-8")
	}
	if _, err := Marshal(&File{Version: Version, Requirements: []core.Requirement{{Name: "\xff\xfe", Constraint: ">=22", Evidence: []core.Evidence{{Source: "a", Field: "b"}}}}}); err == nil {
		t.Error("Marshal accepted an invalid UTF-8 name")
	}
	for _, name := range []string{"node", "pnpm", "my-tool", "tool_2", "ñode", "日本"} {
		if !validName(name) {
			t.Errorf("validName(%q) = false, want true", name)
		}
	}
}

// yamlQuote writes name as a double-quoted YAML scalar with escapes,
// so control characters survive the round trip into the parser.
func yamlQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range []byte(s) {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteByte(r)
		case r < 0x20 || r >= 0x7f:
			b.WriteString(`\x`)
			b.WriteByte("0123456789abcdef"[r>>4])
			b.WriteByte("0123456789abcdef"[r&0xf])
		default:
			b.WriteByte(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func FuzzParse(f *testing.F) {
	seeds := []string{
		"version: 1\nrequirements: []\n",
		"version: 1\nrequirements:\n  - name: node\n    constraint: '>=22'\n    evidence: [{source: a, field: b, value: c}]\n",
		"version: 2\n", "version: x\n", "requirements: []\n", "[", "version: 1\na: &a [*a]\n",
		"version: 1\nrequirements:\n  - name: \"no\\nde\"\n    constraint: x\n    evidence: [{source: a, field: b}]\n",
		"version: 1\nservices:\n  redis:\n    version: '>=7'\n    port: 6379\nenvironment:\n  required: [A]\n",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		file, _, err := Parse(data)
		if err != nil {
			if file != nil {
				t.Fatalf("Parse returned a file alongside an error")
			}
			return
		}
		// Anything Parse accepts must validate, serialize and parse
		// back to an equivalent document.
		if err := Validate(file); err != nil {
			t.Fatalf("Parse accepted a file Validate rejects: %v", err)
		}
		out, err := Marshal(file)
		if err != nil {
			t.Fatalf("Marshal rejected a parsed file: %v", err)
		}
		again, _, err := Parse(out)
		if err != nil {
			t.Fatalf("Marshal output does not parse: %v\n%s", err, out)
		}
		if len(again.Requirements) != len(file.Requirements) {
			t.Fatalf("round trip changed requirement count")
		}
		out2, _ := Marshal(again)
		if string(out2) != string(out) {
			t.Fatalf("Marshal is not a fixed point:\n%s\n---\n%s", out, out2)
		}
	})
}

func TestLoadRefusesSymlink(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "secret.yml")
	if err := os.WriteFile(outside, []byte("github.com:\n  oauth_token: hunter2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	link := filepath.Join(dir, "womm.yaml")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	_, warnings, err := Load(link)
	if !errors.Is(err, ErrSymlink) {
		t.Fatalf("error = %v, want ErrSymlink", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("a refused file must not leak its keys through warnings: %v", warnings)
	}
	if !strings.Contains(err.Error(), "symbolic link") || strings.Contains(err.Error(), "oauth") {
		t.Errorf("error = %q", err)
	}
}

// TestLoadUnderFIFOSwapNeverHangs: Load must not block when womm.yaml
// is swapped to a FIFO between any check and the open.
func TestLoadUnderFIFOSwapNeverHangs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "womm.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nrequirements: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
			if i%2 == 0 {
				_ = os.WriteFile(path, []byte("version: 1\nrequirements: []\n"), 0o644)
			} else {
				_ = syscall.Mkfifo(path, 0o644)
			}
		}
	}()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 3000; i++ {
			_, _, _ = Load(path)
		}
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Load blocked under a FIFO swap")
	}
	close(stop)
	<-swapperDone
}
