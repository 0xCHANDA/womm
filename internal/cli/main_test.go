package cli

import (
	"os"
	"testing"
)

// TestMain keeps every test independent of the machine it runs on: no
// test may see the host's real home directory, and with it the host's
// ~/.volta, ~/.asdf, ~/.local/bin... Tests that exercise the shim
// directories set accountHome themselves.
func TestMain(m *testing.M) {
	accountHome = func() string { return "" }
	os.Exit(m.Run())
}
