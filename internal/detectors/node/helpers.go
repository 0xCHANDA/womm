package node

import (
	"errors"
	"fmt"
	"io"
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
// ordinary project files). A missing source is absence (no requirement,
// no error); an EXISTING broken symlink is an explicit source error —
// the entry declares a source that cannot be read, and faking absence
// would hide the problem silently.
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

	// Only regular files are sources. A FIFO or device planted under a
	// declared name would block the read (and WOMM) forever; a
	// directory is not a declaration either.
	info, err := os.Stat(target)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("cannot read %s: %w", full, err)
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("%s is not a regular file (%s); refusing to read it", full, info.Mode().Type())
	}

	data, err := readBounded(target, maxSourceBytes)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("cannot read %s: %w", full, err)
	}
	return data, true, nil
}

// maxSourceBytes bounds every declared-source read. package.json and
// .nvmrc are small by nature; a project cannot make WOMM allocate
// arbitrary memory by growing them.
const maxSourceBytes = 16 << 20

// errSourceTooLarge marks a declared source above maxSourceBytes.
var errSourceTooLarge = errors.New("declared source exceeds the size limit")

// readBounded reads at most limit bytes from path and fails
// explicitly when the file is larger, instead of truncating it (a
// truncated declaration would be interpreted as something else).
func readBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w (%d bytes)", errSourceTooLarge, limit)
	}
	return data, nil
}
