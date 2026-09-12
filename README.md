# WOMM — Works On My Machine

> Find out why software works on one machine and fails on another.

WOMM (`womm`) is an open source CLI, written in Go, that discovers
the real requirements of a project, figures out the environment
needed to run it, and verifies whether another machine meets those
requirements.

**Status: early development.** The first vertical slice
(`womm capture`, `womm verify`, Linux + Node.js) is being built.

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

- `womm capture` / `womm verify` (Node.js first)
- `womm diff` / `womm explain`
- Python, Go, Docker, Git detectors
- PostgreSQL / Redis services
- machine snapshots, CI mode

Full design lives in the project specification. Philosophy:
**Don't reproduce everything. Find what actually matters.**

## License

[MIT](LICENSE)
