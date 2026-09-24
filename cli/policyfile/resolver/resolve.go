// Package resolver completes a Policyfile install: it takes the evaluated
// intent of a Policyfile.rb (from cli/policyfile/rubyeval) and produces a fully
// resolved, push-ready Policyfile.lock.json that is byte-for-byte compatible
// with what `chef install` writes.
//
// It is the dependency-resolution half the evaluation engine deliberately left
// out, and it reproduces chef exactly in three places that must interoperate
// with a Chef Infra Server:
//
//   - Cookbook identifiers (identifier.go, computed by cinc-api): the SHA1
//     content identifier and its dotted-decimal reinterpretation, matching
//     CookbookProfiler::Identifiers.
//   - Version/dependency solving (solver.go): a deterministic backtracking
//     solver over cookbook metadata `depends` and the Policyfile's per-cookbook
//     constraints, selecting the same versions chef's Molinillo-based solver
//     does.
//   - Lock serialization (yajl.go): FFI_Yajl pretty output and the canonical
//     revision-string SHA256 that becomes the revision_id.
//
// The path: cookbook source is the primary, fully-supported backend; cookbook
// metadata is read from metadata.json or evaluated from metadata.rb with the
// embedded Ruby engine. Other sources (git, Supermarket, chef server) are not
// yet resolved here.
package resolver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/cinc-project/cinc-cli/cli/policyfile/rubyeval"
)

// Result is a fully resolved Policyfile lock plus a summary of what was
// resolved, for the command's human output.
type Result struct {
	// LockJSON is the Policyfile.lock.json, encoded byte-identically to chef.
	LockJSON []byte
	// RevisionID is the lock's revision_id (SHA256 of the canonical revision
	// string).
	RevisionID string
	// Cookbooks summarizes each resolved cookbook, sorted by name.
	Cookbooks []CookbookSummary
}

// CookbookSummary describes one resolved cookbook for display.
type CookbookSummary struct {
	Name       string
	Version    string
	Identifier string
	Source     string
}

// Resolve turns an evaluated Policyfile into a push-ready lock. rawEval is the
// engine's canonical JSON output for eval, used to preserve chef's exact
// attribute key ordering. policyfileDir is the directory the Policyfile lives
// in, used to resolve relative path: sources.
func Resolve(ctx context.Context, eng *rubyeval.Engine, eval *rubyeval.EvaluatedPolicy, rawEval []byte, policyfileDir string) (*Result, error) {
	if eng == nil {
		eng = rubyeval.NewEngine()
	}
	if len(eval.IncludedPolicies) > 0 {
		return nil, fmt.Errorf("cinc: include_policy is not yet supported by the resolver")
	}

	// Load every declared cookbook from its source. Only path: sources resolve
	// today; surface anything else as an explicit, honest error.
	cookbooks := map[string]*cookbookInfo{}
	for _, name := range sortedCookbookNames(eval.Cookbooks) {
		spec := eval.Cookbooks[name]
		path, ok := spec.SourceOptions["path"].(string)
		if !ok {
			return nil, unsupportedSourceErr(name, spec.SourceOptions)
		}
		info, err := loadPathCookbook(ctx, eng, policyfileDir, name, path)
		if err != nil {
			return nil, err
		}
		cookbooks[name] = info
	}

	// Build the solver universe and demands, then solve.
	u := universe{}
	for name, info := range cookbooks {
		u[name] = []candidate{{version: info.version, deps: info.deps}}
	}
	demands, err := buildDemands(eval, cookbooks)
	if err != nil {
		return nil, err
	}
	solution, err := solve(u, demands)
	if err != nil {
		return nil, err
	}

	// Every cookbook in the solution must have been located (path-only today).
	for name := range solution {
		if _, ok := cookbooks[name]; !ok {
			return nil, fmt.Errorf("cinc: cookbook %q is required transitively but no source provides it", name)
		}
	}

	lock, summaries, err := buildLock(eval, rawEval, cookbooks, solution, demands)
	if err != nil {
		return nil, err
	}

	revision := revisionID(eval, lock, cookbooks, solution)
	lock.set("revision_id", revision)
	// revision_id must be the first key; rebuild with it leading.
	ordered := newJSONObject()
	ordered.set("revision_id", revision)
	for _, k := range lock.keys {
		if k != "revision_id" {
			ordered.set(k, lock.vals[k])
		}
	}

	return &Result{
		LockJSON:   yajlEncode(ordered),
		RevisionID: revision,
		Cookbooks:  summaries,
	}, nil
}

// buildDemands computes chef's graph_demands: a constraint for every cookbook
// in the run lists plus every declared cookbook. A fixed-version (path) source
// demands its exact version; an otherwise-declared cookbook demands its
// Policyfile constraint; anything else demands ">= 0.0.0".
func buildDemands(eval *rubyeval.EvaluatedPolicy, cookbooks map[string]*cookbookInfo) ([]demand, error) {
	names := demandCookbookNames(eval, cookbooks)
	demands := make([]demand, 0, len(names))
	for _, name := range names {
		var raw string
		if info, ok := cookbooks[name]; ok && info.fixed {
			raw = "= " + info.verStr
		} else if spec, ok := eval.Cookbooks[name]; ok {
			raw = spec.VersionConstraint
		} else {
			raw = ">= 0.0.0"
		}
		c, err := ParseConstraint(raw)
		if err != nil {
			return nil, fmt.Errorf("cinc: cookbook %q has invalid constraint %q: %w", name, raw, err)
		}
		demands = append(demands, demand{name: name, constraint: c})
	}
	return demands, nil
}

// demandCookbookNames is chef's cookbooks_for_demands: the union of cookbook
// names referenced by the run lists and cookbook names declared in the
// Policyfile, sorted for determinism.
func demandCookbookNames(eval *rubyeval.EvaluatedPolicy, cookbooks map[string]*cookbookInfo) []string {
	set := map[string]struct{}{}
	for _, item := range eval.RunList {
		if cb := runListCookbookName(item); cb != "" {
			set[cb] = struct{}{}
		}
	}
	for _, items := range eval.NamedRunLists {
		for _, item := range items {
			if cb := runListCookbookName(item); cb != "" {
				set[cb] = struct{}{}
			}
		}
	}
	for name := range eval.Cookbooks {
		set[name] = struct{}{}
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// buildLock assembles every top-level lock field except revision_id (which is
// derived from the rest) in chef's key order, and returns a per-cookbook
// summary for display.
func buildLock(eval *rubyeval.EvaluatedPolicy, rawEval []byte, cookbooks map[string]*cookbookInfo, solution map[string]Version, demands []demand) (*jsonObject, []CookbookSummary, error) {
	lock := newJSONObject()
	lock.set("name", eval.Name)
	lock.set("run_list", normalizeRunList(eval.RunList))

	if len(eval.NamedRunLists) > 0 {
		named := newJSONObject()
		for _, name := range sortedStringKeys(eval.NamedRunLists) {
			named.set(name, normalizeRunList(eval.NamedRunLists[name]))
		}
		lock.set("named_run_lists", named)
	}

	lock.set("included_policy_locks", []any{})

	// cookbook_locks, sorted by name.
	locks := newJSONObject()
	solutionNames := sortedVersionKeys(solution)
	summaries := make([]CookbookSummary, 0, len(solutionNames))
	for _, name := range solutionNames {
		info := cookbooks[name]
		id, err := ComputeIdentifier(info.dir, info.verStr)
		if err != nil {
			return nil, nil, fmt.Errorf("cinc: cookbook %q: %w", name, err)
		}
		entry := newJSONObject()
		entry.set("version", info.verStr)
		entry.set("identifier", id.Content)
		entry.set("dotted_decimal_identifier", id.DottedDecimal)
		entry.set("source", info.source)
		entry.set("cache_key", nil)
		entry.set("scm_info", nil)
		entry.set("source_options", info.sourceOptions)
		locks.set(name, entry)
		summaries = append(summaries, CookbookSummary{
			Name: name, Version: info.verStr, Identifier: id.Content, Source: info.source,
		})
	}
	lock.set("cookbook_locks", locks)

	defAttrs, ovrAttrs, err := attributeObjects(rawEval)
	if err != nil {
		return nil, nil, err
	}
	lock.set("default_attributes", defAttrs)
	lock.set("override_attributes", ovrAttrs)

	lock.set("solution_dependencies", solutionDependencies(eval, cookbooks, solution, demands))
	return lock, summaries, nil
}

// solutionDependencies builds chef's solution_dependencies: the Policyfile
// demands (every declared/required cookbook with its constraint) and the
// per-version dependency lists, both in chef's exact shape and ordering.
func solutionDependencies(eval *rubyeval.EvaluatedPolicy, cookbooks map[string]*cookbookInfo, solution map[string]Version, demands []demand) *jsonObject {
	out := newJSONObject()

	// "Policyfile": [[name, constraint], ...] for all cookbook location specs
	// (declared cookbooks), sorted.
	type pair struct{ name, constraint string }
	var pf []pair
	for name := range eval.Cookbooks {
		pf = append(pf, pair{name, eval.Cookbooks[name].VersionConstraint})
	}
	sort.Slice(pf, func(i, j int) bool {
		if pf[i].name != pf[j].name {
			return pf[i].name < pf[j].name
		}
		return pf[i].constraint < pf[j].constraint
	})
	pfArr := make([]any, 0, len(pf))
	for _, p := range pf {
		pfArr = append(pfArr, []any{p.name, p.constraint})
	}
	out.set("Policyfile", pfArr)

	// "dependencies": {"name (version)": [[dep, constraint], ...]} sorted by key.
	deps := newJSONObject()
	for _, name := range sortedVersionKeys(solution) {
		info := cookbooks[name]
		key := fmt.Sprintf("%s (%s)", name, info.verStr)
		depArr := make([]any, 0, len(info.deps))
		for _, d := range info.deps {
			depArr = append(depArr, []any{d.Name, d.Constraint})
		}
		deps.set(key, depArr)
	}
	// Sort dependency keys lexically (chef does .sort.to_h).
	sortJSONObjectKeys(deps)
	out.set("dependencies", deps)
	return out
}

// revisionID computes the lock's revision_id: the SHA256 of chef's canonical
// revision string (name, run-list items, named run lists, cookbook id lines,
// and canonicalized attribute trees).
func revisionID(eval *rubyeval.EvaluatedPolicy, lock *jsonObject, cookbooks map[string]*cookbookInfo, solution map[string]Version) string {
	var b strings.Builder
	b.WriteString("name:" + eval.Name + "\n")
	for _, item := range normalizeRunListStrings(eval.RunList) {
		b.WriteString("run-list-item:" + item + "\n")
	}
	for _, name := range sortedStringKeys(eval.NamedRunLists) {
		for _, item := range normalizeRunListStrings(eval.NamedRunLists[name]) {
			b.WriteString("named-run-list:" + name + ";run-list-item:" + item + "\n")
		}
	}
	locks, _ := lock.vals["cookbook_locks"].(*jsonObject)
	for _, name := range locks.keys {
		entry := locks.vals[name].(*jsonObject)
		b.WriteString("cookbook:" + name + ";id:" + entry.vals["identifier"].(string) + "\n")
	}
	b.WriteString("default_attributes:" + canonicalize(lock.vals["default_attributes"]) + "\n")
	b.WriteString("override_attributes:" + canonicalize(lock.vals["override_attributes"]) + "\n")
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// attributeObjects extracts the default/override attribute trees from the
// engine's raw output, preserving key order exactly as chef wrote them.
func attributeObjects(rawEval []byte) (def, ovr any, err error) {
	parsed, err := parseOrdered(rawEval)
	if err != nil {
		return nil, nil, fmt.Errorf("cinc: parse evaluation output: %w", err)
	}
	root, ok := parsed.(*jsonObject)
	if !ok {
		return newJSONObject(), newJSONObject(), nil
	}
	def = orDefaultObject(root.vals["default_attributes"])
	ovr = orDefaultObject(root.vals["override_attributes"])
	return def, ovr, nil
}

func orDefaultObject(v any) any {
	if v == nil {
		return newJSONObject()
	}
	return v
}

// unsupportedSourceErr reports an honest error for a cookbook whose source the
// resolver does not yet handle.
func unsupportedSourceErr(name string, opts map[string]any) error {
	for _, k := range []string{"git", "github", "supermarket", "artifactserver", "chef_server"} {
		if _, ok := opts[k]; ok {
			return fmt.Errorf("cinc: cookbook %q uses a %s source, which the resolver can't resolve yet; only path: sources are supported", name, k)
		}
	}
	return fmt.Errorf("cinc: cookbook %q has no resolvable source (only path: sources are supported yet)", name)
}
