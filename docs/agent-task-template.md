# Agent task template

Copy this structure for every future agent session. The job of the
template is to make each mission a closed box: the agent implements the
assigned slice and does not improvise the roadmap. Fill in every
section that applies; delete nothing else.

---

# Mission

One sentence: what must exist when this session ends, and why it
matters. (Example: "Implement `internal/reporting` that renders
`[]core.Match` deterministically; downstream consumer of `compare`,
fallout of roadmap item 2.")

# Allowed scope

- Packages/files the session may create or modify.
- Commands/features this slice adds, exactly.

# Explicitly out of scope

List neighboring tempting work so the agent never "helps": touching
other PR branches, refactors, new ecosystems, version bumps, CI
changes unless assigned, dependency additions.

# Required repository reading

- `CLAUDE.md` (always)
- `docs/architecture.md` (always)
- `docs/roadmap.md` (always)
- Package docs for the target area, and any open PR that interacts
  with this slice (`gh pr list`).

# Architectural constraints

- Frontier rule (`Detected ≠ Required`, `Observed ≠ Verified`).
- Sentinel errors, `errors.Is`; no swallowed errors.
- Strict semver (`semver.StrictNewVersion`); no silent coercion.
- L0/L1 security invariants as-is; report any design that weakens them.
- No core type may gain ecosystem-specific detail.

# Required tests

- New behavior + edge/error paths, table-driven where natural.
- Refusals tested explicitly (unsupported input must not execute).
- `-race` for anything touching exec/goroutines.

# Required validation

```sh
gofmt -l .
go vet ./...
go test -count=1 ./...
go test -race ./...
go build ./cmd/womm
```

All must pass; paste outputs into the final report.

# Deliverables

- Code + tests, this single slice.
- `docs/roadmap.md` status updated to reflect reality.
- `README.md` public-behavior section updated if user-facing.

# Git / commit requirements

- New branch from `origin/main`: `<type>/<slice>`.
- Conventional Commits; small, coherent commits.
- Identity untouched (`0xCHANDA`); no attribution trailers.
- One PR, `.github/pull_request_template.md` filled.
- Run `scripts/check-commit-attribution.sh` before requesting merge.

# Final report

- Validation report (command → result).
- What was NOT done / explicitly left out.
- Any divergence between the mission as written and the real state of
  the repository (drift is information: report it, don't fix it here).
