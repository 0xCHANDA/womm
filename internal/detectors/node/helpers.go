package node

import (
	"fmt"
	"os"
)

// readFileIfPresent returns (data, true, nil) when path exists, or
// (nil, false, nil) when it does not. Other I/O failures propagate.
func readFileIfPresent(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("cannot read %s: %w", path, err)
	}
	return data, true, nil
}
