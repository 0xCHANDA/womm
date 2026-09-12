package node

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// readFileIfPresent returns (data, true, nil) when name exists inside
// projectRoot, or (nil, false, nil) when it does not. Other I/O
// failures propagate.
//
// L0 containment guarantee: os.ReadFile follows symlinks, so an
// untrusted project could point ".nvmrc" at a file outside the
// project (".nvmrc → /etc/passwd") and make WOMM read outside the
// declared root. This helper refuses that:
//
//  1. resolve projectRoot itself;
//  2. resolve the file's symlink target, if any;
//  3. verify the resolved target stays inside the root;
//  4. anything escaping the root aborts with an explicit error.
//
// Symlinks to targets INSIDE the project remain allowed (they are
// ordinary project files). Broken symlinks are treated as absent.
func readFileIfPresent(projectRoot, name string) ([]byte, bool, error) {
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, false, fmt.Errorf("cannot resolve project root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, false, fmt.Errorf("cannot resolve project root: %w", err)
	}

	full := filepath.Join(root, name)
	target := full
	if fi, lerr := os.Lstat(full); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
		// Only follow symlinks if they resolve under the root.
		resolved, serr := filepath.EvalSymlinks(full)
		if os.IsNotExist(serr) {
			// The filesystem entry EXISTS and declares itself as a
			// source: a broken symlink is a real, declared source we
			// cannot read. Faking absence would hide the problem
			// silently (false negative): report it explicitly.
			return nil, false, fmt.Errorf("%s is a broken symlink; the declared source exists but cannot be read", full)
		}
		if serr != nil {
			return nil, false, fmt.Errorf("cannot resolve %s: %w", full, serr)
		}
		target = resolved
	}

	if rel, rerr := filepath.Rel(root, target); rerr != nil ||
		rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) ||
		filepath.IsAbs(rel) {
		return nil, false, fmt.Errorf("%s escapes the project root; refusing to read outside the project (L0 contract)", full)
	}

	data, err := os.ReadFile(target)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("cannot read %s: %w", full, err)
	}
	return data, true, nil
}
