package resolver

import (
	"fmt"
	"maps"
	"sort"
	"strings"

	"github.com/cinc-project/cinc-cli/cli/policyfile/rubyeval"
)

// candidate is one available version of a cookbook in the universe, with the
// dependency constraints that version declares.
type candidate struct {
	version Version
	deps    []rubyeval.Dependency
}

// universe is the merged dependency graph the solver resolves over: for each
// cookbook name, the available versions (each with its own dependencies). It is
// chef's artifacts_graph.
type universe map[string][]candidate

// demand is a top-level requirement: a cookbook name and a constraint it must
// satisfy. These are chef's graph_demands.
type demand struct {
	name       string
	constraint Constraint
}

// NoSolutionError reports that the demands and the universe cannot be satisfied
// together. It names the cookbook that could not be resolved and the
// constraints that conflicted, matching chef's failure mode (a NoSolutionError
// listing the unsatisfiable requirements) even though the wording differs.
type NoSolutionError struct {
	Cookbook    string
	Constraints []string
	Detail      string
}

func (e *NoSolutionError) Error() string {
	if e.Detail != "" {
		return e.Detail
	}
	return fmt.Sprintf("no version of cookbook %q satisfies all constraints: %s",
		e.Cookbook, strings.Join(e.Constraints, ", "))
}

// solve resolves demands against the universe, returning the chosen version of
// every cookbook required (transitively) to satisfy them. Like chef's Solve
// (Molinillo), it prefers the highest version that satisfies all accumulated
// constraints and backtracks when a choice leads to a dead end, so the result
// is deterministic and matches chef's selection for the same inputs.
func solve(u universe, demands []demand) (map[string]Version, error) {
	// Accumulated constraints per cookbook, seeded with the demands. The
	// "source" string is carried only for error reporting.
	constraints := map[string][]constraintWithSource{}
	for _, d := range demands {
		constraints[d.name] = append(constraints[d.name], constraintWithSource{d.constraint, "Policyfile"})
	}

	s := &solverState{u: u}
	assignment := map[string]Version{}
	if err := s.backtrack(constraints, assignment); err != nil {
		return nil, err
	}
	return assignment, nil
}

type constraintWithSource struct {
	constraint Constraint
	source     string
}

type solverState struct {
	u universe
}

// backtrack assigns versions to every constrained cookbook. It mutates copies
// so a failed branch leaves no trace, then unwinds to try the next candidate.
func (s *solverState) backtrack(constraints map[string][]constraintWithSource, assignment map[string]Version) error {
	// Pick the next unassigned cookbook that has at least one constraint.
	// Deterministic order (sorted by name) so results are reproducible.
	name := s.nextUnassigned(constraints, assignment)
	if name == "" {
		return nil // every constrained cookbook is assigned
	}

	cons := constraints[name]
	cands, known := s.u[name]
	if !known {
		return &NoSolutionError{
			Cookbook:    name,
			Constraints: constraintStrings(cons),
			Detail:      fmt.Sprintf("cookbook %q is required but is not provided by any source", name),
		}
	}

	// Highest version first, matching chef's preference.
	ordered := append([]candidate(nil), cands...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].version.Compare(ordered[j].version) > 0 })

	var lastErr error
	for _, cand := range ordered {
		if !satisfiesAll(cand.version, cons) {
			continue
		}
		// Tentatively assign and fold in this version's dependency constraints.
		nextAssign := cloneAssign(assignment)
		nextAssign[name] = cand.version
		nextCons := cloneConstraints(constraints)

		conflict := false
		for _, dep := range cand.deps {
			c, err := ParseConstraint(dep.Constraint)
			if err != nil {
				return fmt.Errorf("cinc: cookbook %q depends on %q with invalid constraint %q: %w", name, dep.Name, dep.Constraint, err)
			}
			nextCons[dep.Name] = append(nextCons[dep.Name], constraintWithSource{c, fmt.Sprintf("%s-%s", name, cand.version)})
			// If the dependency is already assigned, it must still satisfy.
			if v, ok := nextAssign[dep.Name]; ok && !c.Satisfies(v) {
				conflict = true
				break
			}
		}
		if conflict {
			lastErr = &NoSolutionError{Cookbook: name, Constraints: constraintStrings(cons)}
			continue
		}

		if err := s.backtrack(nextCons, nextAssign); err != nil {
			lastErr = err
			continue
		}
		// Success: copy the resolved assignment back up.
		maps.Copy(assignment, nextAssign)
		return nil
	}

	if lastErr != nil {
		return lastErr
	}
	return &NoSolutionError{Cookbook: name, Constraints: constraintStrings(cons)}
}

// nextUnassigned returns the lexically-first cookbook that has constraints but
// no assignment yet, or "" when all are assigned.
func (s *solverState) nextUnassigned(constraints map[string][]constraintWithSource, assignment map[string]Version) string {
	names := make([]string, 0, len(constraints))
	for name := range constraints {
		if _, ok := assignment[name]; !ok {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return names[0]
}

func satisfiesAll(v Version, cons []constraintWithSource) bool {
	for _, c := range cons {
		if !c.constraint.Satisfies(v) {
			return false
		}
	}
	return true
}

func constraintStrings(cons []constraintWithSource) []string {
	out := make([]string, len(cons))
	for i, c := range cons {
		out[i] = c.constraint.raw
	}
	return out
}

func cloneAssign(m map[string]Version) map[string]Version {
	out := make(map[string]Version, len(m)+1)
	maps.Copy(out, m)
	return out
}

func cloneConstraints(m map[string][]constraintWithSource) map[string][]constraintWithSource {
	out := make(map[string][]constraintWithSource, len(m))
	for k, v := range m {
		out[k] = append([]constraintWithSource(nil), v...)
	}
	return out
}
