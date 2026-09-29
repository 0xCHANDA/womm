# WOMM — Works On My Machine

> Find out why software works on one machine and fails on another.

WOMM (`womm`) is an open source CLI, written in Go, that discovers
the real requirements of a project, figures out the environment
needed to run it, and verifies whether another machine meets those
requirements.

**Status: early development.** The first vertical slice
(`womm capture`, `womm verify`, Linux + Node.js) is being built.

### v0.0.1 vertical slice progress

- [x] Core model / schema
- [x] L0 requirement detection (Node, package manager — engines/.nvmrc/packageManager, conflict-safe)
- [x] L1 machine inspection (`NodeInspector`: node/npm/pnpm/yarn, sanitized + bounded probes)
- [x] Requirement comparison (pure `compare` engine: exact/range/present, prerelease-vs-range refused explicitly)
- [x] Reporting (deterministic `[]core.Match` rendering); exit code 1 arrives with `verify`
- [x] `capture` (`womm capture [dir]` → deterministic `womm.yaml`)
- [x] `verify` (`womm verify [dir]` → report + exit code 0/1/2/3)
- [ ] final E2E

Merged:

- PR #1 — Foundation hardening
- PR #2 — Node + package manager L0 detection
- PR #3 — `NodeInspector` (L1 machine inspection)
- PR #4 — Requirement comparison (pure `compare` engine)
- PR #6 — Reporting (deterministic rendering of matches)
- PR #7 — `womm capture`
- PR #8 — `womm verify` (unreachable contract, exit codes)

## Build

```sh
go build ./cmd/womm
```

## Test

```sh
go test -count=1 ./...
go test -race ./...
```

Full validation contract and development docs: `docs/development.md`.
Architecture, roadmap and the agent workflow for coding sessions:
`docs/architecture.md`, `docs/roadmap.md`, `docs/agent-workflow.md`.

## Run (currently available)

```sh
womm --help
womm version
womm capture [project-dir]        # writes <project-dir>/womm.yaml
womm capture -o out.yaml --force  # custom output; overwrite a regular file
womm verify  [project-dir]        # checks this machine against womm.yaml
womm verify  -f spec.yaml         # verify a specific file
```

`capture` reads only explicit declarations (`package.json`
`engines.node` / `packageManager`, `.nvmrc`), never runs project code
and never inspects the machine. Conflicting or unsupported
declarations are errors (exit 3), not guesses. Example output:

```yaml
version: 1
requirements:
  - name: node
    constraint: 24.7.0
    evidence:
      - source: package.json
        field: engines.node
        value: '>=22 <25'
      - source: .nvmrc
        field: version
        value: v24.7.0
  - name: pnpm
    constraint: 10.15.1
    evidence:
      - source: package.json
        field: packageManager
        value: pnpm@10.15.1
```

`verify` probes only fixed `--version` invocations of `node`, `npm`,
`pnpm`, `yarn` resolved from `/usr/local/bin`, `/usr/bin`, `/bin`
(never the inherited `PATH`, never project code). Example:

```text
$ womm verify
FAIL        node required 24.7.0; observed 20.19.0
            observed version differs from the required version
            evidence: package.json → engines.node = ">=22 <25"
            evidence: .nvmrc → version = "v24.7.0"
PASS        pnpm required 10.15.1; observed 10.15.1
            observed version equals the required version
            evidence: package.json → packageManager = "pnpm@10.15.1"

2 requirements: 1 pass, 1 fail, 0 unknown, 0 unreachable
$ echo $?
1
```

Exit codes:

| Code | Meaning |
|---|---|
| 0 | every requirement PASS |
| 1 | at least one FAIL, nothing inconclusive |
| 2 | usage error |
| 3 | inconclusive: an UNKNOWN or UNREACHABLE result, a malformed `womm.yaml`, or an operational failure (3 wins over 1) |

`UNREACHABLE` means the tool is installed but could not be queried
(hung, crashed); `UNKNOWN` means WOMM refuses to decide (unparseable
version, a prerelease against a range, invalid constraint).

## Roadmap

- Node.js machine inspection (L1: `node`, `npm`, `pnpm`, `yarn`)
- `womm capture` / `womm verify` (Node.js first)
- `womm diff` / `womm explain`
- Python, Go, Docker, Git detectors
- PostgreSQL / Redis services
- machine snapshots, CI mode

Full design lives in the project specification. Philosophy:
**Don't reproduce everything. Find what actually matters.**

## License

[MIT](LICENSE)
