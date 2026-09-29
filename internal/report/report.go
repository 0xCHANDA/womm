// Package report renders comparison results for humans.
//
// It is presentation only: it never inspects the machine, never
// re-evaluates a requirement against an observation, and never
// decides process exit status. Every byte it writes is a function of
// the []core.Match it is given, in a fixed order, with fixed wording —
// two runs over equal inputs produce identical output.
package report

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/0xCHANDA/womm/internal/core"
)

// ErrUnknownStatus marks a core.MatchStatus this renderer does not
// know. Rendering must not invent a label for it: an unexpected status
// is a programming error upstream, surfaced instead of masked.
var ErrUnknownStatus = errors.New("unknown match status")

// labels are the fixed, uppercase presentation of each status.
var labels = map[core.MatchStatus]string{
	core.StatusPass:        "PASS",
	core.StatusFail:        "FAIL",
	core.StatusUnknown:     "UNKNOWN",
	core.StatusUnreachable: "UNREACHABLE",
}

// labelWidth is the widest label; status columns are padded to it.
const labelWidth = len("UNREACHABLE")

// Summary counts matches per status. It is derived purely from the
// statuses already decided by the compare engine; it decides nothing.
type Summary struct {
	Pass, Fail, Unknown, Unreachable int
}

// Total is the number of matches summarized.
func (s Summary) Total() int { return s.Pass + s.Fail + s.Unknown + s.Unreachable }

// Summarize counts the statuses in matches. An unknown status is an
// error, never silently dropped from the counts.
func Summarize(matches []core.Match) (Summary, error) {
	var s Summary
	for _, m := range matches {
		switch m.Status {
		case core.StatusPass:
			s.Pass++
		case core.StatusFail:
			s.Fail++
		case core.StatusUnknown:
			s.Unknown++
		case core.StatusUnreachable:
			s.Unreachable++
		default:
			return Summary{}, fmt.Errorf("%w: %q (requirement %q)", ErrUnknownStatus, string(m.Status), m.Requirement.Name)
		}
	}
	return s, nil
}

// Render writes the human-readable report for matches to w.
//
// Layout, one block per match, in deterministic order (see Sort):
//
//	STATUS       name   required <constraint>; observed <observation>
//	             <reason>
//	             evidence: <source> → <field> = <value>   (one per entry)
//
// followed by a blank line and one summary line. An empty input
// renders the summary line only. Nothing is written before the whole
// report has been validated: an unknown status fails before the first
// byte reaches w.
func Render(w io.Writer, matches []core.Match) error {
	summary, err := Summarize(matches)
	if err != nil {
		return err
	}
	ordered := Sort(matches)

	nameWidth := 0
	for _, m := range ordered {
		if l := len(m.Requirement.Name); l > nameWidth {
			nameWidth = l
		}
	}

	var b strings.Builder
	indent := strings.Repeat(" ", labelWidth+1)
	for _, m := range ordered {
		fmt.Fprintf(&b, "%-*s %-*s required %s; observed %s\n",
			labelWidth, labels[m.Status],
			nameWidth, m.Requirement.Name,
			m.Requirement.Constraint, describeObservation(m.Observation))
		if m.Reason != "" {
			fmt.Fprintf(&b, "%s%s\n", indent, m.Reason)
		}
		for _, ev := range m.Requirement.Evidence {
			fmt.Fprintf(&b, "%sevidence: %s → %s = %q\n", indent, ev.Source, ev.Field, ev.Value)
		}
	}
	if len(ordered) > 0 {
		b.WriteString("\n")
	}
	b.WriteString(summaryLine(summary))
	b.WriteString("\n")

	_, err = io.WriteString(w, b.String())
	return err
}

// Sort returns a copy of matches in presentation order: by requirement
// name, then constraint, then observation name; ties keep input order
// (stable). The input slice is never reordered.
func Sort(matches []core.Match) []core.Match {
	out := make([]core.Match, len(matches))
	copy(out, matches)
	sort.SliceStable(out, func(i, j int) bool {
		a, c := out[i], out[j]
		if a.Requirement.Name != c.Requirement.Name {
			return a.Requirement.Name < c.Requirement.Name
		}
		if a.Requirement.Constraint != c.Requirement.Constraint {
			return a.Requirement.Constraint < c.Requirement.Constraint
		}
		return a.Observation.Name < c.Observation.Name
	})
	return out
}

// describeObservation states what was observed, truthfully: absence,
// presence without a version, a version, or the contradictory
// absent-with-version case (rendered as such, never smoothed over).
func describeObservation(o core.Observation) string {
	switch {
	case !o.Present && o.Version != "":
		return fmt.Sprintf("absent (contradictory: version %s reported)", o.Version)
	case !o.Present:
		return "absent"
	case o.Version == "":
		return "present, version unknown"
	default:
		return o.Version
	}
}

func summaryLine(s Summary) string {
	noun := "requirements"
	if s.Total() == 1 {
		noun = "requirement"
	}
	return fmt.Sprintf("%d %s: %d pass, %d fail, %d unknown, %d unreachable",
		s.Total(), noun, s.Pass, s.Fail, s.Unknown, s.Unreachable)
}
