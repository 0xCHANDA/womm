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
- [~] L1 machine inspection — implemented on `feat/node-inspector`; pending review/merge
- [ ] Requirement comparison
- [ ] Reporting / exit codes
- [ ] `capture`
- [ ] `verify`
- [ ] final E2E

Merged:

- PR #1 — Foundation hardening
- PR #2 — Node + package manager L0 detection

Current:

- PR #3 — `NodeInspector` (L1 machine inspection: `node`, `npm`, `pnpm`,
  `yarn`), implemented and validated on `feat/node-inspector`; pending
  review/merge — not yet on `main`

Next after merge:

- PR #4 — Requirement comparison

## Build

```sh
go build ./cmd/womm
```

## Test

```sh
go test ./...
```

## Run (currently available)

```sh
womm --help
womm version
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
