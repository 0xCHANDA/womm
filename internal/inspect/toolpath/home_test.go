package toolpath

import "testing"

// TestAccountHomeForNeverFallsBackToTheEnvironment pins the contract at
// unit level, where it runs everywhere (the node package's subprocess
// test needs root and a cgo-less build): a failed lookup is "", whatever
// HOME and USER say.
func TestAccountHomeForNeverFallsBackToTheEnvironment(t *testing.T) {
	t.Setenv("HOME", "/evil/project")
	t.Setenv("USER", "ci")
	cases := []struct {
		name   string
		lookup func(int) (string, bool)
		want   string
	}{
		{"unknown uid", func(int) (string, bool) { return "", false }, ""},
		{"relative passwd home", func(int) (string, bool) { return "evil/relative", true }, ""},
		{"empty passwd home", func(int) (string, bool) { return "", true }, ""},
		{"passwd home is cleaned", func(int) (string, bool) { return "/home/u/", true }, "/home/u"},
	}
	for _, tc := range cases {
		if got := accountHomeFor(tc.lookup, 1234); got != tc.want {
			t.Errorf("%s: home = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestAccountHomeIgnoresInheritedHome pins the property the implicit
// shim directories depend on: whatever $HOME says, the home is the
// account's.
func TestAccountHomeIgnoresInheritedHome(t *testing.T) {
	hostile := tempDir(t)
	t.Setenv("HOME", hostile)
	t.Setenv("USER", "ci")
	if got := AccountHome(); got == hostile {
		t.Fatalf("AccountHome() = %q, the inherited $HOME", got)
	}
}
