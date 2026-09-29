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

### `LD_PRELOAD` / `LD_LIBRARY_PATH`

Passed through **on purpose**. Reasoning:

- No launcher above lets a project set them without the user first
  running project code (`direnv allow`, `make`), at which point the
  project already executes as the user and WOMM is not the boundary.
- `npm run` does not export project config as `LD_*`; `.npmrc` keys
  become `npm_config_<key>` only.
- Stripping them breaks legitimate machines: Node builds that rely on
  a non-default `libstdc++`/`libatomic` location, and Nix/Guix
  profiles where `LD_LIBRARY_PATH` is how a user-level toolchain is
  wired. A probe that cannot start would be reported as UNREACHABLE —
  a false "cannot query" on a machine that works.

If a future launcher is found that lets project content reach `LD_*`
without user consent, the variables move to `strippedEnvKeys` with a
regression test, as `NODE_OPTIONS` did.

## What is out of scope

- A hostile **root** or a compromised system directory: WOMM
  observes those directories; it cannot verify them.
- A hostile **user** attacking themselves (`womm capture -o /etc/passwd`
  as root is a user choice).
- **Availability** beyond the documented bounds: a probe is limited
  to 5 s + 2 s wait and 4 KiB of output; a descendant that leaves the
  process group is adopted and killed (Linux); WOMM does not defend
  against fork bombs launched by a system binary the user installed.
