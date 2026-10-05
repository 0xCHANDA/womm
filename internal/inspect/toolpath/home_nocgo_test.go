//go:build !cgo || osusergo

package toolpath

import (
	"os"
	"path/filepath"
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

// Without cgo the lookup must read the passwd file itself: os/user would
// answer from the inherited $HOME for a uid with no entry. A uid that
// exists only in a fixture proves where the answer comes from, with or
// without root.
func TestLookupHomeReadsThePasswdFileNotOsUser(t *testing.T) {
	f := filepath.Join(t.TempDir(), "passwd")
	if err := os.WriteFile(f, []byte("fixture:x:54321:54321::/fixture/home:/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := passwdPath
	passwdPath = f
	t.Cleanup(func() { passwdPath = prev })
	t.Setenv("HOME", "/evil/project")
	t.Setenv("USER", "ci")
	if home, ok := lookupHome(54321); !ok || home != "/fixture/home" {
		t.Errorf("lookupHome(54321) = %q, %v; want the fixture's /fixture/home", home, ok)
	}
	if home, ok := lookupHome(os.Getuid()); ok || home == "/evil/project" {
		t.Errorf("lookupHome(own uid) = %q, %v; a uid absent from the file has no home", home, ok)
	}
	passwdPath = filepath.Join(t.TempDir(), "missing")
	if _, ok := lookupHome(0); ok {
		t.Error("an unreadable passwd file produced a home")
	}
}
