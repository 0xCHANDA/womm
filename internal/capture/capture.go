// Package capture turns a project's declared requirements into a
// womm.yaml document.
//
// It is L0 only: it runs the project detectors (filesystem reads inside
// the project root) and serializes what they found. It never inspects
// the machine, never judges satisfaction, and never executes anything
// the project controls. Detection errors (malformed sources,
// conflicting declarations, unsupported selectors, containment
// violations) are returned as-is: capture writes what is declared or
// nothing at all.
package capture

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"

	"github.com/0xCHANDA/womm/internal/core"
	"github.com/0xCHANDA/womm/internal/detectors"
	"github.com/0xCHANDA/womm/internal/detectors/node"
	"github.com/0xCHANDA/womm/internal/schema"
)

var (
	// ErrOutputExists marks an output file that already exists when
	// overwriting was not requested. Overwriting a captured file is a
	// decision the user must make explicitly (--force).
	ErrOutputExists = errors.New("output file already exists")
	// ErrOutputSymlink marks an output path that is a symbolic link.
	// A project can plant `womm.yaml -> <anything>`; writing through
	// it would clobber a file the user never chose. Refused, always.
	ErrOutputSymlink = errors.New("output path is a symbolic link")
	// ErrDuplicateRequirement marks two detectors declaring the same
	// requirement name. No merge policy exists for that; it is a bug
	// in the detector set, surfaced rather than resolved.
	ErrDuplicateRequirement = errors.New("duplicate requirement")
)

// FileName is the default output file name inside a project.
const FileName = "womm.yaml"

// defaultDetectors is the fixed detector set of the v0.1 vertical
// slice (Node.js ecosystem). Order does not affect the output:
// requirements are sorted by name.
func defaultDetectors() []detectors.Detector {
	return []detectors.Detector{
		node.NewNodeDetector(),
		node.NewPackageManagerDetector(),
	}
}

// Capture runs the detectors on projectRoot and returns the resulting
// schema file, requirements sorted by name. It does not touch the
// filesystem beyond the detectors' contained reads.
func Capture(ctx context.Context, projectRoot string) (*schema.File, error) {
	return captureWith(ctx, projectRoot, defaultDetectors())
}

func captureWith(ctx context.Context, projectRoot string, dets []detectors.Detector) (*schema.File, error) {
	info, err := os.Stat(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("cannot capture %s: %w", projectRoot, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("cannot capture %s: not a directory", projectRoot)
	}

	reqs := []core.Requirement{}
	seen := map[string]string{}
	for _, d := range dets {
		found, err := d.Detect(ctx, projectRoot)
		if err != nil {
			return nil, fmt.Errorf("%s detection: %w", d.Name(), err)
		}
		for _, r := range found {
			if prev, dup := seen[r.Name]; dup {
				return nil, fmt.Errorf("%w: %q declared by both %s and %s detectors", ErrDuplicateRequirement, r.Name, prev, d.Name())
			}
			seen[r.Name] = d.Name()
			reqs = append(reqs, r)
		}
	}
	sort.SliceStable(reqs, func(i, j int) bool { return reqs[i].Name < reqs[j].Name })

	f := &schema.File{Version: schema.Version, Requirements: reqs}
	if err := schema.Validate(f); err != nil {
		return nil, fmt.Errorf("captured requirements are not a valid womm.yaml: %w", err)
	}
	return f, nil
}

// DefaultOutput is the output path for a project: <projectRoot>/womm.yaml.
func DefaultOutput(projectRoot string) string {
	return filepath.Join(projectRoot, FileName)
}

// Write serializes f and stores it at path.
//
// Without force the file must not exist yet (ErrOutputExists). With
// force an existing regular file is replaced. A symbolic link at path
// is refused in both modes (ErrOutputSymlink): the write never follows
// a link, so a hostile project cannot redirect it. The document is
// parsed back before being written, so a file on disk is always one
// schema.Parse accepts.
func Write(path string, f *schema.File, force bool) ([]byte, error) {
	data, err := schema.Marshal(f)
	if err != nil {
		return nil, err
	}
	if _, _, err := schema.Parse(data); err != nil {
		return nil, fmt.Errorf("serialized womm.yaml does not parse back (internal error): %w", err)
	}

	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: %s", ErrOutputSymlink, path)
		}
		if !force {
			return nil, fmt.Errorf("%w: %s (use --force to overwrite)", ErrOutputExists, path)
		}
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("cannot write %s: not a regular file", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("cannot write %s: %w", path, err)
	}

	flags := os.O_WRONLY | os.O_CREATE | syscall.O_NOFOLLOW
	if force {
		flags |= os.O_TRUNC
	} else {
		// O_EXCL closes the window between Lstat and open: if
		// anything appears at path meanwhile, the open fails instead
		// of overwriting or following it.
		flags |= os.O_EXCL
	}
	fh, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("%w: %s (use --force to overwrite)", ErrOutputExists, path)
		}
		if errors.Is(err, syscall.ELOOP) {
			return nil, fmt.Errorf("%w: %s", ErrOutputSymlink, path)
		}
		return nil, fmt.Errorf("cannot write %s: %w", path, err)
	}
	if _, err := fh.Write(data); err != nil {
		fh.Close()
		return nil, fmt.Errorf("cannot write %s: %w", path, err)
	}
	if err := fh.Close(); err != nil {
		return nil, fmt.Errorf("cannot write %s: %w", path, err)
	}
	return data, nil
}
