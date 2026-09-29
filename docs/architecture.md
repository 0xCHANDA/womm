# WOMM architecture (as implemented)

This document describes what is **actually built**, not the aspirational
design. The conceptual pipeline is:

```text
Detector (project, L0)  → core.Requirement + Evidence
Inspector (machine, L1) → core.Observation
Compare                 → core.Match
Reporting               → presentation (internal/report)
Verify orchestration    → internal/verify (unreachable mapping, exit code)
CLI                     → user-facing command surface
```

Frontier rule: **Detected ≠ Required, Observed ≠ Verified.** Detectors
never inspect the machine; inspectors never judge satisfaction;
comparison is pure logic; reporting is presentation only.

## Packages and responsibilities

| Package | Layer | Status | Responsibility |
|---|---|---|---|
| `cmd/womm` | CLI | merged (PR 1) | `main` → `internal/cli.Execute` |
| `internal/cli` | CLI | merged (PR 1, 7, 8) | cobra root + `version` + `capture` + `verify`; exit-code contract 0/1/2/3 fully produced; a fresh command tree per run |
| `internal/core` | domain | merged (PR 1) | ecosystem-agnostic model: `Requirement`, `Evidence`, `Observation`, `Match`, `MatchStatus` |
| `internal/schema` | persistence | merged (PR 1, PR 7) | `womm.yaml` schema v1: load, parse, validate, `Marshal` (deterministic, validated, round-trip safe); version-policy errors (`ErrVersion`), unknown-keys → warnings |
| `internal/detectors` | L0 | merged (PR 2) | `Detector` boundary: project filesystem reads only → `core.Requirement` |
| `internal/detectors/node` | L0 | merged (PR 2) | `NodeDetector` (package.json `engines.node` + `.nvmrc`, conflict-safe) and `PackageManagerDetector` (`packageManager`, Corepack hash subset) |
| `internal/inspect` | L1 | merged (PR 3, 8) | `Inspector` boundary: demand-driven machine observation → `core.Observation`; partial `Observation{Present: true}` next to a probe error |
| `internal/inspect/node` | L1 | merged (PR 3) | `NodeInspector`: node/npm/pnpm/yarn `--version` probes with full L1 containment |
| `internal/semverrange` | logic | merged (PR 10) | npm range grammar gate over Masterminds/semver: rejects `,`, `!=`, `=>`, `=<`, `~>`, empty sets, qualifiers on wildcards; normalizes whitespace |
| `internal/compare` | logic | merged (PR 4) | pure `Compare(req, obs) → core.Match`; exact versions by equality, ranges for release versions only, prerelease-vs-range refused |
| `internal/capture` | orchestration (L0) | merged (PR 7) | `Capture(root)`: run the fixed detector set → sorted `schema.File`; `Write(path, file, force)`: never follows symlinks, never overwrites without `--force` |
| `internal/verify` | orchestration | merged (PR 8) | `Verify(file, inspectors) → Result{Matches, Errors}`: inspect → compare, maps known-present probe failures to `unreachable`; `ExitCode(Result)` |
| `internal/report` | presentation | merged (PR 6) | `Render([]core.Match)`: deterministic order + wording, `Summarize` counts; no inspection, no comparison, no exit codes |

## Data flow (current and planned)

1. **Detect (L0).** A `Detector` reads declared sources inside the
   project root and emits `[]core.Requirement` with explicit
   `Evidence` (source file, field, literal value). No requirements →
   empty slice, never an error.
2. **Inspect (L1).** An `Inspector` acts on one `Requirement` at a time
   and returns `core.Observation{Present, Version}`. Missing binaries
   are observations, not errors. Unsupported tools → `ErrUnsupportedTool`.
3. **Compare.** `compare.Compare` pairs requirement + observation into
   `core.Match` with a `MatchStatus` (`pass` / `fail` / `unreachable` /
   `unknown`) and a deterministic, human-readable `Reason`.
   Pure: no I/O, no process execution, no printing. Constraint
   semantics: `present` (existence only), an exact strict semver
   version (equality, prerelease included), or a range in npm's
   grammar (`semverrange.Parse`; Masterminds-only syntax such as
   `>=22, <25` or `!=` is `ErrInvalidConstraint`) evaluated for
   release versions only. Three undecidable inputs are `unknown` plus
   a sentinel, never a verdict: a contradictory observation
   (`Present: false` with a version, `ErrInconsistentObservation`), a
   prerelease observed against a range (`ErrPrereleaseRange`: npm's
   engines check, node-semver's default and Masterminds/semver give
   three different answers, so WOMM picks none), and malformed
   inputs (`ErrInvalidConstraint`, `ErrInvalidObservedVersion`).
4. **Report.** `report.Render` writes one block per match in a fixed
   order (name, constraint, observation name; stable), with the
   status label, the constraint, a truthful description of the
   observation (absence, unknown version, contradictory states
   included), the reason and every evidence entry, then a summary
   line. `report.Summarize` counts statuses. An unknown status is
   `ErrUnknownStatus` before any byte is written. Presentation only.
5. **Capture (`womm capture [dir]`).** `capture.Capture` runs the
   fixed detector set on the project, refuses duplicate names,
   sorts requirements by name and validates the result;
   `capture.Write` serializes through `schema.Marshal`, parses the
   bytes back, and stores them at `<dir>/womm.yaml` (or `--output`).
   Existing files need `--force`; a symlink at the output path is
   refused in both modes (a project could point `womm.yaml` at any
   file). Detection errors abort with exit 3 and nothing is written.
6. **Verify (`womm verify [dir]`).** `schema.Load` → `verify.Verify`
   → `report.Render` → `verify.ExitCode`. For each requirement (in
   name order) the first supporting inspector observes the machine;
   the observation goes through `compare.Compare` and the match is
   kept whatever its status (a compare sentinel is echoed to stderr
   as a diagnostic). Requirements no inspector supports, and
   inspection failures where presence was never established, are
   operational errors: reported on stderr, never turned into a match.

   **Unreachable contract.** `core.StatusUnreachable` means "the
   target is known to be present, but WOMM could not query it". The
   knowledge comes from the inspector: when the binary resolved and
   started but the probe failed (timeout, non-zero exit, exec error),
   `Inspect` returns `Observation{Name, Present: true}` together with
   the error. `verify` maps exactly that pair — present, same name,
   no version, error — to an `unreachable` match whose reason carries
   the probe failure. Compare never sees it and gains no error
   mapping; inspectors decide nothing.

   **Exit-code contract** (`verify.ExitCode`, pure):

   | Code | When |
   |---|---|
   | 0 | every match is PASS and no operational error |
   | 1 | at least one FAIL and nothing inconclusive |
   | 2 | usage error (CLI only) |
   | 3 | any UNKNOWN or UNREACHABLE match, any operational error, malformed or unloadable `womm.yaml` — 3 wins over 1 |

## Boundaries and dependencies (enforced)

- `core` depends on nothing ecosystem-specific; no core type may gain
  technology-specific detail.
- `schema` produces `core.Requirement` values — one shared model, no
  adapters between the loader and detectors.
- `detectors` never touches `inspect`; `inspect` never decides
  satisfaction; `compare` never does I/O; `report` only formats what
  it is given; `cli` is the only place that writes to the process
  streams and exits.
- Inspection is demand-driven: an `Inspector` `Supports(name)` gate
  refuses anything outside its fixed tool list before resolution/exec.

## Known divergences from the conceptual model

Documented on purpose — do not "resolve" these silently; they are the
next slices (`docs/roadmap.md`):

1. `capture` does not detect conflicts with an existing womm.yaml
   (`--force` overwrites, it never merges).
2. `verify` observes only binaries in the fixed system path allowlist
   (`/usr/local/bin`, `/usr/bin`, `/bin`). A Node installed through a
   version manager under `$HOME` is reported as absent or shadowed by
   the system one — by design for v0.1 (never the inherited PATH),
   and visible in the report as what was actually observed.
3. Only the Node.js ecosystem has a detector and an inspector.

## Security invariants (invariant — do not weaken)

**L0 — project reads:**
- All declared-source reads go through `readFileIfPresent`, which
  resolves symlinks and refuses to read outside the project root.
- A broken symlink in a declared source is an explicit error, not
  silent absence (declaration implies source).

**Capture output (`internal/capture`):**
- The output path is `Lstat`ed and opened with `O_NOFOLLOW`
  (`O_EXCL` without `--force`): a symlink planted as `womm.yaml` is
  never followed, and nothing can be written outside the path the
  user named.

**L1 — machine probes (`internal/inspect/node`):**
- Executables resolve only from a hardcoded allowlist
  (`/usr/local/bin`, `/usr/bin`, `/bin`) — never the inherited `PATH`,
  never project-relative paths, never non-bare names.
- Execution is direct (`exec.CommandContext`, absolute resolved path,
  fixed `--version` arg) — no shell, no npm scripts, no `npx`/corepack.
- `NODE_OPTIONS` is stripped from the probe environment (code-execution
  vector: `--require` runs arbitrary JS before `--version` prints);
  `PATH` inside the probe is replaced with the sanitized allowlist.
- Neutral working directory (`os.TempDir()`), never the project root.
- Hard 5s timeout, process-group `SIGKILL` on cancel (`Setpgid` +
  negative-PID kill), `WaitDelay` as backstop, output capped at 4KiB.
- Missing binary → `Observation{Present: false}`; unparseable output →
  `Observation{Version: ""}`. Both are observations, never fabricated.

**Explicit refusal semantics (project-wide):**
- `ErrEvidenceConflict` (detectors), `ErrUnsupportedTool` /
  `ErrProbeTimeout` (inspectors), `schema.ErrVersion`,
  `compare.ErrNameMismatch` / `ErrInconsistentObservation` /
  `ErrInvalidConstraint` / `ErrInvalidObservedVersion` /
  `ErrPrereleaseRange` — each a sentinel checked via `errors.Is`,
  never swallowed. A `compare` error always comes with
  `StatusUnknown`: an error never accompanies a PASS or a FAIL.
