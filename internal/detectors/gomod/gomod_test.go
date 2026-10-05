package gomod

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/0xCHANDA/womm/internal/core"
	"github.com/0xCHANDA/womm/internal/goversion"
)

func detect(t *testing.T, gomod string) ([]core.Requirement, error) {
	t.Helper()
	dir := t.TempDir()
	if gomod != "\x00absent" {
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return New().Detect(context.Background(), dir)
}

func TestDetectEmitsAMinimumWithEvidence(t *testing.T) {
	reqs, err := detect(t, "module example.com/m\n\ngo 1.24.1\n\nrequire golang.org/x/mod v0.20.0\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []core.Requirement{{Name: "go", Constraint: ">=1.24.1", Evidence: []core.Evidence{{Source: "go.mod", Field: "go", Value: "1.24.1"}}}}
	if !reflect.DeepEqual(reqs, want) {
		t.Errorf("got %+v, want %+v", reqs, want)
	}
}

func TestDetectLanguageReleaseAndPrereleaseSpellingsAreVerbatim(t *testing.T) {
	for _, v := range []string{"1.21", "1.21.0", "1.22rc1", "1.9", "1.20"} {
		reqs, err := detect(t, "module m\ngo "+v+"\n")
		if err != nil || len(reqs) != 1 || reqs[0].Constraint != ">="+v || reqs[0].Evidence[0].Value != v {
			t.Errorf("go %s: %+v, %v", v, reqs, err)
		}
	}
}

func TestDetectNoDeclarationIsNotAnError(t *testing.T) {
	for _, tc := range []string{"\x00absent", "", "module m\n", "module m\nrequire a v1.0.0\n", "// only a comment\n", "module m\ntoolchain go1.99.0\n"} {
		reqs, err := detect(t, tc)
		if err != nil || len(reqs) != 0 {
			t.Errorf("%q: %+v, %v; want no requirement and no error", tc, reqs, err)
		}
	}
}

// The toolchain directive is a suggestion for GOTOOLCHAIN=auto, not a
// requirement; it must not produce one, conflict with, or raise the go line.
func TestDetectIgnoresTheToolchainDirective(t *testing.T) {
	for gm, want := range map[string]string{
		"module m\ngo 1.21\ntoolchain go1.99.0\n":   ">=1.21",
		"module m\ntoolchain go1.99.0\ngo 1.21\n":   ">=1.21",
		"module m\ngo 1.24.0\ntoolchain go1.20.0\n": ">=1.24.0",
		"module m\ngo 1.21\ntoolchain default\n":    ">=1.21",
		"module m\ngo 1.21\ntoolchain bogus\n":      ">=1.21",
	} {
		reqs, err := detect(t, gm)
		if err != nil || len(reqs) != 1 || reqs[0].Constraint != want || len(reqs[0].Evidence) != 1 || reqs[0].Evidence[0].Field != "go" {
			t.Errorf("%q: %+v, %v; want only the go line as %s", gm, reqs, err, want)
		}
	}
}

func TestDetectorName(t *testing.T) {
	if New().Name() != "go" {
		t.Errorf("Name = %q", New().Name())
	}
}

func TestDetectRefusesAmbiguousOrMalformedGoDirectives(t *testing.T) {
	cases := map[string]string{
		"repeated":            "module m\ngo 1.21\ngo 1.22\n",
		"repeated same":       "module m\ngo 1.21\ngo 1.21\n",
		"block":               "module m\ngo (\n1.21\n)\n",
		"no argument":         "module m\ngo\n",
		"two arguments":       "module m\ngo 1.21 1.22\n",
		"quoted":              "module m\ngo \"1.21\"\n",
		"raw quoted":          "module m\ngo `1.21`\n",
		"major only":          "module m\ngo 1\n",
		"leading zero":        "module m\ngo 01.21\n",
		"v prefix":            "module m\ngo v1.21\n",
		"go prefix":           "module m\ngo go1.21\n",
		"semver prerelease":   "module m\ngo 1.21.0-rc1\n",
		"vendor suffix":       "module m\ngo 1.21.0-bigcorp\n",
		"prerelease of patch": "module m\ngo 1.21.0rc1\n",
		"unknown kind":        "module m\ngo 1.21foo1\n",
		"four parts":          "module m\ngo 1.21.0.1\n",
		"too long":            "module m\ngo 1." + strings.Repeat("9", 80) + "\n",
		"block comment":       "module m\n/* c */\ngo 1.21\n",
		"unterminated string": "module m\nrequire \"a\ngo 1.21\n",
		"unterminated raw":    "module m\nrequire `a\ngo 1.21\n",
		"unterminated block":  "module m\nrequire (\na v1.0.0\ngo 1.21\n",
		"newline in string":   "module m\nrequire \"a\n\"\ngo 1.21\n",
		"newline in raw":      "module m\nrequire `a\n`\ngo 1.21\n",
		"quote glued to word": "module m\ngo\"1.21\"\ngo 1.22\n",
		"comma after verb":    "module m\ngo 1.21\ngo,\n",
		"bracket after verb":  "module m\ngo 1.21\ngo]\n",
		"brace splits args":   "module m\ngo{ 1.21\ngo 1.22\n",
	}
	for name, gm := range cases {
		t.Run(name, func(t *testing.T) {
			reqs, err := detect(t, gm)
			if !errors.Is(err, ErrMalformed) || len(reqs) != 0 {
				t.Errorf("got %+v, %v; want ErrMalformed and no requirement", reqs, err)
			}
		})
	}
}

// A `go` that is not the directive must not be read as one.
func TestDetectOnlyReadsDepthZeroStatements(t *testing.T) {
	cases := []struct{ gm, want string }{
		// `(` opens a block only at the end of a line; mid-line it is an argument.
		{"module m\nexclude ( v1.0.0\ngo 1.99\nexclude ) v1.0.1\n", "1.99"},
		{"module m\nexclude (\n\tgo 1.99\n)\ngo 1.21\n", "1.21"},
		{"module m\nretract [v1.0.0, v1.0.1]\ngo 1.22\n", "1.22"},
		{"module m\nexclude (\n\ta v1.0.0 )\n)\ngo 1.23\n", "1.23"}, // a mid-line ) does not end the block
		{"module m\nrequire (\n\tgo v1.0.0\n)\ngo 1.21\n", "1.21"},
		{"module m\nrequire (\n\tgo v1.0.0\n)\n", ""},
		{"module m\nrequire a v1.0.0 // go 1.99\ngo 1.22\n", "1.22"},
		{"module m\nretract v1.0.0 // go 1.99\n", ""},
		{"module m\nretract \"v1.0.0 // go 1.99\"\ngo 1.23\n", "1.23"},
		{"module \"go 1.99\"\n", ""},
		{"module m\nreplace a => ./go\ngo 1.24\n", "1.24"},
		{"module m\r\ngo 1.21\r\n", "1.21"},
		{"module \"a\\\"b\"\ngo 1.21\n", "1.21"},                  // an escaped quote inside a string
		{"module m\ngo 1.21// glued comment\n", "1.21"},           // a comment may touch the word
		{"module m\nrequire(\n\tgo v1.0.0\n)\ngo 1.22\n", "1.22"}, // `require(` is a block opener
		{"module m\nGO 1.21\n", ""},                               // verbs are case-sensitive
		{"module m\nGo 1.99\ngo 1.21\n", "1.21"},
		{"module m\n\tgo\t1.21\t// c\n", "1.21"},
		{"module m\n// go 1.99\ngo 1.21\n", "1.21"},
	}
	for _, tc := range cases {
		reqs, err := detect(t, tc.gm)
		if err != nil {
			t.Errorf("%q: %v", tc.gm, err)
			continue
		}
		got := ""
		if len(reqs) == 1 {
			got = strings.TrimPrefix(reqs[0].Constraint, ">=")
		}
		if got != tc.want {
			t.Errorf("%q: version %q, want %q", tc.gm, got, tc.want)
		}
	}
}

func TestDetectRefusesNonRegularAndEscapingSources(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "go.mod")
	if err := os.WriteFile(outside, []byte("module m\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "go.mod")); err != nil {
		t.Fatal(err)
	}
	if reqs, err := New().Detect(context.Background(), dir); err == nil || len(reqs) != 0 {
		t.Errorf("a go.mod symlink escaping the project was read: %+v, %v", reqs, err)
	}
}

// --- differential test against the real go command -----------------------

// goOracle asks the installed go command how it reads a go.mod:
// ok is whether it parsed, version the go directive it found, why the
// error text when it did not.
func goOracle(t *testing.T, gomod string) (version string, ok bool, why string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "mod", "edit", "-json")
	cmd.Dir = dir
	// Test-only: the file is one this test just wrote, and `go mod edit`
	// executes nothing from it.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "GOTOOLCHAIN=local", "GOFLAGS=", "GOPROXY=off", "GOENV=off", "GOCACHE=" + filepath.Join(dir, ".cache")}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if strings.Contains(errb.String(), "errors parsing go.mod") {
			return "", false, errb.String()
		}
		t.Fatalf("go mod edit failed for another reason: %v\n%s", err, errb.String())
	}
	var j struct{ Go string }
	if err := json.Unmarshal(out.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	return j.Go, true, ""
}

func TestDifferentialAgainstTheGoCommand(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("no go command (CI must have it: it is the oracle)")
		}
		t.Skip("no go command to compare against")
	}
	n, seed := 400, int64(1)
	if v, err := strconv.Atoi(os.Getenv("WOMM_GO_DIFF_N")); err == nil {
		n = v
	}
	if v, err := strconv.ParseInt(os.Getenv("WOMM_GO_DIFF_SEED"), 10, 64); err == nil {
		seed = v
	}
	rng := rand.New(rand.NewSource(seed))
	pick := func(a []string) string { return a[rng.Intn(len(a))] }
	modules := []string{"module m", "module example.com/a/b", "module \"m\"", ""}
	goLines := []string{"go 1.21", "go 1.21.0", "go 1.24.7", "go 1.99", "go\t1.21", "go  1.21", "go 1.21 // c", "go 1.22rc1", "go 1.23beta2", "go 1.21alpha1",
		"go 1.21.0rc1", "go 1.21foo1", "go 1", "go 1.0", "go 01.21", "go 1.021", "go v1.21", "go \"1.21\"", "go `1.21`", "go 1.21 1.22", "go", "go (", "go 1.21.0-rc1",
		"go 1.21.0-bigcorp", "GO 1.21", "go 1.21,", "go 1.2.3.4", "go 99999999999999999999.1", "go 1.20", "go 1.9", "go 1.100.0", "go 1.21.", "go .21", "go 1.x", "go ( 1.21", "go )", "go 1.21 )", ""}
	others := []string{"", "// comment", "toolchain go1.99.0", "toolchain go1.20.0", "toolchain default", "toolchain bogus",
		"require a/b v1.0.0", "require a/b v1.0.0 // go 1.99", "require (\n\ta/b v1.0.0\n\tc/d v1.2.3 // go 1.99\n)",
		"require (\n\tgo v1.0.0\n)", "replace a/b => ./b", "exclude a/b v1.0.0", "retract v1.0.0 // go 1.99", "retract \"v1.0.0\"",
		"godebug default=go1.21", "godebug (\n\tpanicnil=1\n)", "/* c */", "require `a/b\nc` v1.0.0", "require \"a/b v1.0.0", "go 1.50", "toolchain",
		"exclude ( v1.0.0", "exclude ) v1.0.1", "exclude (\n\tgo 1.99\n)", "exclude (\n\ta/b v1.0.0\n\tgo 1.98 )\ngo 1.97", "retract [v1.0.0, v1.0.1]", "retract (\n\t[v1.0.0, v1.0.1]\n\tv1.2.0 // why\n)",
		"go,", "go]", "go[", "go{ 1.21", "go} 1.22", "go, 1.21", "go ,1.21", "go 1.21,", "go (", "go (\n1.21\n)", "go ( 1.21 )", "go(1.21)", "replace x => y // go 1.5", "require (\n\ta v1.0.0 )\n)", "( go 1.99", ") go 1.99", "exclude {\n\tgo 1.99\n}", "exclude [ ( v1", "exclude ] ) v2",
		"module \"a\\\"b\"", "go 1.21// c", "go 1.21//c", "require(\n\tgo v1.0.0\n)", "require \"a\n\"", "GO 1.21", "Go 1.21", "go\"1.21\"", "require `a\n`"}
	var tried, bothOK int
	for i := 0; i < n; i++ {
		var lines []string
		lines = append(lines, pick(modules))
		n := 1 + rng.Intn(4)
		for j := 0; j < n; j++ {
			if rng.Intn(2) == 0 {
				lines = append(lines, pick(goLines))
			} else {
				lines = append(lines, pick(others))
			}
		}
		sep := "\n"
		if rng.Intn(5) == 0 {
			sep = "\r\n"
		}
		gm := strings.Join(lines, sep) + sep
		tried++

		if checkAgainstGo(t, gm) {
			bothOK++
		}
	}
	if bothOK < 40 {
		t.Fatalf("only %d/%d files were accepted by both; the generator is not exercising the happy path", bothOK, tried)
	}
	t.Logf("%d files compared, %d accepted by both", tried, bothOK)
}

// aboutGoDirective reports whether the go command's parse error concerns
// the go directive (as opposed to any other line of the file).
func aboutGoDirective(msg string) bool {
	for _, line := range strings.Split(msg, "\n") {
		for _, k := range []string{"invalid go version", "go directive", "repeated go statement", "unexpected newline in string", "unterminated"} {
			if strings.Contains(line, k) {
				return true
			}
		}
		line = strings.TrimSpace(line)
		// "unknown block type: go" exactly (not "...: gorequire"). An
		// unknown directive such as `GO` is not a disagreement about how
		// the go directive reads: WOMM ignores it (pinned by a unit test).
		if strings.HasSuffix(line, "unknown block type: go") {
			return true
		}
	}
	return false
}

func writeDir(t *testing.T, gomod string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func FuzzParseGoDirective(f *testing.F) {
	for _, s := range []string{"module m\ngo 1.21\n", "go (\n1.21\n)", "require (\n go 1.2\n)\ngo 1.21", "go \"1.21\"", "`a\nb`", "/* x */", "go 1.21\r\ngo 1.22", "\x00", "go 1.21 // c"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		v, found, err := parseGoDirective(data)
		if err != nil && (found || v != "") {
			t.Fatalf("error with a result: %q %v %v", v, found, err)
		}
		if found && !goversion.Valid(v) {
			t.Fatalf("accepted an invalid version %q from %q", v, data)
		}
		if found && !bytes.Contains(data, []byte(v)) {
			t.Fatalf("version %q is not in the input %q", v, data)
		}
		_ = fmt.Sprint()
	})
}

// checkAgainstGo compares WOMM's reading of one go.mod with the go
// command's and reports whether both accepted it.
func checkAgainstGo(t *testing.T, gm string) (ok bool) {
	t.Helper()
	wantV, goOK, why := goOracle(t, gm)
	reqs, err := New().Detect(context.Background(), writeDir(t, gm))
	mineOK := err == nil
	gotV := ""
	if mineOK && len(reqs) == 1 {
		gotV = strings.TrimPrefix(reqs[0].Constraint, goversion.MinPrefix)
	}
	switch {
	case mineOK && !goOK:
		// WOMM reads only the go directive and validates nothing else
		// (a bad `toolchain` line, an unknown directive, ...): the go
		// command may reject the file for those. It must never accept
		// a file whose rejection is ABOUT the go directive.
		if aboutGoDirective(why) {
			t.Errorf("FALSE REQUIREMENT: WOMM read %q from a go.mod the go command rejects about its go directive (%s):\n%s", gotV, strings.TrimSpace(why), gm)
		}
	case mineOK && goOK:
		ok = true
		if gotV != wantV {
			t.Errorf("WOMM read %q, the go command reads %q:\n%s", gotV, wantV, gm)
		}
	case !mineOK && goOK:
		// WOMM may be STRICTER than the go command, but only about
		// the version itself (syntax go accepts and go/version or
		// WOMM's gate refuses) — never about anything else.
		if wantV == "" || goversion.Valid(wantV) {
			t.Errorf("WOMM refused (%v) a go.mod the go command reads as go %q:\n%s", err, wantV, gm)
		}
	}
	return ok
}

// A second, independent generator: take real go.mod files (the Go
// distribution's own) and mutate them at the byte level with the
// characters the go.mod lexer treats specially. Every file is compared
// with the go command's reading under the same invariants. This is what
// finds tokenizer disagreements a template generator cannot imagine.
func TestDifferentialMutatedRealGoMod(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("no go command (CI must have it: it is the oracle)")
		}
		t.Skip("no go command to compare against")
	}
	out, err := exec.Command(goBin, "env", "GOROOT").Output()
	if err != nil {
		t.Fatal(err)
	}
	root := strings.TrimSpace(string(out))
	var seeds []string
	for _, rel := range []string{"src/go.mod", "src/cmd/go.mod"} {
		if b, err := os.ReadFile(filepath.Join(root, rel)); err == nil {
			seeds = append(seeds, string(b))
		}
	}
	if len(seeds) == 0 {
		t.Skip("no go.mod in GOROOT")
	}
	n, seed := 300, int64(1)
	if v, err := strconv.Atoi(os.Getenv("WOMM_GO_DIFF_N")); err == nil {
		n = v
	}
	if v, err := strconv.ParseInt(os.Getenv("WOMM_GO_DIFF_SEED"), 10, 64); err == nil {
		seed = v
	}
	rng := rand.New(rand.NewSource(seed))
	inserts := []string{",", "[", "]", "{", "}", "(", ")", " (", ") ", "\n", " ", "\t", "\"", "`", "//", " // c\n", "go 1.99\n", "go 1.21\n", "\ngo 1.5", "=> ", "go", "( ", "\r"}
	mutated := 0
	for i := 0; i < n; i++ {
		b := []byte(seeds[rng.Intn(len(seeds))])
		for k := 1 + rng.Intn(3); k > 0; k-- {
			pos := rng.Intn(len(b) + 1)
			switch rng.Intn(3) {
			case 0: // insert
				ins := inserts[rng.Intn(len(inserts))]
				b = append(b[:pos:pos], append([]byte(ins), b[pos:]...)...)
			case 1: // delete a byte
				if pos < len(b) {
					b = append(b[:pos:pos], b[pos+1:]...)
				}
			case 2: // replace a byte
				if pos < len(b) {
					b[pos] = inserts[rng.Intn(len(inserts))][0]
				}
			}
		}
		checkAgainstGo(t, string(b))
		mutated++
	}
	t.Logf("%d mutated go.mod files compared", mutated)
}

// A hostile go.mod must not be able to make capture allocate gigabytes:
// the lexer materialises tokens, so go.mod has a much tighter cap (1 MiB)
// than the generic 16 MiB source limit.
func TestDetectBoundsTheSizeOfGoMod(t *testing.T) {
	dir := t.TempDir()
	write := func(size int) {
		t.Helper()
		pad := strings.Repeat("// pad\n", 1)
		body := "module m\ngo 1.21\n" + pad
		b := make([]byte, 0, size)
		b = append(b, body...)
		for len(b) < size {
			b = append(b, '\n')
		}
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(1 << 20) // exactly at the limit: fine
	if reqs, err := New().Detect(context.Background(), dir); err != nil || len(reqs) != 1 {
		t.Fatalf("1 MiB go.mod: %v %v", reqs, err)
	}
	for _, size := range []int{1<<20 + 1, 16 << 20} {
		write(size)
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := New().Detect(context.Background(), dir)
		runtime.ReadMemStats(&after)
		if err == nil || !strings.Contains(err.Error(), "size limit") {
			t.Fatalf("%d-byte go.mod: error = %v, want the size-limit error", size, err)
		}
		if grew := after.TotalAlloc - before.TotalAlloc; grew > 32<<20 {
			t.Errorf("%d-byte go.mod allocated %d MiB before being refused", size, grew>>20)
		}
	}
}
