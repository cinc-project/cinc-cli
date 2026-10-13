package resolver

import (
	"errors"
	"testing"

	"github.com/cinc-project/cinc-cli/cli/policyfile/rubyeval"
)

// mkUniverse builds a solver universe from a compact description:
// name -> version -> (dep -> constraint).
func mkUniverse(t *testing.T, spec map[string]map[string]map[string]string) universe {
	t.Helper()
	u := universe{}
	for name, versions := range spec {
		for ver, deps := range versions {
			v, err := ParseVersion(ver)
			if err != nil {
				t.Fatalf("bad version %q: %v", ver, err)
			}
			c := candidate{version: v}
			for dn, dc := range deps {
				c.deps = append(c.deps, rubyeval.Dependency{Name: dn, Constraint: dc})
			}
			u[name] = append(u[name], c)
		}
	}
	return u
}

func mkDemand(t *testing.T, name, constraint string) demand {
	t.Helper()
	c, err := ParseConstraint(constraint)
	if err != nil {
		t.Fatalf("bad constraint %q: %v", constraint, err)
	}
	return demand{name: name, constraint: c}
}

func TestSolvePrefersHighestVersion(t *testing.T) {
	u := mkUniverse(t, map[string]map[string]map[string]string{
		"foo": {"1.0.0": nil, "2.0.0": nil, "1.5.0": nil},
	})
	sol, err := solve(u, []demand{mkDemand(t, "foo", ">= 0.0.0")})
	if err != nil {
		t.Fatal(err)
	}
	if got := sol["foo"].String(); got != "2.0.0" {
		t.Errorf("foo = %s, want 2.0.0", got)
	}
}

func TestSolveRespectsPessimisticConstraint(t *testing.T) {
	u := mkUniverse(t, map[string]map[string]map[string]string{
		"foo": {"1.0.0": nil, "1.5.0": nil, "2.0.0": nil},
	})
	sol, err := solve(u, []demand{mkDemand(t, "foo", "~> 1.0")})
	if err != nil {
		t.Fatal(err)
	}
	if got := sol["foo"].String(); got != "1.5.0" {
		t.Errorf("foo = %s, want 1.5.0 (highest in the 1.x line)", got)
	}
}

func TestSolveTransitivePicksHighestSatisfying(t *testing.T) {
	u := mkUniverse(t, map[string]map[string]map[string]string{
		"app": {"1.0.0": {"lib": "~> 1.0"}},
		"lib": {"1.0.0": nil, "1.5.0": nil, "2.0.0": nil},
	})
	sol, err := solve(u, []demand{mkDemand(t, "app", ">= 0.0.0")})
	if err != nil {
		t.Fatal(err)
	}
	if got := sol["app"].String(); got != "1.0.0" {
		t.Errorf("app = %s, want 1.0.0", got)
	}
	if got := sol["lib"].String(); got != "1.5.0" {
		t.Errorf("lib = %s, want 1.5.0 (highest satisfying ~> 1.0)", got)
	}
}

func TestSolveBacktracks(t *testing.T) {
	// app 2.0.0 needs lib ~> 2.0, but only lib 1.0.0 exists, so the solver must
	// fall back to app 1.0.0 (which needs lib ~> 1.0).
	u := mkUniverse(t, map[string]map[string]map[string]string{
		"app": {"2.0.0": {"lib": "~> 2.0"}, "1.0.0": {"lib": "~> 1.0"}},
		"lib": {"1.0.0": nil},
	})
	sol, err := solve(u, []demand{mkDemand(t, "app", ">= 0.0.0")})
	if err != nil {
		t.Fatal(err)
	}
	if got := sol["app"].String(); got != "1.0.0" {
		t.Errorf("app = %s, want 1.0.0 after backtracking", got)
	}
	if got := sol["lib"].String(); got != "1.0.0" {
		t.Errorf("lib = %s, want 1.0.0", got)
	}
}

func TestSolveUnsatisfiable(t *testing.T) {
	u := mkUniverse(t, map[string]map[string]map[string]string{
		"app": {"1.0.0": {"lib": "~> 3.0"}},
		"lib": {"1.0.0": nil},
	})
	_, err := solve(u, []demand{mkDemand(t, "app", ">= 0.0.0")})
	if err == nil {
		t.Fatal("expected NoSolutionError, got nil")
	}
	if _, ok := errors.AsType[*NoSolutionError](err); !ok {
		t.Fatalf("expected *NoSolutionError, got %T: %v", err, err)
	}
}

func TestSolveMissingCookbook(t *testing.T) {
	u := mkUniverse(t, map[string]map[string]map[string]string{
		"app": {"1.0.0": {"absent": ">= 0.0.0"}},
	})
	_, err := solve(u, []demand{mkDemand(t, "app", ">= 0.0.0")})
	if err == nil {
		t.Fatal("expected error for missing cookbook")
	}
}
