# WOMM architecture (as implemented)

This document describes what is **actually built**, not the aspirational
design. The conceptual pipeline is:

```text
Detector (project, L0)  → core.Requirement + Evidence
Inspector (machine, L1) → core.Observation
Compare                 → core.Match
Reporting               → presentation    ← not implemented yet
CLI orchestration       → user-facing command surface
```

Frontier rule: **Detected ≠ Required, Observed ≠ Verified.** Detectors
never inspect the machine; inspectors never judge satisfaction;
comparison is pure logic; reporting is presentation only.

## Packages and responsibilities

| Package | Layer | Status | Responsibility |
|---|---|---|---|
| `cmd/womm` | CLI | merged (PR 1) | `main` → `internal/cli.Execute` |
| `internal/cli` | CLI | merged (PR 1) | cobra root + `version`; exit-code contract 0/1/2/3 (1 reserved, not produced yet) |
| `internal/core` | domain | merged (PR 1) | ecosystem-agnostic model: `Requirement`, `Evidence`, `Observation`, `Match`, `MatchStatus` |
| `internal/schema` | persistence | merged (PR 1) | `womm.yaml` schema v1: load, parse, validate; version-policy errors (`ErrVersion`), unknown-keys → warnings |
| `internal/detectors` | L0 | merged (PR 2) | `Detector` boundary: project filesystem reads only → `core.Requirement` |
| `internal/detectors/node` | L0 | merged (PR 2) | `NodeDetector` (package.json `engines.node` + `.nvmrc`, conflict-safe) and `PackageManagerDetector` (`packageManager`, Corepack hash subset) |
| `internal/inspect` | L1 | merged (PR 3) | `Inspector` boundary: demand-driven machine observation → `core.Observation` |
| `internal/inspect/node` | L1 | merged (PR 3) | `NodeInspector`: node/npm/pnpm/yarn `--version` probes with full L1 containment |
| `internal/compare` | logic | **open PR #4, not on main** | pure `Compare(req, obs) → core.Match` |
| reporting | presentation | **does not exist** | — |

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
   Pure: no I/O, no process execution, no printing.
4. **Report/CLI (future).** Rendering + exit-code mapping. Exit 1 will
   (eventually) mean semantic FAIL; nothing produces it today.

## Boundaries and dependencies (enforced)

- `core` depends on nothing ecosystem-specific; no core type may gain
  technology-specific detail.
- `schema` produces `core.Requirement` values — one shared model, no
  adapters between the loader and detectors.
- `detectors` never touches `inspect`; `inspect` never decides
  satisfaction; `compare` never does I/O; `cli` is the only printer.
- Inspection is demand-driven: an `Inspector` `Supports(name)` gate
  refuses anything outside its fixed tool list before resolution/exec.

## Known divergences from the conceptual model

Documented on purpose — do not "resolve" these silently; they are the
next slices (`docs/roadmap.md`):

1. `internal/compare` exists only on the open PR #4 branch
   (`feat/compare-engine`); not merged, not wired to the CLI.
2. No reporting package; no consumer of `core.Match` yet.
3. The CLI produces 0/2/3 only; exit 1 (semantic FAIL) is part of the
   public contract but not produced until verify+reporting exist.
4. `capture` / `verify` commands do not exist.

## Security invariants (invariant — do not weaken)

**L0 — project reads:**
- All declared-source reads go through `readFileIfPresent`, which
  resolves symlinks and refuses to read outside the project root.
- A broken symlink in a declared source is an explicit error, not
  silent absence (declaration implies source).

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
  `compare.ErrNameMismatch` / `ErrInvalidConstraint` /
  `ErrInvalidObservedVersion` — each a sentinel checked via
  `errors.Is`, never swallowed.
