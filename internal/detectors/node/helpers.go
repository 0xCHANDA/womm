package node

import "github.com/0xCHANDA/womm/internal/detectors/source"

// readFileIfPresent is the contained, bounded read of a declared
// source; see internal/detectors/source, shared by every detector.
func readFileIfPresent(projectRoot, name string) ([]byte, bool, error) {
	return source.ReadIfPresent(projectRoot, name)
}

const maxSourceBytes = source.MaxBytes

var errSourceTooLarge = source.ErrTooLarge
