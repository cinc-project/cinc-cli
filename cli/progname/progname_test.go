package progname

import "testing"

func TestGetDefaultsWhenUnset(t *testing.T) {
	if got := Get(); got != Default {
		t.Errorf("Get() = %q before any Set, want %q", got, Default)
	}
}

func TestSetAndGet(t *testing.T) {
	t.Cleanup(func() { current.Store(nil) })

	Set("cinc-ng")
	if got := Get(); got != "cinc-ng" {
		t.Errorf("Get() = %q, want cinc-ng", got)
	}

	// An empty name is never allowed to leak into user-facing text.
	Set("")
	if got := Get(); got != Default {
		t.Errorf("Get() = %q after Set(\"\"), want %q", got, Default)
	}
}
