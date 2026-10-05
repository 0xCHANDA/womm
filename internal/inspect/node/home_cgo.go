//go:build cgo && !osusergo

package node

import (
	"os/user"
	"strconv"
)

// lookupHome resolves the home directory of uid through the C library
// (getpwuid_r, so NSS accounts work). With cgo, os/user's Current() has
// no fallback to the environment.
func lookupHome(uid int) (string, bool) {
	u, err := user.LookupId(strconv.Itoa(uid))
	if err != nil {
		return "", false
	}
	return u.HomeDir, true
}
