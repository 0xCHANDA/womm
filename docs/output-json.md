# `womm verify --format json` — output schema (version 1)

`womm verify --format json` prints **one JSON document** on stdout and
nothing else there; the human report is not printed. Everything that
is not part of the document — `warning:` lines, `error:` lines, usage
text — goes to stderr, exactly as in the default format. The process
exit code is identical in both formats (`docs/architecture.md`,
exit-code contract: 0 / 1 / 2 / 3, 3 beats 1).

The schema is public and versioned. It is written out explicitly in
`internal/report/json.go`: it is not a serialization of any internal
type, so refactoring the domain model cannot change it by accident.

## When a document is printed

- A document is printed whenever verification ran to a conclusion:
  exit 0, 1 or 3 with a result. `errors` carries the operational
  failures that produced no verdict for some requirement.
- **No document (stdout empty)** when nothing was verified: usage
  errors (exit 2, e.g. an invalid `--format` or `--tool-dir`), a
  missing, malformed or unsupported-version `womm.yaml`, and a run
  interrupted by a signal (no partial verdict, ever; exit 3). The exit
  code and stderr tell what happened. Consumers must check the exit
  code first.

## Shape

```json
{
  "schemaVersion": 1,
  "result": "inconclusive",
  "exitCode": 3,
  "requirements": [
    {
      "name": "node",
      "constraint": ">=22 <25",
      "status": "pass",
      "reason": "observed version satisfies the requirement",
      "observation": { "present": true, "version": "24.7.0", "path": "/home/u/.volta/bin/node" },
      "evidence": [
        { "source": "package.json", "field": "engines.node", "value": ">=22 <25" }
      ]
    }
  ],
  "errors": [
    { "kind": "unsupported_requirement", "message": "..." }
  ],
  "summary": { "total": 1, "pass": 1, "fail": 0, "unknown": 0, "unreachable": 0 }
}
```

Every field is always present, in this order. Lists are `[]`, never
`null`; absent values are `null`.

| Field | Meaning |
|---|---|
| `schemaVersion` | `1`. Incremented when a field is removed, renamed or changes meaning. Adding a field does not increment it: consumers must ignore fields they do not know. |
| `result` | `"pass"` (exit 0), `"fail"` (1) or `"inconclusive"` (3). Derived from the exit code, never different from it. |
| `exitCode` | The process exit code. |
| `requirements[]` | One entry per requirement taken through inspection, in the human report's order (name, then constraint). |
| `…name`, `…constraint` | As in `womm.yaml` (`constraint` is `present`, an exact version or an npm range). |
| `…status` | `"pass"`, `"fail"`, `"unknown"` or `"unreachable"`. `unknown`: WOMM refuses to decide. `unreachable`: the tool is present but could not be queried. |
| `…reason` | Deterministic human-readable explanation of the status. Not a stable code: do not parse it. |
| `…observation.present` | Whether the tool was found. |
| `…observation.version` | The observed version, or `null` when unknown or absent. |
| `…observation.path` | The executable that was started, or `null` when nothing ran (absent tool). Host state, shown for explanation; never part of the comparison and never written to `womm.yaml`. |
| `…evidence[]` | The project evidence behind the requirement, verbatim (`source`, `field`, `value`). |
| `errors[]` | Operational errors, in a fixed order: unsupported `services` entries (by name), the `environment` section, then per-requirement errors in requirement-name order. `kind` is a stable code; `message` is for humans. |
| `…kind` | `unsupported_requirement` (no inspector supports it, or a `womm.yaml` section WOMM cannot verify), `inspection_failed` (WOMM could not inspect; nothing known about the target), `undecidable` (the comparison refused to decide; the requirement also appears with status `unknown`), `other`. (`cancelled` exists internally but never appears: an interrupted run prints no document.) New kinds may be added. |
| `summary` | Counts per status; `total` equals the length of `requirements`. |

A requirement that was observed absent but with a version (a
contradictory observation) is rendered as it is, `present: false` with
a `version`, with status `unknown` — never smoothed over.

## Determinism and privacy

Two runs over the same project and machine produce byte-identical
output. There are no timestamps, host names, user names, identifiers,
fingerprints or environment values; the only host-derived value is the
executed `path`, which is what the local user asked to see.

## Encoding

UTF-8, indented by two spaces, one trailing newline. Control
characters (C0, DEL, the C1 controls such as CSI) and Unicode format
characters (bidi overrides, zero-width and tag characters) are written
as `\uXXXX` escapes, so the document is safe to `cat` to a terminal;
`<`, `>` and `&` are not escaped (constraints stay readable). Strings
are valid UTF-8: each byte that is not is replaced by one U+FFFD — the
only lossy step, and only for input that was not text in the first
place.

`womm verify --help` (also with `--format json`) prints the help text on
stdout and exits 0; no document is printed.
