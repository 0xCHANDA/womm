# Development guide

## Requirements

- **Go 1.24** (`go.mod` says `go 1.24`). CI pins `GOTOOLCHAIN=local` +
  `setup-go@v5` with `go-version: '1.24'` — no silent toolchain
  upgrades anywhere in the pipeline. Verify locally with
  `go env GOVERSION` (must be `go1.24.*`).
- Linux is the only supported development target for v0.1 (the
  inspector's path allowlist and probe hardening are Linux-specific).

## Commands

```sh
# format check (must output nothing)
gofmt -l .

# static analysis
go vet ./...

# tests (no cache, so CI and local runs are equivalent)
go test -count=1 ./...

# race detector (required for anything touching exec/goroutines)
go test -race ./...

# build
go build ./cmd/womm

# run
./womm --help
./womm version
./womm capture ./path/to/project
./womm verify ./path/to/project   # exit 0/1/2/3
```

CI (`.github/workflows/ci.yml`) runs exactly the above on every push
and PR, plus a `go mod tidy` reproducibility check (drifting `go.mod`
/ `go.sum` fails the build).

## Layout

```text
cmd/womm/            entry point
internal/cli/        cobra CLI, exit-code contract
internal/core/       domain model (Requirement, Observation, Evidence, Match)
internal/schema/     womm.yaml v1 load/validate/parse
internal/detectors/  L0 boundary + node detectors
internal/inspect/    L1 boundary + node inspector
internal/compare/    pure comparison
internal/capture/    detect → womm.yaml (L0 orchestration)
internal/verify/     load → inspect → compare, unreachable mapping, exit code
internal/report/     deterministic rendering of matches
docs/                architecture, roadmap, agent workflow, task template
scripts/             small verification helpers
```

`internal/*` is not importable from outside the module by design —
there is no public Go API; the CLI is the API.

## Conventions

- **Conventional Commits**: `feat:`, `fix:`, `docs:`, `chore:`,
  `ci:`, `test:`. Small, coherent commits.
- **No attribution trailers** (no `Co-Authored-By:`, no
  "Generated with …"). Identity is configured; never change
  `user.name` / `user.email`.
- **Zero dependencies policy on essentials**: the three deps in
  `go.mod` (semver, cobra, yaml) are deliberate. Adding another
  requires justification in the PR description.
- **Test style**: focused unit tests, table-driven where natural;
  refusals are tested explicitly ("unsupported must not execute",
  "symlink escape must abort"). Same-package tests may substitute
  `resolvePath` / shrink `timeout`; production defaults stay real.
- **Errors**: sentinel errors checked with `errors.Is`; wrapped with
  `%w` context; user-facing wording speaks the user's problem, not
  internal terminology.

## Fuzzing

Every parser and the renderer has a fuzz target whose seed corpus runs
as an ordinary test. To fuzz for real:

```sh
go test -run '^$' -fuzz '^FuzzParse$' -fuzztime 30s ./internal/schema/
go test -run '^$' -fuzz '^FuzzParse$' -fuzztime 30s ./internal/semverrange/
go test -run '^$' -fuzz '^FuzzPackageJSON$' -fuzztime 30s ./internal/detectors/node/
go test -run '^$' -fuzz '^FuzzCompare$' -fuzztime 30s ./internal/compare/
go test -run '^$' -fuzz '^FuzzRender$' -fuzztime 30s ./internal/report/
go test -run '^$' -fuzz '^FuzzRenderJSON$' -fuzztime 30s ./internal/report/
go test -run '^$' -fuzz '^FuzzRenderPath$' -fuzztime 30s ./internal/report/
```

A crasher lands in `testdata/fuzz/<Target>/`; commit it with the fix.

## Differential semver corpus

`internal/compare/testdata/node-semver-corpus.json` holds range ×
version pairs with node-semver's own answers (`validRange`,
`satisfies` in default and `includePrerelease` modes). The test
`TestDifferentialAgainstNodeSemver` needs no Node runtime. To extend
or regenerate it (research tooling only — Node is never a runtime
dependency):

```sh
SEMVER_PATH=/path/to/node_modules/semver \
  node internal/compare/testdata/node-semver-corpus.gen.js \
  > internal/compare/testdata/node-semver-corpus.json
```

## Versioning / release policy (pre-release)

- `internal/cli` holds the development literal (`0.0.1-dev`) in
  `var version`; a release build sets it from the tag:

  ```sh
  go build -ldflags "-X github.com/0xCHANDA/womm/internal/cli.version=0.1.0" ./cmd/womm
  ```

- There are **no git tags and no releases yet**. `CHANGELOG.md` holds
  the candidate notes for the first tag; cutting the tag is a human
  decision, never an agent action.
- Before tagging: rename the CHANGELOG section `[Unreleased] — v0.1.0
  candidate` to `[0.1.0] — <date>`, point its compare link at
  `...v0.1.0`, and commit that on `main`; the release notes link to
  CHANGELOG by version heading.
- `.github/workflows/release.yml` is dormant until a `v*.*.*` tag is
  pushed. It then validates, builds static Linux amd64/arm64 binaries
  (`-trimpath`, `CGO_ENABLED=0`, version from the tag via `-ldflags
  -X`), writes `SHA256SUMS`, and attaches them to a GitHub Release for
  that tag. Job permissions: `contents: read` for builds, `contents:
  write` only for the publish step. Reproduce a release binary
  locally with:

  ```sh
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
    -ldflags "-s -w -X github.com/0xCHANDA/womm/internal/cli.version=0.1.0" \
    -o womm_0.1.0_linux_amd64 ./cmd/womm
  sha256sum womm_0.1.0_linux_amd64
  ```
- Do **not** bump the version literal from a feature slice; the tag
  carries the version.
