# WOMM — Works On My Machine

> Find out why software works on one machine and fails on another.

WOMM (`womm`) is an open-source CLI, written in Go, that turns "it
works on my machine" into a report: it discovers what a project
**requires** from explicit declarations, observes what a machine
**actually has**, and compares the two with the evidence attached.

**Status: v0.1 vertical slice — Linux + Node.js.** `capture` and
`verify` work end to end for `node`, `npm`, `pnpm` and `yarn`. No
release is tagged yet; see [CHANGELOG.md](CHANGELOG.md).

## What WOMM is not

- **Not a provisioner.** It never installs, upgrades or changes
  anything on the machine.
- **Not a project-code executor.** It never runs a project's scripts
  or lifecycle hooks. The only processes it starts are fixed
  `<tool> --version` probes of system binaries.
- **Not a guessing tool.** Ambiguous, conflicting or unsupported
  declarations are explicit errors, never silently interpreted.

## Install

Requires Go 1.24.

```sh
# from a clone
git clone https://github.com/0xCHANDA/womm && cd womm
go build ./cmd/womm          # produces ./womm

# or straight from the module path (main branch; no tag yet)
go install github.com/0xCHANDA/womm/cmd/womm@main
```

Release builds set the version from the tag with
`-ldflags "-X github.com/0xCHANDA/womm/internal/cli.version=<version>"`;
source builds print `0.0.1-dev`.

## Usage

```sh
womm capture [project-dir]        # write <project-dir>/womm.yaml
womm capture -o out.yaml --force  # custom output; overwrite a regular file
womm verify  [project-dir]        # check this machine against womm.yaml
womm verify  -f spec.yaml         # verify a specific file
womm version
```

### `womm capture` — what the project requires

Reads only explicit declarations inside the project root:

| Source | Field | Becomes |
|---|---|---|
| `package.json` | `engines.node` | `node` range (npm range grammar) |
| `.nvmrc` | exact `x.y.z` / `vx.y.z` (nvm comment and `KEY=value` rules) | `node` exact version |
| `package.json` | `packageManager` | `npm` / `pnpm` / `yarn` exact version (Corepack `+sha1/sha224/sha512.<hex>` validated and stripped) |

`engines.node` and `.nvmrc` together must agree: the exact `.nvmrc`
version must satisfy the `engines` range, and the result is that exact
version with both pieces of evidence. If they cannot both be true,
capture aborts with an evidence conflict — no side is picked.

Explicit errors (exit 3, nothing written): malformed `package.json`,
conflicting declarations, `.nvmrc` selectors that are valid for nvm
but not verifiable (`lts/*`, `22`, `node`, `stable`), an `.nvmrc`
nvm itself rejects (no version line, `node=` setting), range syntax npm
does not accept (`>=22, <25`, `!=`), package managers other than
npm/pnpm/yarn, Corepack URLs, a declared source that is a symlink
escaping the project, a broken symlink, a FIFO, or a file over 16 MiB.

```sh
$ cat package.json
{
  "name": "demo",
  "engines": { "node": ">=22 <25" },
  "packageManager": "pnpm@10.15.1"
}
$ cat .nvmrc
v24.7.0
$ womm capture
wrote womm.yaml (2 requirements)
$ cat womm.yaml
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

The file is deterministic (requirements sorted by name, fixed key
order, evidence verbatim) and safe to commit. A project that declares
nothing produces `requirements: []`. `womm.yaml` is never overwritten
without `--force`, and a symlink at the output path is refused in
both modes.

### `womm verify` — what this machine has

Loads `womm.yaml`, probes each required tool, compares, prints one
block per requirement (sorted by name) and a summary. The line states
which executable answered (`at /usr/bin/node`) whenever one was run;
an absent tool has no path:

```text
$ womm verify
FAIL        node required 24.7.0; observed 20.20.2 at /usr/bin/node
            observed version differs from the required version
            evidence: package.json → engines.node = ">=22 <25"
            evidence: .nvmrc → version = "v24.7.0"
FAIL        pnpm required 10.15.1; observed absent
            required target is absent
            evidence: package.json → packageManager = "pnpm@10.15.1"

2 requirements: 0 pass, 2 fail, 0 unknown, 0 unreachable
$ echo $?
1
```

Statuses:

| Status | Meaning |
|---|---|
| `PASS` | the observation satisfies the requirement |
| `FAIL` | it does not (wrong version, or the tool is absent) |
| `UNKNOWN` | WOMM refuses to decide: version could not be parsed, a prerelease was observed against a range, or the constraint is invalid |
| `UNREACHABLE` | the tool is installed but could not be queried (hung past 5s, crashed, would not start) |

Exit codes:

| Code | Meaning |
|---|---|
| 0 | every requirement `PASS` |
| 1 | at least one `FAIL`, nothing inconclusive |
| 2 | usage error |
| 3 | inconclusive: any `UNKNOWN` or `UNREACHABLE`, a `womm.yaml` that does not load, or an operational failure — 3 takes precedence over 1 |

Operational failures (a requirement no inspector supports, a
`services` or `environment` section — not verified in v0.1 — a probe
that failed before the tool was even found) are printed on stderr as
`error: …`, never become a status, and make the run inconclusive. An
`UNKNOWN` caused by malformed input (invalid constraint, unparseable
observed version, prerelease against a range) keeps its status line
on stdout *and* echoes the detail as an `error: …` line on stderr.

#### Machine-readable output

`womm verify --format json` prints one JSON document on stdout instead
of the report (schema version 1, `docs/output-json.md`): requirements
with status, reason, observation (including the executed `path`) and
evidence, the operational errors, and a summary. The document is
deterministic (no timestamps, host names or identifiers); the exit code
and everything on stderr are identical to the default format. When
nothing was verified (usage error, missing or malformed `womm.yaml`,
interrupted run) stdout stays empty — check the exit code first.

### Constraint semantics

- `present` — the tool must exist; version ignored.
- exact version (`24.7.0`, `4.0.0-rc.1`) — equality, prerelease
  included.
- range (`>=22 <25`, `^20.10.0 || >=22`, `20.x`, `22 - 24`) — npm
  range grammar, evaluated for release versions only. A **prerelease
  observed against a range is `UNKNOWN`**, on purpose: npm's own
  engines check, node-semver's documented rule and the Go semver
  library give three different answers for the same pair, and WOMM
  does not pick one silently.

## How probes are contained

- Tools resolve only from the directories you name with `--tool-dir`
  (repeatable, consulted in the order given), then `/usr/local/bin`,
  `/usr/bin`, `/bin`, then the account's `~/.volta/bin`,
  `~/.asdf/shims`, `~/.local/share/mise/shims` and `~/.local/bin` —
  never the inherited `PATH`, never the project tree. Those four are
  taken from the user database, not `$HOME`, and are used only when
  they exist and are owned by you or root, not world-writable, and
  outside the project; an unsafe one is skipped with a `warning:`. A
  tool already installed system-wide always wins over them. nvm and
  fnm keep one directory per Node version: pass it,
  e.g. `womm verify --tool-dir ~/.nvm/versions/node/v24.7.0/bin`. A
  `--tool-dir` must be an absolute path (your shell expands `~`), must
  exist, and is refused (usage error, exit 2) when it is the project,
  inside it, under a `node_modules`, or world-writable; one owned by
  another user is accepted with a `warning:` on stderr. A tool that is
  a symlink into the project is refused, never run. The report shows
  which executable answered.
- Fixed `--version` argument, no shell, working directory `/` (never
  the project root, no writable ancestors for yarn/pnpm to walk up
  into).
- `NODE_OPTIONS` stripped; Corepack forced offline and passive; yarn
  `yarn-path` ignored; pnpm self-version-management disabled: a probe
  cannot download anything, run project-chosen code or edit a
  `package.json`.
- 5-second timeout with process-group kill, 2-second wait backstop,
  output capped at 4 KiB.

Full detail: [`docs/architecture.md`](docs/architecture.md); who
controls what: [`docs/threat-model.md`](docs/threat-model.md).

## Development

```sh
gofmt -l .                 # must print nothing
go vet ./...
go test -count=1 ./...
go test -race ./...
go build ./cmd/womm
```

Fuzz targets exist for every parser and the renderer
(`go test -fuzz=FuzzParse ./internal/schema/`, etc.). Contributor
docs: [`docs/development.md`](docs/development.md); design and
status: [`docs/architecture.md`](docs/architecture.md),
[`docs/roadmap.md`](docs/roadmap.md); agent sessions:
[`docs/agent-workflow.md`](docs/agent-workflow.md).

## Roadmap (after v0.1)

- `womm diff` / `womm explain`
- Python, Go, Docker, Git detectors; PostgreSQL / Redis services
- machine snapshots, CI mode

Philosophy: **don't reproduce everything — find what actually
matters.**

## License

[MIT](LICENSE)
