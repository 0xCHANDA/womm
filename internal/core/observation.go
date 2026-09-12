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
}
