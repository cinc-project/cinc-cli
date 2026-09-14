// Package progname reports the name the running binary was invoked
// under, so user-facing text can name a command the reader actually has.
//
// The name is process-global because argv[0] is: a single binary cannot be
// two programs at once. Keeping it here rather than threading an option
// through every package means a message anywhere in the tree can name the
// binary without its package growing a ProgramName field, which is what
// made earlier attempts miss sites.
//
// Execute sets it once at startup. Everything else reads it.
package progname

import "sync/atomic"

// Default is the canonical name, used until Set says otherwise and
// whenever argv[0] is unusable.
const Default = "cinc"

var current atomic.Pointer[string]

// Set records the invoked program name. It is called once from Execute,
// before any command runs. Tests may call it with t.Cleanup to restore.
func Set(name string) {
	if name == "" {
		name = Default
	}
	current.Store(&name)
}

// Get returns the invoked program name, or Default if Set was never called
// (a library consumer, or a unit test that drives a command directly).
func Get() string {
	if p := current.Load(); p != nil {
		return *p
	}
	return Default
}
