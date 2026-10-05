package source

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestReadIfPresentUnderSymlinkSwapNeverEscapes hammers the
// declared-source read while another goroutine swaps .nvmrc between a
// regular in-project file and a symlink to a file outside the project.
// Every read must either return the in-project content or refuse;
// outside content must never come back, and nothing may block.
func TestReadIfPresentUnderSymlinkSwapNeverEscapes(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("OUTSIDE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	link := filepath.Join(project, ".nvmrc")
	if err := os.WriteFile(link, []byte("INSIDE\n"), 0o644); err != nil {
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
			_ = os.Remove(link)
			switch i % 3 {
			case 0:
				_ = os.WriteFile(link, []byte("INSIDE\n"), 0o644)
			case 1:
				_ = os.Symlink(outside, link)
			case 2:
				_ = syscall.Mkfifo(link, 0o644)
			}
		}
	}()
	done := make(chan int, 1)
	go func() {
		escapes := 0
		for i := 0; i < 4000; i++ {
			data, _, err := ReadIfPresent(project, ".nvmrc")
			if err == nil && string(data) == "OUTSIDE\n" {
				escapes++
			}
		}
		done <- escapes
	}()
	select {
	case escapes := <-done:
		if escapes != 0 {
			t.Fatalf("outside content returned %d times under a symlink swap", escapes)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("ReadIfPresent blocked under a FIFO swap")
	}
	close(stop)
	<-swapperDone
}

func TestReadIfPresentAbsoluteInternalSymlink(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "real"), []byte("v24.7.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(project, "real"), filepath.Join(project, ".nvmrc")); err != nil {
		t.Fatal(err)
	}
	data, ok, err := ReadIfPresent(project, ".nvmrc")
	if err != nil || !ok || string(data) != "v24.7.0\n" {
		t.Fatalf("absolute symlink to an in-project file: data=%q ok=%v err=%v", data, ok, err)
	}
	// Absolute link whose target escapes: refused.
	outside := filepath.Join(t.TempDir(), "x")
	os.WriteFile(outside, []byte("x"), 0o644)
	os.Remove(filepath.Join(project, ".nvmrc"))
	os.Symlink(outside, filepath.Join(project, ".nvmrc"))
	if _, _, err := ReadIfPresent(project, ".nvmrc"); err == nil || !strings.Contains(err.Error(), "escapes the project root") {
		t.Fatalf("absolute escaping symlink: %v", err)
	}
}

func TestReadIfPresentAbsenceIsNotAnErrorButBrokenLinksAre(t *testing.T) {
	project := t.TempDir()
	if data, ok, err := ReadIfPresent(project, "go.mod"); err != nil || ok || data != nil {
		t.Fatalf("missing source: %q %v %v", data, ok, err)
	}
	if err := os.Symlink(filepath.Join(project, "gone"), filepath.Join(project, "go.mod")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadIfPresent(project, "go.mod"); err == nil || !strings.Contains(err.Error(), "broken symlink") {
		t.Fatalf("broken symlink: %v", err)
	}
}

func TestReadIfPresentRefusesNonRegularAndOversizedSources(t *testing.T) {
	project := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(project, "fifo"), 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, _, err := ReadIfPresent(project, "fifo"); done <- err }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("FIFO: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a FIFO blocked the read")
	}
	if err := os.Mkdir(filepath.Join(project, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadIfPresent(project, "dir"); err == nil {
		t.Fatal("a directory was read as a source")
	}
	big := filepath.Join(project, "big")
	f, _ := os.Create(big)
	if err := f.Truncate(MaxBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, _, err := ReadIfPresent(project, "big"); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("oversized: %v", err)
	}
}

func TestReadIfPresentMaxIsATighterBoundAndNeverAWiderOne(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "a"), []byte(strings.Repeat("x", 100)), 0o644); err != nil {
		t.Fatal(err)
	}
	if data, ok, err := ReadIfPresentMax(project, "a", 100); err != nil || !ok || len(data) != 100 {
		t.Fatalf("at the limit: %d %v %v", len(data), ok, err)
	}
	if _, _, err := ReadIfPresentMax(project, "a", 99); err == nil || !strings.Contains(err.Error(), "size limit") || !strings.Contains(err.Error(), "99") {
		t.Fatalf("over the limit: %v", err)
	}
	// A bound above MaxBytes is clamped, never honoured.
	f, _ := os.Create(filepath.Join(project, "big"))
	if err := f.Truncate(MaxBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, _, err := ReadIfPresentMax(project, "big", MaxBytes*10); err == nil {
		t.Fatal("a limit above MaxBytes widened the bound")
	}
}
