# Roadmap — status as observed on `main` (v0.0.1 slice)

This file is the only place where the plan lives. Statuses reflect
reality: nothing listed as "completed" is aspirational, nothing listed
as future is implemented. PRs update this file; they don't improvise.

## Completed (merged to main)

- **Core domain model** (`internal/core`) — `Requirement`, `Evidence`,
  `Observation`, `Match`, `MatchStatus`.
- **Schema v1** (`internal/schema`) — `womm.yaml` load/validate/parse,
  version-policy errors, unknown-keys → warnings, evidence required.
- **CLI foundation** (`internal/cli`) — root + `version`, exit-code
  contract 0/1/2/3 (1 reserved; not produced yet).
- **L0 Node detection** (`internal/detectors/node`, PR #2) —
  `engines.node` / `.nvmrc` (conflict-safe conjunction, strict exact
  versions) and `packageManager` (npm/pnpm/yarn, Corepack
  sha1/sha224/sha512 subset); symlink-rooted L0 containment.
- **L1 Node inspection** (`internal/inspect/node`, PR #3) —
  `NodeInspector` for node/npm/pnpm/yarn: system-path allowlist,
  sanitized environment (`NODE_OPTIONS` stripped), neutral CWD, 5s
  process-group-bounded probes, 4KiB output cap, missing tools as
  observations.
- **Compare engine** (`internal/compare`, PR #4) — pure
  `Compare(Requirement, Observation) → Match`; `present`, exact semver
  (equality) and ranges (release versions only); explicit `unknown` +
  sentinel for name mismatches, contradictory observations
  (absent-with-version), malformed inputs and prerelease-vs-range
  (consumers disagree; WOMM refuses to guess). Not wired to the CLI.
- **Reporting** (`internal/report`, PR #6) — deterministic rendering
  of `[]core.Match` (fixed order, fixed wording, evidence listed,
  summary counts); PASS/FAIL/UNKNOWN/UNREACHABLE labels; unknown
  statuses refused. Presentation only; exit-code **1** semantics
  arrive with `verify`. Not wired to the CLI.
- **`womm capture`** (`internal/capture`, `internal/cli`, PR #7) —
  detect → deterministic `womm.yaml` (requirements sorted by name,
  evidence preserved verbatim, `requirements: []` when nothing is
  declared); `schema.Marshal` validated + round-trip checked;
  `--output`, `--force` (overwrites a regular file, never a
  symlink, never a merge); detection errors → exit 3, nothing
  written.

- **Hardening: npm range grammar** (`internal/semverrange`, PR #10)
  — Masterminds/semver accepts syntax npm rejects (`>=22, <25`,
  `!=`, `=>`, `~>`, `>=22 ||`); a range npm cannot parse never
  satisfies anything, so such declarations are now explicit errors in
  the detector and `unknown` in compare instead of a possible false
  PASS. Fuzzed.
- **Hardening: hostile input** (PR #11) — regular-file-only, bounded
  reads for project sources (16 MiB) and womm.yaml (4 MiB): a FIFO
  or an oversized file is an explicit error, never a hang or a
  truncated interpretation; UTF-8 BOM on package.json stripped like
  npm; `engines.node` stored normalized; requirement names validated
  (no whitespace/control chars); report escapes control characters.
- **`womm verify`** (`internal/verify`, `internal/cli`, PR #8) —
  load `womm.yaml` → inspect → compare → report → exit code. Partial
  `Observation{Present: true}` from the inspector on probe failure,
  mapped to `unreachable` by verify only; unsupported requirements
  and presence-less inspection failures stay operational errors.
  Exit codes 0/1/2/3 fully produced (3 beats 1).

## Next vertical slices (order)

1. **CLI UX + E2E** — flow wiring, help surfaces, end-to-end tests on
   the real pipeline.
2. **Hardening (security/fuzz/property)** — fuzz the parsers
   (`schema`, `nvmrc`, `packageManager`), property tests for
   `compare` determinism, remove holes documented in code comments.
3. **Docs / release** — README final shape, `CHANGELOG`, tag `v0.0.1`,
   release notes.
4. **Only after all of the above: new ecosystems** (Python, Docker,
   Go detectors; PostgreSQL/Redis services). Every new ecosystem is
   its own slice: detector + inspector + tests, no cross-cutting
   refactors.
