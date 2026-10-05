// Package source is the one contained, bounded way every L0 detector
// reads a declared project source (package.json, .nvmrc, go.mod, ...).
// Containment is race-free (os.Root), reads are non-blocking, regular-file
// only and size-bounded; see ReadIfPresent.
package source

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ReadIfPresent returns (data, true, nil) when name exists inside
// projectRoot, or (nil, false, nil) when it does not. Other I/O
// failures propagate.
//
// L0 containment guarantee, race-free: every path component is opened
// relative to the project root through os.Root (openat-style), so a
// symlink anywhere in the chain that resolves outside the root is
// refused by the kernel-backed check at open time — not by a path
// inspection that a concurrent swap could invalidate. Symlinks to
// targets INSIDE the project remain allowed (they are ordinary project
// files). A missing source is absence (no requirement, no error); an
// EXISTING broken symlink is an explicit source error — the entry
// declares a source that cannot be read, and faking absence would hide
// the problem silently.
//
// The open is non-blocking and the opened inode is checked with fstat:
// a FIFO, socket, device or directory planted (or swapped in) under a
// declared name can neither block WOMM nor be read as a declaration.
// Reads are bounded (MaxBytes); a larger source is an explicit
// error, never a truncated interpretation.
func ReadIfPresent(projectRoot, name string) ([]byte, bool, error) {
	return ReadIfPresentMax(projectRoot, name, MaxBytes)
}

// ReadIfPresentMax is ReadIfPresent with a tighter bound for sources that
// are small by nature and that a parser would otherwise amplify in
// memory (a 16 MiB go.mod lexed into tokens took ~1.9 GB). max must not
// exceed MaxBytes.
func ReadIfPresentMax(projectRoot, name string, max int64) ([]byte, bool, error) {
	if max > MaxBytes {
		max = MaxBytes
	}
	root, err := os.OpenRoot(projectRoot)
	if err != nil {
		return nil, false, fmt.Errorf("cannot resolve project root: %w", err)
	}
	defer root.Close()

	full := filepath.Join(projectRoot, name)
	const flags = os.O_RDONLY | syscall.O_NONBLOCK | syscall.O_CLOEXEC
	f, err := root.OpenFile(name, flags, 0)
	if err != nil && escapesRoot(err) {
		// os.Root refuses every absolute symlink target. A link whose
		// absolute target lies inside the project is an ordinary
		// project file: translate it to a root-relative path and open
		// that through the root again, so the final open is still
		// contained. Anything genuinely outside stays refused.
		if rel, ok := internalAbsoluteTarget(root, projectRoot, name); ok {
			f, err = root.OpenFile(rel, flags, 0)
		}
	}
	if err != nil {
		switch {
		case errors.Is(err, os.ErrNotExist):
			// Either nothing is there, or a symlink whose target is
			// missing. The entry itself decides which.
			if fi, lerr := root.Lstat(name); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
				return nil, false, fmt.Errorf("%s is a broken symlink; the declared source exists but cannot be read", full)
			}
			return nil, false, nil
		case escapesRoot(err):
			return nil, false, fmt.Errorf("%s escapes the project root; refusing to read outside the project (L0 contract)", full)
		default:
			return nil, false, fmt.Errorf("cannot read %s: %w", full, err)
		}
	}
	defer f.Close()

	// Only regular files are sources; decided on the opened inode.
	info, err := f.Stat()
	if err != nil {
		return nil, false, fmt.Errorf("cannot read %s: %w", full, err)
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("%s is not a regular file (%s); refusing to read it", full, info.Mode().Type())
	}
	if info.Size() > max {
		return nil, false, fmt.Errorf("cannot read %s: %w (%d bytes)", full, ErrTooLarge, max)
	}

	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, false, fmt.Errorf("cannot read %s: %w", full, err)
	}
	if int64(len(data)) > max {
		return nil, false, fmt.Errorf("cannot read %s: %w (%d bytes)", full, ErrTooLarge, max)
	}
	return data, true, nil
}

// internalAbsoluteTarget handles a symlink at name whose target is an
// absolute path: when that path lies inside the (resolved) project
// root, the root-relative form is returned so the caller can open it
// through os.Root. Only the entry's own link text is translated; any
// further symlink in the target chain is resolved by os.Root itself.
func internalAbsoluteTarget(root *os.Root, projectRoot, name string) (string, bool) {
	fi, err := root.Lstat(name)
	if err != nil || fi.Mode()&os.ModeSymlink != 0 == false {
		return "", false
	}
	dest, err := os.Readlink(filepath.Join(projectRoot, name))
	if err != nil || !filepath.IsAbs(dest) {
		return "", false
	}
	rootAbs, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", false
	}
	if resolved, err := filepath.EvalSymlinks(rootAbs); err == nil {
		rootAbs = resolved
	}
	rel, err := filepath.Rel(rootAbs, filepath.Clean(dest))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return rel, true
}

// escapesRoot reports whether err is os.Root's refusal to leave the
// root (a symlink pointing outside, or a path with ".." components).
func escapesRoot(err error) bool {
	var pe *os.PathError
	if errors.As(err, &pe) {
		msg := pe.Err.Error()
		return strings.Contains(msg, "escapes from parent") || strings.Contains(msg, "path escapes")
	}
	return false
}

// MaxBytes bounds every declared-source read. package.json and
// .nvmrc are small by nature; a project cannot make WOMM allocate
// arbitrary memory by growing them.
const MaxBytes = 16 << 20

// ErrTooLarge marks a declared source above its limit.
var ErrTooLarge = errors.New("declared source exceeds the size limit")
