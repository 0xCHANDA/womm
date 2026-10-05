package toolpath

import (
	"os"
	"path/filepath"
)

// AccountHome returns the absolute, cleaned home directory of the
// account WOMM runs as, or "" when the account has none. It comes from
// the user database (see lookupHome) and never from the inherited
// $HOME, which a launcher can point into a project.
func AccountHome() string { return accountHomeFor(lookupHome, os.Getuid()) }

// accountHomeFor returns the absolute home directory lookup reports for
// uid, or "". It must never consult the environment.
func accountHomeFor(lookup func(uid int) (string, bool), uid int) string {
	if home, ok := lookup(uid); ok && filepath.IsAbs(home) {
		return filepath.Clean(home)
	}
	return ""
}
