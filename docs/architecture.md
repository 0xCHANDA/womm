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
| `internal/cli` | CLI | merged (PR 1, 7, 8, 14) | cobra root + `version` + `capture` + `verify`; exit-code contract 0/1/2/3 fully produced; a fresh command tree per run; version settable via `-ldflags -X` |
| `internal/core` | domain | merged (PR 1) | ecosystem-agnostic model: `Requirement`, `Evidence`, `Observation`, `Match`, `MatchStatus` |
| `internal/schema` | persistence | merged (PR 1, PR 7) | `womm.yaml` schema v1: load, parse, validate, `Marshal` (deterministic, validated, round-trip safe); version-policy errors (`ErrVersion`), unknown-keys → warnings |
| `internal/detectors` | L0 | merged (PR 2) | `Detector` boundary: project filesystem reads only → `core.Requirement` |
| `internal/detectors/node` | L0 | merged (PR 2) | `NodeDetector` (package.json `engines.node` + `.nvmrc`, conflict-safe) and `PackageManagerDetector` (`packageManager`, Corepack hash subset) |
| `internal/inspect` | L1 | merged (PR 3, 8) | `Inspector` boundary: demand-driven machine observation → `core.Observation`; partial `Observation{Present: true}` next to a probe error |
| `internal/inspect/toolpath` | L1 | integration/v0.2 | `Resolver`: ordered, validated search list (explicit `--tool-dir` directories, the system allowlist, then the account's version-manager shim directories) → absolute executable path; refuses project-controlled directories and symlinks into the project; never the inherited `PATH`; also owns `AccountHome()` (user database, never `$HOME`) |
| `internal/inspect/probe` | L1 | integration/v0.2 | the one place a probe process is started: own process group, timeout then group SIGKILL then WaitDelay, 4 KiB output cap, child subreaper that adopts and kills `setsid` escapees, fixed args, allowlisted environment (`Env`, `PathFor`); shared by every ecosystem |
| `internal/inspect/gotool` | L1 | integration/v0.2 | `GoInspector`: `go version` with `GOTOOLCHAIN=local`, `GOENV=off`, telemetry redirected; version parsed strictly (a development build is unknown) |
| `internal/goversion` | logic | integration/v0.2 | Go version syntax (strict gate) and order on top of `go/version` — not semver |
| `internal/detectors/gomod` | L0 | integration/v0.2 | the `go` directive of `go.mod` → `Requirement{go, ">=V"}`; lexer follows the go command's reader; differential-tested against it |
| `internal/detectors/source` | L0 | integration/v0.2 | the one contained (`os.Root`), non-blocking, size-bounded read of a declared source, shared by every detector |
| `internal/inspect/node` | L1 | merged (PR 3) | `NodeInspector`: node/npm/pnpm/yarn `--version` probes with full L1 containment |
| `internal/semverrange` | logic | merged (PR 10) | npm range grammar gate over Masterminds/semver: rejects `,`, `!=`, `=>`, `=<`, `~>`, empty sets, qualifiers on wildcards; normalizes whitespace |
| `internal/compare` | logic | merged (PR 4) | pure `Compare(req, obs) → core.Match`; exact versions by equality, ranges for release versions only, prerelease-vs-range refused |
| `internal/capture` | orchestration (L0) | merged (PR 7) | `Capture(root)`: run the fixed detector set → sorted `schema.File`; `Write(path, file, force)`: never follows symlinks, never overwrites without `--force` |
| `internal/verify` | orchestration | merged (PR 8) | `Verify(file, inspectors) → Result{Matches, Errors}`: inspect → compare, maps known-present probe failures to `unreachable`; `ExitCode(Result)` |
| `internal/report` | presentation | merged (PR 6) | `Render([]core.Match)`: deterministic order + wording, `Summarize` counts; no inspection, no comparison, no exit codes. On `integration/v0.2`: `RenderJSON` — the public, versioned `--format json` document (`docs/output-json.md`), written as its own DTO; it presents the verdict and exit code it is given and decides neither |

## Data flow (current and planned)

1. **Detect (L0).** A `Detector` reads declared sources inside the
   project root and emits `[]core.Requirement` with explicit
   `Evidence` (source file, field, literal value). No requirements →
   empty slice, never an error.
2. **Inspect (L1).** An `Inspector` acts on one `Requirement` at a time
   and returns `core.Observation{Present, Version, Path}`; `Path` is
   the executable that was started (empty when nothing ran) — evidence
   shown by the report, never part of the comparison and never written
   to `womm.yaml`. Missing binaries are observations, not errors. Unsupported tools → `ErrUnsupportedTool`.
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
   as a diagnostic). Requirements no inspector supports, `services`
   and `environment` sections (no verifier in v0.1), and inspection
   failures where presence was never established, are operational
   errors: reported on stderr, never turned into a match, and the run
   is inconclusive.

   **Unreachable contract.** `core.StatusUnreachable` means "the
   target is known to be present, but WOMM could not query it". The
   knowledge comes from the inspector: when the binary resolved and
   started but the probe failed (timeout, non-zero exit, exec error),
   `Inspect` returns `Observation{Name, Present: true, Path}` together
   with the error. `verify` maps exactly that pair — present, same name,
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

Documented on purpose — do not "resolve" these silently; they are
deliberate v0.1 limitations (`docs/roadmap.md`, `CHANGELOG.md`):

1. `capture` does not detect conflicts with an existing womm.yaml
   (`--force` overwrites, it never merges).
2. `verify` observes only binaries in the fixed system path allowlist
   (`/usr/local/bin`, `/usr/bin`, `/bin`). A Node installed through a
   version manager under `$HOME` is reported as absent or shadowed by
   the system one — by design for v0.1 (never the inherited PATH),
   and visible in the report as what was actually observed. (On
   `integration/v0.2`, `--tool-dir` names extra directories; see
   `docs/proposals/version-managers.md`.)
3. On `main`, only the Node.js ecosystem has a detector and an inspector
   (`integration/v0.2` adds Go).

## Security invariants (invariant — do not weaken)

**L0 — project reads:**
- All declared-source reads go through `readFileIfPresent`, which
  opens every path component relative to the project root through
  `os.Root` (openat-style): a symlink that resolves outside the root
  is refused by the open itself, not by a path inspection a
  concurrent swap could invalidate. The open is non-blocking and the
  opened inode is fstat'ed (regular file, size) before reading.
  Absolute symlink targets inside the project are translated to
  root-relative paths and re-opened through the root.
- A broken symlink in a declared source is an explicit error, not
  silent absence (declaration implies source).
- Only regular files are read (a FIFO or device under a declared name
  cannot block WOMM; a directory is not a declaration), and reads are
  bounded (16 MiB; larger sources are explicit errors, never
  truncated).
- A UTF-8 BOM on package.json is stripped, exactly as npm does;
  everything else must be strict JSON. `engines.node` is stored in
  its normalized spelling (`semverrange.Normalize`), the verbatim
  literal stays in `Evidence.Value`.

**womm.yaml (`internal/schema`):**
- `Load` opens with `O_NOFOLLOW|O_NONBLOCK`, fstat's the opened inode
  (regular files only, bounded to 4 MiB) and refuses a symbolic link
  at the path (a project could point `womm.yaml` outside
  itself and have unknown-key warnings echo the target's keys).
- Requirement names must be plain identifiers: valid UTF-8, no
  whitespace, no control characters (they are matched against tool
  names and printed on their own report line).

**Report (`internal/report`):**
- Any name, constraint, observation or reason containing a control
  character or invalid UTF-8 is rendered quoted (Go syntax), so no
  input can add, break or forge a report line.

**Capture output (`internal/capture`):**
- Without `--force` the file is created with `O_CREAT|O_EXCL|O_NOFOLLOW`
  (atomic: whatever appears at the path first wins; a symlink there
  fails the create instead of being followed).
- With `--force` the target is never opened: the document goes to an
  `O_EXCL` temporary file in the same directory and is moved over the
  path with `rename(2)`. A swap of the path to a symlink, FIFO or hard
  link between the checks and the move can therefore neither redirect
  the write, block WOMM, nor modify another name of the inode. Proven
  by a swap-race stress test and a hard-link test; the previous
  `O_TRUNC` open blocked on a FIFO swap and wrote through hard links.

**L1 — machine probes (`internal/inspect/node`):**
- Executables resolve only from an ordered, validated list
  (`internal/inspect/toolpath`): directories the invoking user named
  with `--tool-dir`, then the hardcoded allowlist (`/usr/local/bin`,
  `/usr/bin`, `/bin`), then the fixed shim directories under the
  account's home (`~/.volta/bin`, `~/.asdf/shims`,
  `~/.local/share/mise/shims`, `~/.local/bin`; home from the user
  database, never `$HOME`; skipped with a warning unless owned by the
  invoking user or root, not world-writable, outside the project) —
  never the inherited `PATH`, never
  project-relative paths, never non-bare names. A user directory is
  refused when it is, or resolves into, the project (or the directory
  of the verified file), sits under `node_modules`, or is
  world-writable; a candidate that is a symlink into the project is an
  error (`ErrUnsafe`), never skipped. The candidate's own path is what
  runs (shims dispatch on their name), and it is recorded as
  `Observation.Path`.
- Execution is direct (`exec.CommandContext`, absolute resolved path,
  fixed `--version` arg) — no shell, no npm scripts, no `npx`/corepack.
- The probe environment is an **allowlist**: only locale settings
  (`LANG`, `LANGUAGE`, `LC_*`) are inherited; everything else the probe
  sees is forced (`PATH`, `HOME`, `COREPACK_*`, `YARN_IGNORE_PATH`,
  `npm_config_manage_package_manager_versions`). Version-manager shims
  are dispatchers steered by `VOLTA_HOME`, `MISE_*`, `ASDF_*`,
  `XDG_DATA_HOME`... which a launcher can point into the project, so no
  denylist can be complete. Cost: a tool that only starts with a custom
  variable is reported `UNREACHABLE`. The stripped-variable notes below
  remain true by construction.
- `NODE_OPTIONS` is stripped from the probe environment (code-execution
  vector: `--require` runs arbitrary JS before `--version` prints);
  `PATH` inside the probe is replaced: the system allowlist, preceded
  by the executable's own (already validated) directory when it is not
  a system one, so launchers such as nvm's `npm` find their sibling
  `node`.
- Loader injection (`LD_PRELOAD`, `LD_AUDIT`, `LD_LIBRARY_PATH`) and
  node's file-writing switches (`NODE_V8_COVERAGE`,
  `NODE_REDIRECT_WARNINGS`) are stripped; `HOME` and `COREPACK_HOME`
  are forced from the account's passwd entry (a Corepack shim runs
  whatever is in its cache directory), `XDG_CACHE_HOME` /
  `XDG_CONFIG_HOME` dropped.
- Corepack is forced offline and passive (`COREPACK_ENABLE_NETWORK=0`,
  `COREPACK_ENABLE_AUTO_PIN=0`, `COREPACK_ENABLE_STRICT=0`): a shim at
  `/usr/local/bin/pnpm` must neither download a package manager nor
  rewrite a `package.json` during a version probe. An uncached shim
  fails fast and is reported as unreachable.
- Working directory `/` — never the project root, and with no
  ancestor an unprivileged user can write to. This matters: yarn 1
  walks up from the cwd for `.yarnrc` `yarn-path` and executes that
  file even for `--version`; pnpm walks up for a `package.json`
  `packageManager` and downloads + runs that version. `os.TempDir()`
  (world-writable `/tmp`, or an inherited, possibly relative
  `$TMPDIR`) would hand both an attacker-chosen ancestor. The probe
  environment additionally forces `YARN_IGNORE_PATH=1` and
  `npm_config_manage_package_manager_versions=false`.
- Ctrl-C / SIGTERM / SIGHUP cancel the CLI context; cancellation kills the
  running probe's process group, so a hung tool does not outlive
  WOMM (measured: return 1 ms after the signal, descendants in the
  group gone). An interrupted probe is an operational error, never an
  `unreachable` verdict, and the CLI prints no report at all for a
  cancelled run — partial PASS lines would read as a result — only
  `error: verification cancelled; no result`, exit 3.
- Hard 5s timeout, process-group `SIGKILL` on cancel (`Setpgid` +
  negative-PID kill), `WaitDelay` (2s) as backstop, output capped at
  4KiB. WOMM is a child subreaper (`PR_SET_CHILD_SUBREAPER`,
  unprivileged, Linux): a descendant that leaves the process group
  (`setsid`) is orphaned by the group kill, reparents to WOMM instead
  of init, and is then SIGKILLed as WOMM's own child. Only WOMM's
  descendants can ever have WOMM as parent, killed processes stay
  zombies so their pids are not reused, and the sweep is bounded
  (500 ms) — the timeout guarantee never depends on it. The sweep
  runs after successful probes too (a shim must not leave a detached
  process behind) and is skipped when WOMM is pid 1 of its PID
  namespace, where every orphan would reparent to it. Measured
  before the change: WOMM returned at 7.0s (timeout + WaitDelay) and
  the escapee lived on under init; now it is gone.
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
