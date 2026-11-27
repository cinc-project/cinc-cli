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
// satisfy. These are chef's graph_demands. from says where the demand came
// from, for error messages; empty means the Policyfile's run lists or
// cookbook lines.
type demand struct {
	name       string
	constraint Constraint
	from       string
}

// Requirement is one constraint on a cookbook and what imposed it ("the
// Policyfile", or the "name version" of a cookbook whose metadata depends on
// it), so a failed solve can say which demands clash.
type Requirement struct {
	Constraint string
	From       string
}

// NoSolutionError reports that the demands and the universe cannot be satisfied
// together. It names the cookbook that could not be resolved and the
// constraints that conflicted, matching chef's failure mode (a NoSolutionError
// listing the unsatisfiable requirements) even though the wording differs.
type NoSolutionError struct {
	Cookbook    string
	Constraints []string
	// Requirements pairs each of Constraints with what imposed it.
	Requirements []Requirement
	// Available lists the versions of Cookbook the sources offer, highest
	// first. It is empty when no source provides the cookbook at all.
	Available []string
	Detail    string
}

func (e *NoSolutionError) Error() string {
	if e.Detail != "" {
		return e.Detail
	}
	if len(e.Available) == 0 {
		return missingCookbookMessage(e.Cookbook, e.Requirements)
	}
	return conflictMessage(e.Cookbook, e.Requirements, e.Available)
}

// missingCookbookMessage explains a cookbook the policy needs that no
// cookbook line in the Policyfile provides, naming who needs it and the line
// that fixes it.
func missingCookbookMessage(name string, reqs []Requirement) string {
	var b strings.Builder
	fmt.Fprintf(&b, "cookbook %q is needed, but the Policyfile doesn't say where to get it.\n\n", name)
	b.WriteString("It's needed by:\n")
	for _, r := range reqs {
		b.WriteString("  - " + describeRequirement(name, r) + "\n")
	}
	fmt.Fprintf(&b, `
Every cookbook a policy uses has to come from somewhere, including the ones
other cookbooks depend on. For now the resolver only reads cookbooks from a
local directory (a path: source); a default_source such as :supermarket isn't
used to fetch cookbooks yet. Add a line like this to the Policyfile, with the
path of a copy of the cookbook on disk (relative to the Policyfile):

  cookbook '%s', path: '../%s'`, name, name)
	return b.String()
}

// conflictMessage explains a cookbook whose available versions don't satisfy
// every constraint on it, listing each constraint and where it came from.
func conflictMessage(name string, reqs []Requirement, available []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "we can't find a version of cookbook %q that satisfies everything that depends on it.\n\n", name)
	b.WriteString("These requirements have to hold at the same time:\n")
	for _, r := range reqs {
		b.WriteString("  - " + describeRequirement(name, r) + "\n")
	}
	if len(available) == 1 {
		fmt.Fprintf(&b, "\nThe only version of %q available is %s.\n", name, available[0])
	} else {
		fmt.Fprintf(&b, "\nThe versions of %q available are %s.\n", name, strings.Join(available, ", "))
	}
	var dependents []string
	for _, r := range reqs {
		if r.From != "" && r.From != policyfileOrigin && !strings.HasPrefix(r.From, pathOriginPrefix) {
			cb, _, _ := strings.Cut(r.From, " ")
			dependents = append(dependents, cb+"'s")
		}
	}
	whose := "the metadata.rb of each cookbook above"
	if len(dependents) > 0 {
		whose = strings.Join(dependents, " and ") + " metadata.rb"
	}
	fmt.Fprintf(&b, `
To fix it, do one of these:
  - Change the depends '%s' line in %s so it accepts a version you have.
  - Point the Policyfile's cookbook '%s', path: line at a copy of %q whose
    version (in its metadata.rb) meets every requirement above.`, name, whose, name, name)
	return b.String()
}

// describeRequirement renders one requirement for a list, such as
// `app 1.2.0 depends on lib "~> 2.0"`.
func describeRequirement(name string, r Requirement) string {
	switch {
	case r.From == "" || r.From == policyfileOrigin:
		if r.Constraint == "" || r.Constraint == ">= 0.0.0" {
			return "the Policyfile's run_list"
		}
		return fmt.Sprintf("the Policyfile asks for %s %q", name, r.Constraint)
	case strings.HasPrefix(r.From, pathOriginPrefix):
		return fmt.Sprintf("the Policyfile's cookbook '%s', path: '%s' line, which provides %s %s",
			name, strings.TrimPrefix(r.From, pathOriginPrefix), name, strings.TrimSpace(strings.TrimPrefix(r.Constraint, "=")))
	default:
		return fmt.Sprintf("%s depends on %s %q", r.From, name, r.Constraint)
	}
}

// pathOriginPrefix marks a demand that pins a cookbook to the one version its
// path: source provides; the rest of the origin is that path.
const pathOriginPrefix = "path:"

// policyfileOrigin labels a demand that comes from the Policyfile's run
// lists or cookbook lines.
const policyfileOrigin = "the Policyfile"

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
		from := d.from
		if from == "" {
			from = policyfileOrigin
		}
		constraints[d.name] = append(constraints[d.name], constraintWithSource{d.constraint, from})
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
		return newNoSolutionError(name, cons, nil)
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
				return fmt.Errorf("cookbook %q %s depends on %q, but its version constraint isn't one we can read: %w. Fix the depends line in that cookbook's metadata", name, cand.version, dep.Name, err)
			}
			nextCons[dep.Name] = append(nextCons[dep.Name], constraintWithSource{c, fmt.Sprintf("%s %s", name, cand.version)})
			// If the dependency is already assigned, it must still satisfy.
			// The clash is on the dependency, so that's what the error names.
			if v, ok := nextAssign[dep.Name]; ok && !c.Satisfies(v) {
				conflict = true
				lastErr = newNoSolutionError(dep.Name, nextCons[dep.Name], s.u[dep.Name])
				break
			}
		}
		if conflict {
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
	return newNoSolutionError(name, cons, cands)
}

// newNoSolutionError builds the error for a cookbook whose constraints cons
// no candidate in cands satisfies (or that has no candidates at all).
func newNoSolutionError(name string, cons []constraintWithSource, cands []candidate) *NoSolutionError {
	e := &NoSolutionError{Cookbook: name, Constraints: constraintStrings(cons)}
	for _, c := range cons {
		e.Requirements = append(e.Requirements, Requirement{Constraint: c.constraint.raw, From: c.source})
	}
	ordered := append([]candidate(nil), cands...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].version.Compare(ordered[j].version) > 0 })
	for _, c := range ordered {
		e.Available = append(e.Available, c.version.String())
	}
	return e
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
