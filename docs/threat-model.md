# Threat model: who controls what WOMM executes

WOMM's guarantee is narrow and testable: **the project being
inspected never controls what WOMM executes.** This page states who
*does* control each input, so a reviewer can tell a violation from a
non-goal.

## Principals

| Principal | Controls | Trusted? |
|---|---|---|
| The project (repository content) | `package.json`, `.nvmrc`, `womm.yaml` inside the checkout, symlinks, file types, sizes, names | **No.** Every read is contained, bounded, and refused on anything unexpected. |
| The invoking user | argv (`--force`, `-o`, `-f`, project dir), the process environment, `$HOME`, the system directories they can write to | Yes, for what they typed. The environment is *sanitized where a project could reach into it*, not distrusted wholesale. |
| The machine (root, distro) | `/usr/local/bin`, `/usr/bin`, `/bin`, kernel | Yes. WOMM observes it; it cannot defend against a hostile root. |
| Other local users | `/tmp`, other home directories | **No.** Probes run from `/` and never consult `$TMPDIR`. |

## Environment variables: project-reachable vs user-owned

A project cannot set environment variables for the `womm` process
directly. It can reach the environment only through a launcher the
user chose to run it with:

| Launcher | What the project can inject | WOMM's answer |
|---|---|---|
| `npm run <script>` | `NODE_OPTIONS` via `.npmrc` `node-options`, any `npm_config_*`, `PATH` prefixed with `node_modules/.bin` | `NODE_OPTIONS` stripped; `npm_config_manage_package_manager_versions` forced; `PATH` replaced by the fixed allowlist |
| `direnv` / `.envrc` | anything — but only after the user explicitly `direnv allow`ed the file | user consent; see below |
| `make`, task runners | anything the Makefile exports | user ran the project's Makefile: user consent |
| Corepack shims | `COREPACK_*` behaviour | all forced to off/passive |
| yarn 1 `.yarnrc` `yarn-path`, pnpm `packageManager` | code execution during `--version` — via **cwd**, not env | cwd is `/`; `YARN_IGNORE_PATH=1`; pnpm self-management off |

### `LD_PRELOAD` / `LD_AUDIT` / `LD_LIBRARY_PATH`

Stripped. An independent review demonstrated a preloaded constructor
running inside the `node --version` probe with `LD_PRELOAD` set in the
inherited environment. Whoever sets these in a shell affects every
program, so the boundary is the user's own — but the guarantee WOMM
states is that *inherited* code-injection vectors are filtered, and
the loader is the same class as `NODE_OPTIONS`. Cost: a Node install
that only starts with a custom `LD_LIBRARY_PATH` (rare; Nix and
distro builds use rpath) is reported as `UNREACHABLE` with the
loader's error in the reason — honest, never wrong.

### `HOME`, `COREPACK_HOME`, `XDG_*`

Forced, not inherited. A Corepack shim executes whatever `pnpm.cjs`
sits in `$COREPACK_HOME` (default `$HOME/.cache/node/corepack`) with
no integrity check; a launcher that sets `HOME` or `COREPACK_HOME`
into the project turns a version probe into project code execution
(demonstrated by review). The probe therefore gets `HOME` from the
account's passwd entry and `COREPACK_HOME` derived from it;
`XDG_CACHE_HOME` / `XDG_CONFIG_HOME` are dropped. If the account has
no home — including a uid with no passwd entry at all (Docker
`--user`, OpenShift) — a non-existent path is used and cached shims
fail fast (`UNREACHABLE`). The lookup deliberately avoids
`os/user.Current()` and `user.LookupId`: without cgo (the release
binaries) both fall back to the inherited `$HOME` when `$USER` is set
and the uid has no passwd entry, which would hand the probe the very
value this section forbids; the static build reads `/etc/passwd`
itself.

### `NODE_V8_COVERAGE` / `NODE_REDIRECT_WARNINGS`

Stripped: they make node write files during `--version`, and a probe
never mutates the machine.

## What is out of scope

- A hostile **root** or a compromised system directory: WOMM
  observes those directories; it cannot verify them.
- A hostile **user** attacking themselves (`womm capture -o /etc/passwd`
  as root is a user choice).
- **Availability** beyond the documented bounds: a probe is limited
  to 5 s + 2 s wait and 4 KiB of output; a descendant that leaves the
  process group is adopted and killed (Linux); WOMM does not defend
  against fork bombs launched by a system binary the user installed.
