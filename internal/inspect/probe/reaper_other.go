//go:build !linux

package probe

// Non-Linux builds have no subreaper; escaped descendants reparent to
// init. WOMM v0.1 supports Linux only — these stubs exist so the
// package still compiles elsewhere.
func becomeSubreaper()        {}
func killAdoptedDescendants() {}
