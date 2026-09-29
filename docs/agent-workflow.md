# Agent workflow (Claude Code / Claude Cloud)

This repository is developed by short, controlled agent sessions. The
discipline is deliberately rigid:

```text
one task → one branch → one coherent scope → one PR → one validation report
```

## Session checklist

1. **Read before writing:** `CLAUDE.md` (root), the mission you were
   given, and — if your task touches them — `docs/architecture.md`,
   `docs/roadmap.md`, `docs/development.md`.
2. **One task.** If the mission contains a second idea, stop; it is
   not part of this session.
3. **Branch from `origin/main`**, named `feat/<slice>`, `fix/<thing>`,
   `docs/<thing>`, `chore/<thing>`. Never branch off another agent's
   open PR branch.
4. **Commit identity is configured.** Never run `git config` to change
   `user.name` / `user.email`; never add `Co-Authored-By` or any
   "Generated with" trailer. Commits are attributed to the repository
   owner and nobody else.
5. **Validate before every PR:**
   ```sh
   gofmt -l .           # empty
   go vet ./...
   go test -count=1 ./...
   go test -race ./...
   go build ./cmd/womm
   ```
6. **PR:** use `.github/pull_request_template.md` unchanged. Target
   `main`. Keep the PR diff inside the assigned scope.
7. **Final message** must include the validation report: each command
   and its outcome, plus what is explicitly out of scope / left undone.

## Rules for multiple parallel sessions

- Sessions work on **disjoint slices** — never the same package unless
  the mission says so. `docs/roadmap.md` is the allocation surface.
- **Do not push to branches you don't own.** Check `gh pr list` before
  choosing a branch name; don't reuse dropped branches.
- Rebase onto `origin/main` (`git fetch origin && git rebase
  origin/main`) only when your branch would not apply cleanly; no
  force-push, no rewrites of `main`.
- If a second task arises mid-session, stop, summarize the unfinished
  state, and end the session. Humans sequence the next mission.
- Long-running statuses are found in `docs/roadmap.md` and the open PR
  list — trust the PR list over stale README/docs lines and fix the
  stale line in your PR.

## Attribution and authorship verification

Before merging ANY remote work, verify who really wrote it:

```sh
scripts/check-commit-attribution.sh            # compares to origin/main
scripts/check-commit-attribution.sh origin/feat/x   # custom base ref
```

It examines every commit in `<base>..HEAD` for:

- author/committer identities that do not match the repository owner
  (`0xCHANDA` and its configured email from local git config);
- forbidden trailers (`Co-Authored-By: Claude`, `noreply@anthropic.com`,
  "Generated with/by …").

Any such commit must not be merged. Note two benign patterns: merge
commits authored as `Santiago Hernández. <…+0xCHANDA@users.noreply.github>`
with committer `GitHub` come from the GitHub UI (web editor / merge
button) and are legitimate; the script whitelists that email.

**known historical contamination:** two commits already on `main`
(`27586b5`, `559906f`, both part of PR #3) contain
`Co-Authored-By: Claude` + `Claude-Session` trailers. They are not
rewritten (no history rewriting, ever), but they are why this check
exists: a run over `origin/main~1..origin/main` intentionally reports
them. Any NEW work must pass clean; if a remote PR shows trailers, ask
for the branch to be redone before merging.

Manual equivalent:

```sh
git log --format='%h | author=%an <%ae> | committer=%cn <%ce>' origin/main..HEAD
git log origin/main..HEAD --format=%B | grep -iE 'co-authored-by|generated|anthropic|claude'
```

## Exit-code contract (for anything touching the CLI)

| Code | Meaning | Status |
|---|---|---|
| 0 | success | implemented |
| 1 | semantic FAIL (environment incompatibility) | **reserved, not produced yet** — arrives with reporting/verify |
| 2 | usage error (cobra flag/args) | implemented (`exitCodeFor`) |
| 3 | execution/configuration error | implemented (`exitCodeFor`) |
