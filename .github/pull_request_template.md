<!--
Keep this short and honest. The diff must contain one slice only
(docs/agent-workflow.md).
-->

## Summary

What this PR changes, in 2–5 lines.

## Scope

Only one slice. State which one (files/packages/commands touched), and
confirm it matches the assigned slice exactly.

## Architectural impact

- Frontier rule (`Detected ≠ Required`, `Observed ≠ Verified`): respected?
- Security invariants (L0 containment, L1 allowlist, sanitized probes):
  unchanged? If touched, justify explicitly.

## Tests

What is covered: new cases, edge cases, error/refusal paths, race.

## Validation commands

Paste the output of:

```sh
gofmt -l .
go vet ./...
go test -count=1 ./...
go test -race ./...
go build ./cmd/womm
```

## Security considerations

Anything affecting the L0/L1 boundaries, probe environment, timeouts,
output caps, or filesystem reads? If not, state "none".

## Explicitly out of scope

List what you deliberately did NOT do (roadmap neighbors you left
alone).
