package core

// Observation is what the Inspector found in the current machine for
// one requirement.
type Observation struct {
	// Requirement name this observation refers to.
	Name string
	// Whether the binary/service is present at all.
	Present bool
	// Raw discovered version, if any ("24.7.0"). Empty when unknown
	// or not applicable.
	Version string
	// Path is the absolute path of the executable that was started to
	// produce this observation — the evidence of *which* binary the
	// version came from ("24.7.0 at /home/u/.volta/bin/node"). Empty
	// when nothing was executed: the target is absent, the name was
	// refused, or the observation does not come from an executable.
	// It is host state, not a portable requirement: it is shown to the
	// local user and never written to womm.yaml, and it never takes
	// part in comparison.
	Path string
}
