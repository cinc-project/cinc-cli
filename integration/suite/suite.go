// Package suite holds the integration tests shared by every server target.
// Each target package (cincserverng for CI, cincservererlang for the CINC
// Server Erlang stack in AWS) builds a Target and calls Run, so a case written
// here runs the real cinc binary unchanged against both servers. A difference
// between them shows up as a named entry in Target.Gaps, never as a case that
// exists for one server only.
package suite

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
)

// Target is one server the suite runs against.
type Target struct {
	// Name identifies the server in skip messages, e.g. "cinc-server-ng".
	Name string
	// ServerURL is the server's base URL, without /organizations/<org>.
	ServerURL string
	// Org is the organization the cases create objects in.
	Org string
	// OtherOrg is a second organization Admin belongs to, for cases that
	// switch or compare organizations.
	OtherOrg string
	// Admin is the user the CLI signs as. It is an admin of Org and OtherOrg
	// and may manage users server-wide.
	Admin string
	// KeyPath is Admin's private key.
	KeyPath string
	// CACertPath is a CA certificate the CLI must trust to reach ServerURL,
	// or "" for a plain-HTTP server.
	CACertPath string
	// Gaps maps a case name (e.g. "nodes/lifecycle") to the reason it is
	// skipped on this target: an upstream issue URL, or the behaviour
	// observed. Every key must name an existing case and carry a reason.
	Gaps map[string]string
}

// testCase is one shared test. name is "<family>/<case>" and becomes the
// subtest name, so `go test -run 'Test.*/nodes/'` selects a family.
type testCase struct {
	name string
	// covers lists the leaf commands the case exercises, as space-joined
	// paths without the root ("node run-list add"). The coverage guard
	// requires every shipped leaf to appear in some case's covers, or in
	// exempt or a family's pending list.
	covers []string
	run    func(t *testing.T, tgt Target, c *cli)
}

// family is one file's worth of cases. pending lists the leaf commands the
// family owns whose cases have not been ported from test/acceptance yet; the
// port empties it.
type family struct {
	cases   []testCase
	pending []string
}

// families returns every family in a fixed order. Each family lives in its
// own file, so porting one never touches another's.
func families() []family {
	return []family{
		nodeFamily,
		searchFamily,
		roleFamily,
		environmentFamily,
		clientFamily,
		userFamily,
		keyFamily,
		groupFamily,
		orgFamily,
		aclFamily,
		databagFamily,
		cookbookFamily,
		policyFamily,
		cliFamily,
	}
}

// allCases flattens families into one list.
func allCases() []testCase {
	var out []testCase
	for _, f := range families() {
		out = append(out, f.cases...)
	}
	return out
}

// Run runs every shared case against tgt as a parallel subtest, skipping the
// ones tgt.Gaps lists.
func Run(t *testing.T, tgt Target) {
	t.Helper()
	cases := allCases()
	names := make([]string, len(cases))
	for i, tc := range cases {
		names[i] = tc.name
	}
	if err := checkCases(names); err != nil {
		t.Fatal(err)
	}
	if err := checkGaps(names, tgt.Gaps); err != nil {
		t.Fatalf("%s: %v", tgt.Name, err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if reason, ok := tgt.Gaps[tc.name]; ok {
				t.Skipf("known gap on %s: %s", tgt.Name, reason)
			}
			t.Parallel()
			tc.run(t, tgt, newCLI(t, tgt))
		})
	}
}

// checkCases rejects duplicate case names, which would make a gap ambiguous.
func checkCases(names []string) error {
	seen := map[string]bool{}
	var errs []error
	for _, n := range names {
		if seen[n] {
			errs = append(errs, fmt.Errorf("case %q is defined twice", n))
		}
		seen[n] = true
	}
	return errors.Join(errs...)
}

// checkGaps rejects a gap that names no case (a typo or a renamed test would
// otherwise skip nothing and hide the mistake) or that gives no reason.
func checkGaps(names []string, gaps map[string]string) error {
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(gaps)) {
		if !slices.Contains(names, name) {
			errs = append(errs, fmt.Errorf("gap %q names no test", name))
		}
		if strings.TrimSpace(gaps[name]) == "" {
			errs = append(errs, fmt.Errorf("gap %q has no reason", name))
		}
	}
	return errors.Join(errs...)
}
