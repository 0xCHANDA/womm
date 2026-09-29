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
- [ ] `verify`
- [ ] final E2E

Merged:

- PR #1 — Foundation hardening
- PR #2 — Node + package manager L0 detection
- PR #3 — `NodeInspector` (L1 machine inspection)
- PR #4 — Requirement comparison (pure `compare` engine)
- PR #6 — Reporting (deterministic rendering of matches)
- PR #7 — `womm capture`

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
