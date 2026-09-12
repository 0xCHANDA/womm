package core

// MatchStatus is the outcome of comparing one Requirement against one
// Observation.
type MatchStatus string

const (
	// StatusPass: the observation satisfies the constraint.
	StatusPass MatchStatus = "pass"
	// StatusFail: the observation does not satisfy the constraint.
	StatusFail MatchStatus = "fail"
	// StatusUnreachable: the target is present but unqueryable, or a
	// service probe got no answer. Distinguishable from fail.
	StatusUnreachable MatchStatus = "unreachable"
	// StatusUnknown: insufficient information (parse failure, unknown
	// version). Never guessed.
	StatusUnknown MatchStatus = "unknown"
)

// Match pairs a requirement with its observation and the resulting
// status. It is the unit of `verify`, `diff` and `explain`.
type Match struct {
	Requirement Requirement
	Observation Observation
	Status      MatchStatus
	// Human-readable, deterministic reason. Terminology of the user's
	// problem, not internal error text.
	Reason string
}
