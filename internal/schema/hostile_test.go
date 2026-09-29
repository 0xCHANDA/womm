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
