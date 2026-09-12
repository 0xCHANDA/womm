package node

import (
	"os"
	"path/filepath"
)

// writeFile creates (or overwrites) name inside dir with content,
// creating parent-friendly tests for inline fixtures.
func writeFile(dir, name, content string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}

// osSymlink is the test helper for symlink containment cases.
func osSymlink(target, link string) error {
	return os.Symlink(target, link)
}
