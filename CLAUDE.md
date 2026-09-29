# CLAUDE.md — WOMM

Working instructions for any AI coding agent (Claude Code / Claude Cloud)
opening a session in this repository. Read this and the docs it links
before writing any code.

## Project identity

**WOMM ("Works On My Machine")** is an open-source Go CLI that diagnoses
development environments: it discovers what a project *requires*
(Node version, package manager, services), observes what a machine
*actually has*, and compares the two with explicit evidence — so "it
works on my machine" becomes a report, not an anecdote.

**What WOMM is NOT:**

- **Not a provisioner.** WOMM never installs anything, never upgrades
  toolchains, never mutates the machine to make it "compatible".
- **Not a project-code executor.** WOMM never runs a project's
  scripts, lifecycle hooks or arbitrary commands. Only fixed,
  known `--version` probes of system tools (this is future-explicit:
  any exception must be defined by a new feature with strict limits).
- **Not a guessing tool.** Ambiguous or malformed declarations are
  hard errors, never silently interpreted into a requirement.

Current status: v0.1 vertical slice (Linux + Node.js) is functionally
complete: `capture` and `verify` work end to end. See
`docs/roadmap.md` for what is real vs. pending.

## Architecture

```text
Detector (project, L0)  → core.Requirement (+ Evidence)
Inspector (machine, L1) → core.Observation
Compare                 → core.Match
Reporting (internal/report) → CLI (verify: internal/verify → exit code)
```

Frontier rule: **Detected ≠ Required, Observed ≠ Verified** — the
detector never inspects the machine, the inspector never judges
satisfaction, comparison is pure logic, reporting is presentation only.

Where things live (on `main`; see `docs/architecture.md` for detail):

| Piece | Location | Notes |
|---|---|---|
| Entry point | `cmd/womm/main.go` | calls `internal/cli.Execute` |
| CLI | `internal/cli` | cobra; `version`, `capture`, `verify`; exit-code contract 0/1/2/3 |
| Domain model | `internal/core` | `Requirement`, `Observation`, `Evidence`, `Match`, `MatchStatus` |
| Schema v1 | `internal/schema` | `womm.yaml` load/validate/parse/marshal |
| Detection (L0) | `internal/detectors` + `internal/detectors/node` | `NodeDetector`, `PackageManagerDetector` |
| Inspection (L1) | `internal/inspect` + `internal/inspect/node` | `NodeInspector` (node/npm/pnpm/yarn) |
| Capture | `internal/capture` | detectors → sorted `schema.File`; symlink-safe `Write` |
| Range grammar | `internal/semverrange` | npm range syntax gate over Masterminds (detector + compare) |
| Comparison | `internal/compare` | pure `Compare`; exact/range/present; prerelease-vs-range refused |
| Reporting | `internal/report` | `Render`/`Summarize`; presentation only |
| Verify | `internal/verify` | inspect → compare; unreachable mapping; `ExitCode` (3 beats 1) |

## Engineering principles

These are enforced by the code today — keep enforcing them:

- **Explicit errors over guessing.** Unsupported selectors, invalid
  constraints and conflicting evidence abort with sentinels
  (`ErrEvidenceConflict`, `ErrUnsupportedTool`, `schema.ErrVersion`).
  Never reinterpret "22" as "22.0.0" or pick "the strictest".
- **Evidence-backed.** Every `Requirement` carries `Evidence`; schema
  validation rejects requirements without it.
- **No silent coercion.** Strict semver only
  (`semver.StrictNewVersion`); `lts/*`, short versions and tags are
  explicit errors, never normalized. Ranges go through
  `semverrange.Parse` (npm grammar), never raw `semver.NewConstraint`.
- **No hidden machine mutation.** Inspectors execute only resolved
  system binaries with fixed `--version` args — no shell, no project
  code, no `npx`/corepack.
- **Predictable exit codes.** 0 success, 1 semantic FAIL, 2 usage,
  3 inconclusive/execution/config (3 takes precedence over 1).
- **Deterministic, composable packages.** `compare` is pure (no I/O);
  detection/inspection return values, never print.
- **Failure is surfaced, not masked.** Broken symlinks and missing
  versions are explicit states (`Observation.Version: ""` =
  unknown, never fabricated).

## Scope discipline

A session implements **only the assigned slice**. Do not add, unless
the mission explicitly asks: Python/Docker/Rust/Java ecosystems,
provisioning, telemetry, `capture`/`verify` beyond their defined
slice, new detectors/inspectors, or unrelated refactors. The roadmap
surface (`docs/roadmap.md`) is the only place where the plan lives;
PRs keep it updated, they don't improvise features.

## Git rules

- One task → one branch → one scope → one PR. Branch naming:
  `feat/…`, `fix/…`, `docs/…`, `chore/…`.
- **Never change `git config user.name` or `user.email`.** Identity is
  already configured and verified (`0xCHANDA`).
- **No attribution trailers of any kind**: no `Co-Authored-By:`,
  no "Generated with …", no Claude/Anthropic references in commits
  or PRs (repository-level Claude setting enforces this too).
- No force push. Never rewrite `main`. Do not push commits authored
  as anyone but the configured identity.
- Small, coherent commits; Conventional Commits style
  (`feat:`, `fix:`, `docs:`, `chore:`, `ci:`, `test:`).
- Do not push to branches you don't own (e.g. open PRs). Open your PR
  from your own branch.

Before merging any remote work, verify authorship:
`scripts/check-commit-attribution.sh` (see `docs/agent-workflow.md`).

## Validation

These commands are the contract; run all of them before opening or
updating a PR (verify them yourself — they are small):

```sh
gofmt -l .                 # must be empty
go vet ./...
go test -count=1 ./...
go test -race ./...
go build ./cmd/womm
```

CI (`.github/workflows/ci.yml`) re-runs these plus a
`go mod tidy` reproducibility check, with `GOTOOLCHAIN=local`
(Go 1.24 baseline — no silent toolchain upgrades).

## Security boundaries (already invariant — do not weaken)

- **L0 filesystem containment** (`internal/detectors/node/helpers.go`):
  reads resolve symlinks and refuse to read anything outside the
  project root; broken symlinks in declared sources are errors;
  regular files only, bounded to 16 MiB (womm.yaml: 4 MiB).
- **L1 system-path allowlist** (`internal/inspect/node/path.go`):
  executables resolve only from `/usr/local/bin`, `/usr/bin`, `/bin`
  — never the inherited `PATH`, never the project tree.
- **Sanitized probe environment**: `NODE_OPTIONS` stripped; sanitized
  `PATH`; neutral working directory (never the project root).
- **Bounded execution**: 5s probe timeout with process-group kill and
  `WaitDelay`; output capped at 4KiB.
- **Openness is an observation, not an error**: a missing binary is
  `Observation{Present: false}` — reported, never guessed around.

## Definition of done

A task is done only when ALL of these hold:

1. New/changed behavior covered by focused unit tests, including edge
   cases and error paths (this repo tests refusals explicitly).
2. `gofmt -l .` empty; `go vet ./...` clean; tests pass
   `-count=1`; `-race` for anything touching goroutines, exec or
   concurrent state; `go build ./cmd/womm` succeeds.
3. Public behavior changes are reflected in `README.md` /
   `docs/` (architecture + roadmap status).
4. No scope creep: the diff contains only the assigned slice.
5. No attribution trailers; commits under the configured identity.
6. Final message includes the validation report (commands + outcome).

Deep background: `docs/architecture.md`, `docs/development.md`,
`docs/agent-workflow.md`, `docs/agent-task-template.md`.
