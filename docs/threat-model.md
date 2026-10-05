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

**The probe's environment is an allowlist** (`LANG`, `LANGUAGE`, `LC_*`
inherited; everything else forced). The per-variable rows below
describe why each *specific* vector mattered; the allowlist is what
makes the list of vectors unnecessary to keep complete. It was
introduced when version-manager shims became executable: Volta, mise,
asdf, nodenv and fnm launchers choose which binary to run from
`VOLTA_HOME`, `MISE_*`, `ASDF_*`, `XDG_DATA_HOME`, ..., and a launcher
that exports one of them pointing into the project would otherwise get
project code executed behind a legitimate-looking `at <shim>` path
(reproduced by a security review with the real Volta and mise).

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

## Tool directories (`--tool-dir`)

`--tool-dir` is user-controlled (the invoking user types it) and is
consulted before the system directories. The project cannot configure
it: it is not read from `womm.yaml` or any project file, and there is
deliberately no environment-variable form, because launchers can set
environment variables the same way they can prepend to `PATH`.

| Input | WOMM's answer |
|---|---|
| relative path, `~`, empty | usage error (exit 2) |
| the project, anything inside it, or the directory of `-f` | usage error: project-controlled |
| a path through `node_modules` | usage error |
| a symlink (or `..`) that lands inside the project | usage error, judged on the lexical *and* the canonical path |
| world-writable directory | usage error: any local user could plant a binary |
| a name containing `:` or a control character | usage error: `/x/a:/x/evil` would put `/x/evil` on the probe `PATH`; also keeps hostile names out of terminals (every path in a message is quoted) |
| an entry named `node` that is not an executable regular file (mode 644, a directory, a FIFO) | operational error (exit 3), never a silent step to the next directory; system directories keep the v0.1 "keep looking" |
| directory owned by another user | accepted, `warning:` on stderr (user's call) |
| a binary there that is a symlink resolving into the project | operational error (exit 3); not run, not stepped over |
| a dangling symlink in a user directory | operational error, never "absent" |
| inherited `PATH` | never consulted |

### The implicit shim directories

`~/.volta/bin`, `~/.asdf/shims`, `~/.local/share/mise/shims` and
`~/.local/bin` under the account's home are searched after the system
directories. The home comes from the user database, never from `$HOME`
(a launcher can point `$HOME` into the project: with a hostile `$HOME`
the project's own `.volta/bin` is not searched). Nobody typed these
directories, so the rules are stricter than for `--tool-dir`: a
directory is used only when it is owned by the invoking user or root,
not world-writable, and outside the project (and `node_modules`);
anything else is skipped with a `warning:` on stderr. Running `womm`
from the home directory itself makes every shim directory
project-controlled, so all are skipped. Candidates are
containment-checked like any other.

The probe's own `PATH` gets the executable's directory first (see
`docs/architecture.md`); that directory has passed the checks above.
Residual risk: the user (or root) names a directory another user can
replace binaries in; WOMM warns about foreign ownership and refuses
world-writable directories but cannot judge every ancestor.

Known limits of the project-root rule: a project root that is an
ancestor of a system directory (`womm verify /`, or a `womm.yaml` in
`/`) makes every system tool "inside the project", so the run is
inconclusive (exit 3) — fail-closed, unlike v0.1 which verified it. A
`womm.yaml` in `$HOME` likewise disables the account's shim directories
(each skipped with a warning) and refuses `--tool-dir` under it. Only
the leaf of a tool directory is checked for ownership and
world-writability; group-writable directories and writable ancestors
are accepted.

## What is out of scope

- A hostile **root** or a compromised system directory: WOMM
  observes those directories; it cannot verify them.
- A hostile **user** attacking themselves (`womm capture -o /etc/passwd`
  as root is a user choice).
- **Availability** beyond the documented bounds: a probe is limited
  to 5 s + 2 s wait and 4 KiB of output; a descendant that leaves the
  process group is adopted and killed (Linux); WOMM does not defend
  against fork bombs launched by a system binary the user installed.
