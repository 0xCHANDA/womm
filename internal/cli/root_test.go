package cli

import (
	"fmt"
	"testing"
)

func TestExitCodeContract(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"unknown command", fmt.Errorf(`unknown command "jaia" for "womm"`), 2},
		{"unknown flag", fmt.Errorf("unknown flag: --jaja"), 2},
		{"shorthand", fmt.Errorf("unknown shorthand flag: 'x' in -x"), 2},
		{"flag needs argument", fmt.Errorf("flag needs an argument: --force"), 2},
		{"incomplete flag", fmt.Errorf("flag provided but not defined: -jaja"), 2},
		{"too many args", fmt.Errorf("accepts at most 1 arg(s), received 2"), 2},
		{"too few args", fmt.Errorf("requires at least 1 arg(s), only received 0"), 2},
		{"execution error", fmt.Errorf("cannot read womm.yaml"), 3},
		{"output exists", fmt.Errorf("output file already exists: womm.yaml"), 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exitCodeFor(tc.err); got != tc.want {
				t.Errorf("exit = %d, want %d", got, tc.want)
			}
		})
	}
}
