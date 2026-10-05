# Changelog

All notable changes to WOMM are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions
follow semver. No tag exists yet: the entry below is the release
candidate content for the first tag, to be cut by a human.

## [Unreleased] — v0.2 development (`integration/v0.2` only; not on `main`)

### Added

- `core.Observation.Path`: the absolute path of the executable that
  produced an observation (empty when nothing ran). `verify` reports it
  (`observed 24.7.0 at /usr/bin/node`, also for `UNREACHABLE` tools);
  it is host evidence, never compared and never written to `womm.yaml`.
- `womm verify --tool-dir <absolute-dir>` (repeatable): extra directories
  to resolve `node`/`npm`/`pnpm`/`yarn` from — nvm, fnm, Volta, asdf,
  mise bin/shim directories — consulted in the order given **before**
  the system directories, never the inherited `PATH`. Refused (usage
  error, exit 2): relative paths, the project or anything inside it
  (also the directory of `-f`), `node_modules`, world-writable
  directories, symlinks landing in the project. A directory owned by
  another user is accepted with a `warning:`. A binary that is a symlink
  into the project is refused (exit 3), never run and never stepped
  over. New package `internal/inspect/toolpath` owns resolution.
- Go ecosystem (second ecosystem; proves the architecture generalizes):
  `capture` reads the `go` directive of `go.mod` into `go: >=V`
  (`toolchain` is a suggestion for GOTOOLCHAIN=auto and is ignored); `verify`
  probes `go version` (GOTOOLCHAIN=local, GOENV=off, telemetry redirected)
  and compares in Go's order via `go/version` — `1.21 < 1.21rc1 <
  1.21.0`, not semver. A development build or vendor-suffixed version is
  unknown. Shared infrastructure extracted from the Node code:
  `internal/inspect/probe` (process containment, environment allowlist),
  `internal/detectors/source` (contained L0 read; `go.mod` has a 1 MiB cap
  of its own). The `go.mod` reader follows the go command's tokenization;
  an independent review found punctuation cases where it disagreed (a file
  the go command reads as `go 1.99` became zero requirements) — fixed, with
  a second differential over byte-mutated real `go.mod` files. Differential tests
  against the real go command (go.mod reading; accept/reject of `go X`).
- `womm verify --format json`: one deterministic JSON document on stdout
  (schema version 1, explicit public DTO, documented in
  `docs/output-json.md`) with requirements, status, reason, observation
  incl. the executed `path`, evidence, classified operational errors and
  a summary. Exit code and stderr are identical to the human format;
  no document when nothing was verified (usage error, missing/malformed
  `womm.yaml`, interrupted run). `verify.Verdict` / `verify.ErrorKind`
  classify by sentinel, never by text; inspection failures now wrap
  `verify.ErrInspectionFailed` (message unchanged).
- Implicit version-manager directories: after `--tool-dir` and the system
  directories, `verify` also searches `~/.volta/bin`, `~/.asdf/shims`,
  `~/.local/share/mise/shims` and `~/.local/bin` under the account's home
  (from the user database, never `$HOME`). A directory is used only when
  it exists, is owned by the invoking user or root, is not
  world-writable and lies outside the project and `node_modules`;
  otherwise it is skipped with a `warning:` on stderr. A system tool
  always wins. nvm and fnm: use `--tool-dir`. The account-home lookup moved
  to `toolpath.AccountHome`.
- Review fixes for the directory support: a `--tool-dir` (or account home)
  containing `:` or a control character is refused (it would split the
  probe `PATH`); an unusable entry named like the tool in a user
  directory is an error instead of a silent step to the system binary;
  an unsearchable implicit shim directory is skipped with a warning;
  every path in a message is quoted.
- The probe environment is now an allowlist (locale only inherited;
  everything else forced). Version-manager shims are dispatchers driven
  by `VOLTA_HOME`/`MISE_*`/`ASDF_*`/`XDG_DATA_HOME`, which a launcher can
  aim at the project; found by an independent security review of the
  shim support. A tool that only starts with a custom variable is
  reported `UNREACHABLE`.
- Probes of binaries from a user directory run with that directory first
  on the probe `PATH`, so launcher scripts (`#!/usr/bin/env node`) find
  their sibling node; system binaries keep the fixed system `PATH`.

## [Unreleased] — v0.1.0 candidate (Linux + Node.js vertical slice)

### Added

- `womm capture [dir]`: reads `package.json` (`engines.node`,
  `packageManager`) and `.nvmrc` inside the project root and writes a
  deterministic `womm.yaml` (schema v1; requirements sorted by name,
  evidence verbatim, `requirements: []` when nothing is declared).
  `--output`, `--force`. (#7)
- `womm verify [dir] [-f file]`: loads `womm.yaml`, probes `node`,
  `npm`, `pnpm`, `yarn` with fixed `--version` invocations, compares
  and prints a report; exit codes 0 (all pass) / 1 (conclusive fail)
  / 2 (usage) / 3 (inconclusive: `UNKNOWN`, `UNREACHABLE`, malformed
  file or operational failure; 3 beats 1). (#8)
- Pure comparison engine: `present`, exact versions (equality,
  prerelease included), npm-grammar ranges for release versions. (#4)
- Deterministic report renderer (`PASS` / `FAIL` / `UNKNOWN` /
  `UNREACHABLE`, evidence lines, summary). (#6)
- `UNREACHABLE` contract: an inspector that found a tool but could not
  query it returns a partial observation (`Present: true`, no
  version); `verify` maps exactly that to `UNREACHABLE`. (#8)
- End-to-end suite over realistic fixtures and the compiled binary;
  acceptance matrix over realistic project and `womm.yaml` shapes;
  fuzz targets for every parser, the renderer and the exit-code
  aggregation. (#9, #13, #25, #28)
- Tag-triggered release workflow (dormant until a human pushes a
  `v*` tag): static Linux amd64/arm64 builds, version from the tag,
  `SHA256SUMS`, GitHub Release. (#24)
- Threat model (`docs/threat-model.md`) and design proposals for
  version-managed Node and v0.2 scope. (#23, #29)

### Security / hardening

- L0 containment: declared-source reads go through `os.Root`
  (openat-style, race-free: a symlink swapped in mid-read cannot
  escape the project); broken symlinks are errors; regular files only,
  decided on the opened inode with a non-blocking open (a FIFO cannot
  block WOMM even when swapped in after a check); reads bounded at
  16 MiB (`womm.yaml`: 4 MiB, `O_NOFOLLOW|O_NONBLOCK` + fstat).
  (#2, #11, #34)
- L1 containment: tools resolve only from `/usr/local/bin`,
  `/usr/bin`, `/bin`; no shell; working directory `/` (no writable
  ancestors: yarn 1 `yarn-path` and pnpm self-version-management walk
  up from the cwd and would execute attacker-chosen code from `/tmp`
  or `$TMPDIR`); `NODE_OPTIONS` stripped; Corepack forced offline and
  passive; `YARN_IGNORE_PATH=1`;
  `npm_config_manage_package_manager_versions=false`; `HOME` and
  `COREPACK_HOME` forced from the account's passwd entry, `XDG_*`
  cache/config, `LD_PRELOAD`/`LD_AUDIT`/`LD_LIBRARY_PATH`,
  `NODE_V8_COVERAGE`/`NODE_REDIRECT_WARNINGS` stripped (#33; the
  account home is read from `/etc/passwd` / the C library and never
  falls back to the inherited `$HOME`, which `os/user` does without cgo
  for a uid with no passwd entry — Docker `--user`, OpenShift); 5s timeout with
  process-group kill and a 2s wait backstop; output capped at 4 KiB;
  Ctrl-C, SIGTERM and SIGHUP kill a running probe and yield no
  verdict (no partial report, exit 3); WOMM is a child subreaper so a
  `setsid`-escaped probe descendant is adopted and killed too, after
  successful probes as well (skipped when WOMM is pid 1 of a PID
  namespace). (#3, #12, #15, #20, #21, #35)
- Version ranges are gated on npm's grammar before evaluation
  (`>=22, <25`, `!=`, `=>`, `~>`, empty sets, `x.1.2`, `>x`, numeric
  components above npm's `MAX_SAFE_INTEGER` (2^53-1) in a range or in
  an observed version are explicit errors, never a false PASS;
  prerelease identifiers containing `x` such as `>=20.0.0-next.1` are
  accepted). A differential corpus of 2 700+ pairs generated from
  node-semver 7.7.4 pins the contract: WOMM never says PASS where
  node-semver rejects the range or is unsatisfied in both modes, and
  never FAIL where both modes are satisfied. (#10, #16, #22)
  `~0.0.0` (evaluated as "any version" by the underlying library,
  `<0.1.0-0` by npm), U+0085 as a separator, and ranges whose derived
  bound would exceed npm's limit (`^9007199254740991`) are refused.
  (#36)
- `package.json` keys are matched exactly like npm (`Engines`, `Node`,
  `PackageManager` no longer honoured or able to shadow the real
  keys); Corepack `+sha256.`/`+sha384.` descriptors accepted (Corepack
  hashes with any Node digest algorithm); `.nvmrc` settings keys and
  ASCII-only trimming follow nvm; requirement names refuse Unicode
  format characters (zero-width, bidi overrides); a multi-document
  `womm.yaml` is malformed. (#36)
- A prerelease observed against a range is `UNKNOWN` rather than a
  verdict, because npm's engines check, node-semver's default and the
  Go semver library disagree. (#4)
- A contradictory observation (absent with a version) is `UNKNOWN`
  with an explicit error, never a verdict. (#4)
- Requirement names must be plain identifiers; the report escapes
  control characters in every field so no input can forge a line.
  (#11, #13)
- `womm.yaml` output: existing files need `--force`; a symlink at the
  output path is refused and never followed; `verify` refuses to read
  a `womm.yaml` that is a symlink, too. `--force` replaces the file
  atomically (temp file + `rename`), so a path swapped to a FIFO,
  symlink or hard link mid-write can neither block WOMM nor redirect
  the write. (#7, #17, #19)
- A `womm.yaml` that declares `services` or `environment` (sections
  v0.1 cannot verify) is inconclusive (exit 3, explicit error), never
  a clean PASS. (#17)

- `.nvmrc` follows nvm's own reader: `#` comments anywhere, a file
  with no version line is an error (nvm rejects it too), a `node=`
  setting and duplicated settings are errors, other `KEY=value`
  settings are ignored. (#18)

### Known limitations

- Linux only. Only the Node.js ecosystem (`node`, `npm`, `pnpm`,
  `yarn`); only explicit declarations (no lockfile inference).
- Tools outside the system path allowlist (e.g. a version manager
  under `$HOME`) are not observed.
- `.nvmrc` aliases (`lts/*`, `22`, `node`) are unsupported selectors,
  reported as errors, not resolved.
- A probe descendant that leaves its process group (`setsid`) can
  keep WOMM waiting up to timeout + 2s before it is adopted and
  killed (Linux child subreaper); non-Linux builds have no subreaper
  and such a process would survive under init (v0.1 is Linux-only).
- `capture --force` overwrites; it never merges with or diffs against
  an existing `womm.yaml`.

[Unreleased]: https://github.com/0xCHANDA/womm/compare/ce93353...main
