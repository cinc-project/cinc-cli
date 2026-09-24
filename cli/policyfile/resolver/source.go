package resolver

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"

	cinc "github.com/cinc-project/cinc-api"

	"github.com/cinc-project/cinc-cli/cli/policyfile/rubyeval"
)

// cookbookInfo is everything the resolver needs about one cookbook it has
// located from a source: its identity (name/version), its dependencies, where
// it lives on disk (for identifier computation and upload), and the lock fields
// describing how it is sourced.
type cookbookInfo struct {
	name    string
	version Version
	verStr  string // the metadata version string, written verbatim into the lock
	deps    []rubyeval.Dependency
	dir     string // on-disk cookbook root

	// Lock fields.
	source        string      // relative "source" path for local cookbooks
	sourceOptions *jsonObject // ordered source_options for the lock

	fixed bool // version_fixed? (path/git resolve to a single version)
}

// loadPathCookbook resolves a `cookbook "name", path: "..."` declaration: it
// reads the cookbook's metadata (metadata.json if present, else metadata.rb via
// the embedded Ruby engine) and records the on-disk directory plus the lock's
// source / source_options, computed relative to the Policyfile directory the
// way chef's CookbookOmnifetch::PathLocation does.
func loadPathCookbook(ctx context.Context, eng *rubyeval.Engine, policyfileDir, name, declaredPath string) (*cookbookInfo, error) {
	abs := declaredPath
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(policyfileDir, declaredPath)
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("cinc: cookbook %q path source %q is not a directory: %w", name, declaredPath, err)
	}

	rel, err := filepath.Rel(policyfileDir, abs)
	if err != nil {
		rel = declaredPath
	}
	relSlash := filepath.ToSlash(rel)

	md, err := readCookbookMetadata(ctx, eng, abs)
	if err != nil {
		return nil, fmt.Errorf("cinc: cookbook %q: %w", name, err)
	}
	ver, err := ParseVersion(md.Version)
	if err != nil {
		return nil, fmt.Errorf("cinc: cookbook %q has invalid version %q: %w", name, md.Version, err)
	}

	so := newJSONObject()
	so.set("path", relSlash)

	return &cookbookInfo{
		name:          name,
		version:       ver,
		verStr:        md.Version,
		deps:          md.Dependencies,
		dir:           abs,
		source:        relSlash,
		sourceOptions: so,
		fixed:         true,
	}, nil
}

// readCookbookMetadata loads a cookbook's metadata from dir. metadata.json wins
// when present (chef reads it directly, and so does cinc-api); otherwise
// metadata.rb is evaluated in the embedded CRuby engine. Dependency
// constraints come back Semverse-normalized either way.
func readCookbookMetadata(ctx context.Context, eng *rubyeval.Engine, dir string) (*rubyeval.Metadata, error) {
	jsonPath := filepath.Join(dir, "metadata.json")
	data, err := os.ReadFile(jsonPath)
	if errors.Is(err, fs.ErrNotExist) {
		rbPath := filepath.Join(dir, "metadata.rb")
		if _, err := os.Stat(rbPath); err == nil {
			return eng.EvaluateMetadataFile(ctx, rbPath)
		}
		return nil, fmt.Errorf("no metadata.rb or metadata.json found in %s", dir)
	}
	if err != nil {
		return nil, err
	}
	mj, err := cinc.ParseMetadataJSON(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", jsonPath, err)
	}
	// Normalize each dependency constraint through the same Semverse semantics
	// chef applies, so the lock's solution_dependencies match. A JSON object
	// has no order, so dependencies are sorted by name.
	md := &rubyeval.Metadata{Name: mj.Name, Version: mj.Version}
	for _, n := range slices.Sorted(maps.Keys(mj.Dependencies)) {
		c, err := ParseConstraint(mj.Dependencies[n])
		if err != nil {
			return nil, fmt.Errorf("%s: dependency %q: %w", jsonPath, n, err)
		}
		md.Dependencies = append(md.Dependencies, rubyeval.Dependency{Name: n, Constraint: normalizeConstraint(c)})
	}
	return md, nil
}
