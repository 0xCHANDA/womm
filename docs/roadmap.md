# Roadmap — status as observed on `main` (v0.1 slice)

This file is the only place where the plan lives. Statuses reflect
reality: nothing listed as "completed" is aspirational, nothing listed
as future is implemented. PRs update this file; they don't improvise.

## Completed (merged to main)

- **Core domain model** (`internal/core`) — `Requirement`, `Evidence`,
  `Observation`, `Match`, `MatchStatus`.
- **Schema v1** (`internal/schema`) — `womm.yaml` load/validate/parse,
  version-policy errors, unknown-keys → warnings, evidence required.
- **CLI foundation** (`internal/cli`) — root + `version`; exit-code
  contract 0/1/2/3 (all produced since `verify`).
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
  (consumers disagree; WOMM refuses to guess). Wired through `verify`.
- **Reporting** (`internal/report`, PR #6) — deterministic rendering
  of `[]core.Match` (fixed order, fixed wording, evidence listed,
  summary counts); PASS/FAIL/UNKNOWN/UNREACHABLE labels; unknown
  statuses refused. Presentation only; exit-code **1** semantics
  arrived with `verify`. Wired through `verify`.
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
- **Campaign II hardening** (PRs #19–#22, #26) — `capture --force`
  replaces atomically (temp file + rename: no hard-link write-through,
  no FIFO hang); a cancelled run yields no verdict; child subreaper
  kills `setsid`-escaped probe descendants; numeric components capped
  at npm's `MAX_SAFE_INTEGER` with a 2 700-pair differential corpus
  against node-semver; flaky race test fixed.
- **Campaign II validation and release engineering** (PRs #23–#25,
  #27–#29) — threat model, version-manager proposal, v0.2 proposal,
  dormant tag-triggered release workflow, acceptance matrix,
  exit-code fuzz target, docs truth audit.
- **Hardening: account home without cgo** (Campaign III RC audit) —
  `os/user.Current()` and `user.LookupId` fall back to the inherited
  `$HOME` without cgo (the release configuration) when the uid has no
  passwd entry and `$USER` is set, which re-opened the Corepack
  `COREPACK_HOME` path closed by #33. Reproduced on the RC binary
  (`HOME=<project>` reached the probe), fixed by reading `/etc/passwd`
  directly; CI now also runs the tests with `CGO_ENABLED=0`.
- **Hardening: `.nvmrc` per nvm** (PR #18, from independent review)
  — a comment-only `.nvmrc` or a `node=<v>` setting produced silent
  absence although nvm rejects both; now explicit errors, inline `#`
  comments stripped like nvm, duplicated settings refused.
- **Hardening: unverifiable sections** (PR #17, from independent
  review) — `services` / `environment` declarations were silently
  skipped (a services-only file verified clean with exit 0); now
  operational errors, exit 3. `schema.Load` refuses a symlinked
  `womm.yaml` (a project could steer the read outside itself).
- **Hardening: range grammar, round two** (PR #16, from independent
  review) — `>=20.0.0-next.1` was falsely rejected; `x.1.2`, `>x` and
  numbers beyond 2^64 were accepted and evaluated (PASS/UNKNOWN);
  `=24.7.0` / `v24.7.0` are now exact comparators in compare.
- **Hardening: probe cwd** (PR #15, from independent adversarial
  review) — probes ran from `os.TempDir()`: yarn 1 executes
  `.yarnrc` `yarn-path` found by walking up from the cwd (even for
  `--version`), pnpm downloads and runs an ancestor `packageManager`;
  `/tmp` is world-writable and `$TMPDIR` may be relative. Now cwd is
  `/`, plus `YARN_IGNORE_PATH=1` and
  `npm_config_manage_package_manager_versions=false`; Ctrl-C cancels
  the probe.
- **Hardening: probe environment** (PR #12) — Corepack forced
  offline/passive in every probe (no downloads, no package.json
  auto-pin); bounded return proven against a `setsid`-escaped
  descendant (timeout + WaitDelay).
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

- **CLI UX + E2E** (PR #9) — end-to-end suite over realistic
  fixtures with explicit machine profiles, repeated-run determinism,
  compiled-binary exit codes.
- **Hardening: fuzz** (PR #13) — fuzz targets for `schema`, `.nvmrc`,
  `packageManager`, `package.json`, `compare`, `report`; the report
  now escapes evidence source/field (found by fuzzing).
- **Docs / release readiness** (PR #14) — README rewritten to the
  real surface, `CHANGELOG.md` with the v0.1.0 candidate notes,
  version settable from the tag via `-ldflags -X`.

## Remaining before a release

1. **Human decision: tag.** Everything above is on `main`; cutting
   the first tag (`v0.1.0` per the release notes) is a human action —
   no session tags or publishes.

## v0.2 candidates

Ranked in `docs/proposals/v0.2.md`: version-managed Node installations
(`docs/proposals/version-managers.md`), machine-readable output,
`explain`/`diff`, a Go ecosystem slice, services. Recommendation
there: version managers, then JSON output, then Go if time remains.

## Next vertical slices (order)

1. **Only after the release: new ecosystems** (Python, Docker,
   Go detectors; PostgreSQL/Redis services). Every new ecosystem is
   its own slice: detector + inspector + tests, no cross-cutting
   refactors.
