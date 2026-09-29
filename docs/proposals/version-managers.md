# Proposal: observing version-managed Node installations

Status: **proposal, not implemented**. Decision required from the
owner (see "Recommendation"). Nothing in this document changes the
v0.1 behavior.

## Problem

`womm verify` resolves `node`, `npm`, `pnpm` and `yarn` only from
`/usr/local/bin`, `/usr/bin` and `/bin`. Most developer machines run
Node from a version manager instead:

| Manager | Where the binary lives | How the shell finds it |
|---|---|---|
| nvm | `~/.nvm/versions/node/v<ver>/bin/node` | `PATH` prepended by `nvm.sh` per shell |
| fnm | `~/.local/share/fnm/node-versions/v<ver>/installation/bin/node` (multishell symlink in `$FNM_MULTISHELL_PATH`) | `PATH` per shell |
| Volta | `~/.volta/bin/node` (shim → `~/.volta/tools/image/node/<ver>/bin/node`) | `PATH` once |
| asdf | `~/.asdf/shims/node` (shim) → `~/.asdf/installs/nodejs/<ver>/bin/node` | `PATH` once |
| mise | `~/.local/share/mise/installs/node/<ver>/bin/node` (or shims) | `PATH` per shell / shims |
| Homebrew (Linux) | `/home/linuxbrew/.linuxbrew/bin/node` | `PATH` |
| npm/pnpm global | `~/.npm-global/bin`, `~/.local/share/pnpm` | `PATH` |

On such a machine WOMM reports the tool as **absent** (or observes an
older system Node that the developer never uses). The report is
truthful about what WOMM looked at, but not about what the project
would run with. This is the largest usability gap of v0.1.

## Invariant that must survive

A project must never be able to place an executable earlier in
WOMM's resolution order and have WOMM execute it. Concretely:

1. never execute from the project root, `node_modules/.bin`, or any
   path derived from project content;
2. never trust the inherited `PATH` as such: a project's task runner,
   `direnv`, `.envrc`, `.nvmrc`-driven auto-switchers or an `npm run`
   wrapper can all prepend directories;
3. resolution order must be deterministic and explainable;
4. what was executed must be reported (evidence), so a surprising
   verdict can be traced to a binary.

## Options

### A. Widen the allowlist with well-known user directories

Add fixed, `$HOME`-relative directories in a fixed order after the
system dirs (e.g. `~/.volta/bin`, `~/.asdf/shims`, `~/.local/bin`).

- Security: `$HOME` is user-writable, not project-writable, so the
  project-code invariant holds — unless the project *is* under one of
  those directories (a checkout in `~/.local/bin/myproj` would not
  shadow anything: only the exact tool name at the exact directory is
  resolved). Shims (Volta, asdf) are wrappers that read *their own*
  config; asdf reads `.tool-versions` walking up from **cwd**, which
  is `/` for probes, so it resolves the global version only. Volta
  reads the nearest `package.json` `volta` key from cwd — again `/`.
- Usability: solves Volta/asdf/mise-shims immediately. Does **not**
  solve nvm/fnm (per-version directories, no stable shim path; the
  "current" one is chosen by the shell, invisible to WOMM).
- Effort: small. Deterministic. No new configuration.

### B. Resolve the *shell's* choice, then prove it is safe

Read the inherited `PATH`, resolve the tool, then accept the result
only if the resolved absolute path (after `EvalSymlinks`) is
**outside the project root** and inside `$HOME` or the system dirs,
and its directory is not world-writable.

- Security: the inherited `PATH` is *consulted* but never *trusted*:
  every candidate is checked against the project root and a
  containment set. Residual risk: a project-controlled process that
  runs `womm` (an `npm` script, `direnv`) can prepend a directory
  under `$HOME` it also controls (e.g. a cache dir it writes to).
  Mitigation: refuse directories the project can write to is not
  decidable; refuse directories *newer than the project checkout*
  is a heuristic; the honest answer is that B trusts "anything under
  `$HOME` that is not the project", which is weaker than A.
- Usability: solves everything, including nvm/fnm, because the shell
  already picked the version.
- Effort: medium; needs careful tests and an evidence line.

### C. Explicit user allowlist (opt-in configuration)

`womm verify --tool-dir <dir>` (repeatable) and/or a user-level
config file (`~/.config/womm/config.yaml`, never the project) listing
extra directories. Nothing implicit.

- Security: the user names the directories; a project cannot. The
  config file lives outside the project; the flag is typed by the
  user. Strongest option.
- Usability: requires the user to know their manager's layout
  (`--tool-dir ~/.nvm/versions/node/v24.7.0/bin`). Acceptable for CI,
  tedious for developers; can be paired with A.
- Effort: small.

### D. Version-manager awareness

Teach WOMM each manager's layout: read `~/.nvm/alias/default`,
`fnm`'s current, Volta's `platform.json`, asdf's `.tool-versions`
**in `$HOME` only** (never the project's), and resolve the version
the manager would pick *globally*.

- Security: reads user config only; no execution beyond `--version`.
- Usability: solves nvm/fnm properly, but "the version the manager
  would pick for this project" depends on project files
  (`.nvmrc`, `.tool-versions`) — which WOMM must not act on for
  resolution, only for requirements. So D resolves the *default*
  version, which may not be what the developer runs in this project.
- Effort: large, per-manager maintenance, fragile across manager
  versions.

## Comparison

| | Project-code invariant | Deterministic | nvm/fnm | Volta/asdf/mise | Effort |
|---|---|---|---|---|---|
| A | holds | yes | no | yes | small |
| B | holds with residual risk | yes (given env) | yes | yes | medium |
| C | holds (strongest) | yes | yes (manual) | yes (manual) | small |
| D | holds | yes | default only | yes | large |

## Recommendation

**C + A, in that order, in one minor release; not B.**

1. **C first**: `--tool-dir` (repeatable) plus `WOMM_TOOL_DIRS`
   (colon-separated) — both user-controlled, both refused if any
   entry resolves inside the project root or is relative. Directories
   are appended *after* the system allowlist; resolution order is
   printed in evidence. This unblocks CI and power users today with
   zero new implicit trust.
2. **A second**: a fixed, documented list of `$HOME`-relative shim
   directories (`~/.volta/bin`, `~/.asdf/shims`, `~/.local/share/mise/shims`,
   `~/.local/bin`) appended after C's entries. Each is skipped unless
   it exists and is owned by the current user and not world-writable.
3. **Evidence**: every observation records the absolute path executed
   (`observed 24.7.0 at /home/u/.volta/bin/node`). This needs one
   field on `core.Observation` (`Path string`) — ecosystem-agnostic,
   so allowed by the core contract — and one line in the report.
4. **Not B**: consulting the inherited `PATH` re-opens exactly the
   attack the allowlist was built to close, with a mitigation that is
   heuristic at best.

Migration impact: none for existing users (system dirs stay first);
`womm.yaml` unchanged; report gains one `at <path>` suffix per line
(golden tests updated). Exit-code contract unchanged.

## Attack surfaces to test before shipping

- `--tool-dir ./node_modules/.bin` and any path under the project →
  refused (usage error).
- A shim directory containing a symlink into the project → refused
  after `EvalSymlinks` (resolved path inside project root).
- World-writable shim directory → skipped, reported in evidence.
- Volta/asdf shims run from cwd `/` → global version only; document.
- Corepack-managed shims under `~/.cache/node/corepack` are never
  added (they are not on any list).
