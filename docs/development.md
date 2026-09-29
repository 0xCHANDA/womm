# Development guide

## Requirements

- **Go 1.24** (`go.mod` says `go 1.24`). CI pins `GOTOOLCHAIN=local` +
  `setup-go@v5` with `go-version: '1.24'` — no silent toolchain
  upgrades anywhere in the pipeline. Verify locally with
  `go env GOVERSION` (must be `go1.24.*`).
- Linux is the only supported development target for v0.0.1 (the
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

CI (`.github/workflows/ci.yml`) runs all of the above on every push and
PR, plus a `go mod tidy` reproducibility check (drifting `go.mod` /
`go.sum` fails the build).

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

## Versioning / release policy (pre-release)

- `internal/cli` holds the literal version (`0.0.1-dev`). There are
  **no git tags and no releases yet** — intentional until the
  vertical slice is reviewed for release.
- When the slice closes: tag `v0.0.1` on `main`, wire the version to
  build metadata (`-ldflags "-X ...cli.version=x"` or similar), add a
  short `CHANGELOG`. Do **not** bump versions from a feature slice.
