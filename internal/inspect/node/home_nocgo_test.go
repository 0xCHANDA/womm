//go:build !cgo || osusergo

package node

import (
	"strings"
	"testing"
)

func TestHomeFromPasswd(t *testing.T) {
	const passwd = `# comment
root:x:0:0:root:/root:/bin/bash

daemon:x:1:1:daemon:/usr/sbin:/usr/sbin/nologin
alice:x:1000:1000:Alice,,,:/home/alice:/bin/bash
alias:x:1000:1000::/home/second:/bin/sh
short:x:2000
nohome:x:3000:3000:::/bin/sh
`
	cases := []struct {
		uid  int
		home string
		ok   bool
	}{
		{0, "/root", true},
		{1, "/usr/sbin", true},
		{1000, "/home/alice", true}, // first entry wins, like os/user
		{3000, "", true},            // present with an empty home: the caller refuses it
		{2000, "", false},           // malformed line is not an entry
		{54321, "", false},          // no entry: nothing is invented
	}
	for _, tc := range cases {
		home, ok := homeFromPasswd(strings.NewReader(passwd), tc.uid)
		if home != tc.home || ok != tc.ok {
			t.Errorf("uid %d: (%q, %v), want (%q, %v)", tc.uid, home, ok, tc.home, tc.ok)
		}
	}
}
