package semverrange

import (
	"errors"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
)

func TestParseAcceptsNpmGrammar(t *testing.T) {
	v := semver.MustParse("24.7.0")
	cases := []struct {
		in   string
		want bool // Check(24.7.0)
	}{
		{">=22 <25", true},
		{">=22 <24", false},
		{">= 22 < 25", true},
		{">=22", true},
		{"<25", true},
		{"=24.7.0", true},
		{"24.7.0", true},
		{"v24.7.0", true},
		{"24", true},
		{"24.7", true},
		{"24.x", true},
		{"24.X", true},
		{"24.*", true},
		{"x", true},
		{"*", true},
		{"X.x.x", true},
		{"^24.0.0", true},
		{"^24", true},
		{"~24.7.0", true},
		{"~24.7", true},
		{"~24", true},
		{"^20.10.0 || >=22.0.0", true},
		{"^18.18.0 || ^20.9.0 || >=21.1.0", true},
		{"22.0.0 - 24.9.9", true},
		{"22 - 24", true},
		{"22.0.0 - 24.6.9", false},
		{">=24.0.0-0", true},
		{"<25.0.0-0", true},
		{">=24.7.0-beta.1 <25", true},
		{"24.7.0+build.1", true},
		{"  >=22   <25  ", true},
		{"\t>=22\t<25\t", true},
		{"24.7.0 24.7.0", true},
		{"9007199254740991", false},
		{">=9007199254740991.0.0", false},
		{">=20.0.0-next.1", true},
		{">=1.2.3+exp.sha.5114f85", true},
		{"1.2.3-x", false},
		{"24.7.0-x.X", false},
		{"x.x", true},
		{"x.x.x", true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			c, err := Parse(tc.in)
			if err != nil {
				t.Fatalf("Parse(%q) = %v, want ok", tc.in, err)
			}
			if got := c.Check(v); got != tc.want {
				t.Errorf("Parse(%q).Check(24.7.0) = %v, want %v", tc.in, got, tc.want)
			}
			// The accepted grammar must agree with Masterminds' own
			// parse of the same string for release versions: the
			// package restricts syntax, it never changes semantics.
			mm, err := semver.NewConstraint(tc.in)
			if err != nil {
				t.Fatalf("Masterminds rejects %q, which the grammar admits", tc.in)
			}
			if mm.Check(v) != tc.want {
				t.Errorf("Masterminds disagrees on %q", tc.in)
			}
		})
	}
}

func TestParseRejectsMastermindsExtensions(t *testing.T) {
	cases := []struct {
		in   string
		hint string
	}{
		{">=22, <25", `comparator ">=22,"`},
		{">=22,<25", `comparator ">=22,<25"`},
		{"!=24.7.0", `comparator "!=24.7.0"`},
		{"=>22", `comparator "=>22"`},
		{"=<25", `comparator "=<25"`},
		{"~>24.0", `comparator "~>24.0"`},
		{"==24.7.0", `comparator "==24.7.0"`},
		{"", "empty comparator set"},
		{"   ", "empty comparator set"},
		{">=22 ||", "empty comparator set"},
		{"|| 22", "empty comparator set"},
		{">=22 || || <20", "empty comparator set"},
		{">=", `dangling operator ">="`},
		{">= <25", `comparator ">=<25"`},
		{"<>24", `comparator "<>24"`},
		{"≥22", `comparator "≥22"`},
		{"01.2.3", `comparator "01.2.3"`},
		{"24.7.0.1", `comparator "24.7.0.1"`},
		{"24.7.0-", `comparator "24.7.0-"`},
		{">=22-0", `comparator ">=22-0"`},
		{"1.x-beta", `comparator "1.x-beta"`},
		{"1.x.x-beta", `qualifier on a wildcard or partial version "1.x.x-beta"`},
		{"1.2-beta", `comparator "1.2-beta"`},
		{"x.1.2", `numeric component after a wildcard in "x.1.2"`},
		{"1.x.3", `numeric component after a wildcard in "1.x.3"`},
		{"*.1", `numeric component after a wildcard in "*.1"`},
		{">x", `operator on a wildcard major version ">x"`},
		{"<*", `operator on a wildcard major version "<*"`},
		{">=X", `operator on a wildcard major version ">=X"`},
		{"^x", `operator on a wildcard major version "^x"`},
		{"~x.x", `operator on a wildcard major version "~x.x"`},
		{"=*", `operator on a wildcard major version "=*"`},
		{"x - 2", `operator on a wildcard major version "-x"`},
		{">=99999999999999999999", "exceeds npm's limit"},
		{"18446744073709551616", "exceeds npm's limit"},
		{"18446744073709551615", "exceeds npm's limit"},
		{"9007199254740992", "exceeds npm's limit"},
		{">=1.9007199254740992", "exceeds npm's limit"},
		{"1.2.9007199254740992", "exceeds npm's limit"},
		{"x.y.z", `comparator "x.y.z"`},
		{"lts/*", `comparator "lts/*"`},
		{"latest", `comparator "latest"`},
		{"22.0.0 - ", `comparator "-"`},
		{"22.0.0 -", `comparator "-"`},
		{"22.0.0 - 24 - 25", `comparator "-"`},
		{"22.0.0 -24", `comparator "-24"`},
		{">=22 <25 extra", `comparator "extra"`},
		{"1.2.3 || 4.5.6 ||", "empty comparator set"},
		{strings.Repeat(">=1 ", 200), "longer than 512 bytes"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			_, err := Parse(tc.in)
			if !errors.Is(err, ErrSyntax) {
				t.Fatalf("Parse(%q) = %v, want ErrSyntax", tc.in, err)
			}
			if !strings.Contains(err.Error(), tc.hint) {
				t.Errorf("error %q should mention %q", err, tc.hint)
			}
		})
	}
}

func TestParseIsDeterministicAndPure(t *testing.T) {
	in := "  ^20.10.0 ||   >=22 <25 "
	a, err := Parse(in)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Parse(in)
	if a.String() != b.String() || a.String() != "^20.10.0 || >=22 <25" {
		t.Errorf("normalized form = %q / %q", a.String(), b.String())
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{">=22 <25", "^20.10.0 || >=22", "22 - 24", "24.x", "*", ">=22, <25", "!=1", "", ">=22-0", "1.x.3-beta", "v24.7.0", "24.7.0+build"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		c, err := Parse(s)
		if err != nil {
			if !errors.Is(err, ErrSyntax) {
				t.Fatalf("non-sentinel error for %q: %v", s, err)
			}
			return
		}
		// Whatever we accept must evaluate without panicking, and
		// whenever Masterminds also accepts the raw string (it may
		// not: npm-style whitespace such as "\v" is normalized away
		// here) both must agree on the verdict.
		v := semver.MustParse("24.7.0")
		got := c.Check(v)
		if mm, merr := semver.NewConstraint(s); merr == nil && mm.Check(v) != got {
			t.Fatalf("normalization changed semantics for %q", s)
		}
		again, err := Parse(s)
		if err != nil || again.String() != c.String() {
			t.Fatalf("Parse(%q) is not deterministic: %q vs %q (%v)", s, c.String(), again.String(), err)
		}
	})
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"  >=22\t<25 \n||\n ^20.10.0 ": ">=22 <25 || ^20.10.0",
		">= 22":                        ">=22",
		"22.0.0   -   24.0.0":          "22.0.0 - 24.0.0",
		"24.x":                         "24.x",
	}
	for in, want := range cases {
		got, err := Normalize(in)
		if err != nil || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := Normalize(">=22,<25"); !errors.Is(err, ErrSyntax) {
		t.Errorf("Normalize must apply the grammar: %v", err)
	}
}
