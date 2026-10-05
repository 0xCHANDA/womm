//go:build !cgo || osusergo

package node

import (
	"bufio"
	"io"
	"os"
	"strconv"
	"strings"
)

// maxPasswdBytes bounds the read of /etc/passwd.
const maxPasswdBytes = 4 << 20

// lookupHome reads the home directory of uid from /etc/passwd and
// nowhere else. It must not use os/user here: without cgo (the release
// binaries) both user.Current() and user.LookupId (which calls Current
// first) fall back to the inherited $HOME — when $USER is set too — for
// a uid with no passwd entry (Docker `--user`, OpenShift, many CI
// images), handing the probe exactly the project-reachable value the
// forced HOME exists to replace.
func lookupHome(uid int) (string, bool) {
	f, err := os.Open("/etc/passwd")
	if err != nil {
		return "", false
	}
	defer f.Close()
	return homeFromPasswd(f, uid)
}

// homeFromPasswd returns the home field of the first entry for uid in
// passwd-format input (name:pw:uid:gid:gecos:home:shell), the same entry
// os/user's pure-Go lookup picks.
func homeFromPasswd(r io.Reader, uid int) (string, bool) {
	want := strconv.Itoa(uid)
	sc := bufio.NewScanner(io.LimitReader(r, maxPasswdBytes))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		f := strings.Split(line, ":")
		if len(f) < 6 || f[2] != want {
			continue
		}
		return f[5], true
	}
	return "", false
}
