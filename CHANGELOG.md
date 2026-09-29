# Changelog

All notable changes to WOMM are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions
follow semver. No tag exists yet: the entry below is the release
candidate content for the first tag, to be cut by a human.

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
  fuzz targets for every parser and the renderer. (#9, #13)

### Security / hardening

- L0 containment: declared-source reads resolve symlinks and refuse to
  leave the project root; broken symlinks are errors; regular files
  only (a FIFO cannot block WOMM); reads bounded at 16 MiB
  (`womm.yaml`: 4 MiB). (#2, #11)
- L1 containment: tools resolve only from `/usr/local/bin`,
  `/usr/bin`, `/bin`; no shell; working directory `/` (no writable
  ancestors: yarn 1 `yarn-path` and pnpm self-version-management walk
  up from the cwd and would execute attacker-chosen code from `/tmp`
  or `$TMPDIR`); `NODE_OPTIONS` stripped; Corepack forced offline and
  passive; `YARN_IGNORE_PATH=1`;
  `npm_config_manage_package_manager_versions=false`; 5s timeout with
  process-group kill and a 2s wait backstop; output capped at 4 KiB;
  Ctrl-C kills a running probe and yields no verdict (no partial
  report, exit 3); WOMM is a child subreaper so a `setsid`-escaped
  probe descendant is adopted and killed too. (#3, #12, #15, #20, #21)
- Version ranges are gated on npm's grammar before evaluation
  (`>=22, <25`, `!=`, `=>`, `~>`, empty sets, `x.1.2`, `>x`, numbers
  beyond 2^64 are explicit errors, never a false PASS; prerelease
  identifiers containing `x` such as `>=20.0.0-next.1` are accepted).
  (#10, #16)
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
- A probe descendant that leaves its process group (`setsid`) is not
  reaped; WOMM still returns within timeout + 2s.
- `capture --force` overwrites; it never merges with or diffs against
  an existing `womm.yaml`.

[Unreleased]: https://github.com/0xCHANDA/womm/compare/ce93353...main
