package goversion

import (
	"strings"
	"testing"
)

func TestValid(t *testing.T) {
	for _, v := range []string{"1.0", "1.20", "1.21", "1.21.0", "1.21.3", "1.21rc1", "1.22rc2", "1.23beta1", "1.9alpha1", "1.100.7", "2.0"} {
		if !Valid(v) {
			t.Errorf("Valid(%q) = false", v)
		}
	}
	for _, v := range []string{"", "1", "01.21", "1.021", "1.21.", ".1.21", "go1.21", "v1.21", "1.21.0-rc1", "1.21.0rc1", "1.21-bigcorp", "1.21.0-bigcorp",
		"1.21 ", " 1.21", "1.21\n", "1.2.3.4", "1.x", "1.21RC1", "1.21rc", "1.21.01", "1.21foo1", "1.21rc01", "1.21gamma2", "0.1", "1.21+meta", "١.21"} {
		if Valid(v) {
			t.Errorf("Valid(%q) = true", v)
		}
	}
	long := "1." + string(make([]byte, 0))
	for i := 0; i < 70; i++ {
		long += "9"
	}
	if Valid(long) {
		t.Error("an over-long version was accepted")
	}
	at64 := "1." + strings.Repeat("9", 62) // 64 bytes: the limit itself is allowed
	if !Valid(at64) || Valid(at64+"9") {
		t.Errorf("64-byte boundary: Valid(64)=%v Valid(65)=%v", Valid(at64), Valid(at64+"9"))
	}
}

// The ordering that makes Go versions not semver, pinned against the
// standard library's authoritative implementation.
func TestCompareIsGoOrderNotSemver(t *testing.T) {
	ordered := []string{"1.20", "1.20.1", "1.21", "1.21alpha1", "1.21beta2", "1.21rc1", "1.21rc2", "1.21.0", "1.21.1", "1.22", "1.22.0", "1.100.0"}
	// 1.21 < 1.21rc1 < 1.21.0 (the language version sorts first) — but
	// the list above is only non-decreasing where equal is allowed.
	for i := 1; i < len(ordered); i++ {
		a, b := ordered[i-1], ordered[i]
		if Compare(a, b) > 0 {
			t.Errorf("Compare(%q, %q) = %d, want <= 0", a, b, Compare(a, b))
		}
	}
	cases := []struct {
		a, b string
		want int
	}{
		{"1.21", "1.21rc1", -1},
		{"1.21rc1", "1.21.0", -1},
		{"1.21", "1.21.0", -1}, // language version before the first release
		{"1.20", "1.20.0", 0},  // before 1.21 a missing patch is .0
		{"1.21rc2", "1.21rc10", -1},
		{"1.21alpha1", "1.21beta1", -1},
		{"1.21beta1", "1.21rc1", -1},
		{"1.9", "1.10", -1}, // numeric, not lexical
		{"1.21.9", "1.21.10", -1},
		{"1.24.7", "1.24.7", 0},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
		if got := Compare(c.b, c.a); got != -c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d (antisymmetry)", c.b, c.a, got, -c.want)
		}
	}
}

func TestParseMin(t *testing.T) {
	for _, ok := range []string{">=1.21", ">=1.21.3", ">=1.22rc1"} {
		if _, err := ParseMin(ok); err != nil {
			t.Errorf("ParseMin(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"", "1.21", ">= 1.21", ">1.21", "<=1.21", "^1.21", ">=go1.21", ">=1", ">=1.21 <1.23", "present", "=1.21", ">=1.21-bigcorp"} {
		if _, err := ParseMin(bad); err == nil {
			t.Errorf("ParseMin(%q) accepted", bad)
		}
	}
	if v, _ := ParseMin(FormatMin("1.24.1")); v != "1.24.1" {
		t.Errorf("round trip = %q", v)
	}
}

func TestParseVersionOutput(t *testing.T) {
	ok := map[string]string{
		"go version go1.24.7 linux/amd64\n":                  "1.24.7",
		"go version go1.20 darwin/arm64":                     "1.20",
		"go version go1.25rc1 linux/amd64\n":                 "1.25rc1",
		"\ngo version go1.22.0 X:boringcrypto linux/amd64\n": "1.22.0",
		"  go version go1.21.0 windows/amd64  \n":            "1.21.0",
		"go version go1.18.10 linux/386":                     "1.18.10",
	}
	for in, want := range ok {
		if got, o := ParseVersionOutput(in); !o || got != want {
			t.Errorf("%q = %q, %v; want %q", in, got, o, want)
		}
	}
	for _, in := range []string{
		"", "\n", "go version devel go1.25-abc123 Mon Jan 1 linux/amd64", "go version go1.21.0-bigcorp linux/amd64",
		"go version go1 linux/amd64", "go version 1.21.0 linux/amd64", "go version go1.21.0", "go version go1.21.0 linux",
		"go version go1.21.0 linux/amd64\nextra", "go foo go1.21.0 linux/amd64", "   \n  \n", "go version go1.21.0 linux/amd64 extra", "go version go1.21.0 X:a X:b linux/amd64",
		"version go1.21.0 linux/amd64", "go: command not found", "go version go1.21.0rc1 linux/amd64", "go version go1.21.0 Y:boringcrypto linux/amd64",
	} {
		if got, o := ParseVersionOutput(in); o {
			t.Errorf("%q accepted as %q", in, got)
		}
	}
}
