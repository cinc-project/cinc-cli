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
	if err != nil {
		shown, pfDir := absOr(abs), absOr(policyfileDir)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, &explainedError{err: err, msg: fmt.Sprintf("cookbook %q has path: '%s' in the Policyfile, but there's nothing at %s. "+
				"A path: source is relative to the directory the Policyfile is in (%s), so fix the path to point at the cookbook's directory",
				name, declaredPath, shown, pfDir)}
		}
		return nil, fmt.Errorf("cookbook %q has path: '%s' in the Policyfile, but we can't open %s: %w", name, declaredPath, shown, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("cookbook %q has path: '%s' in the Policyfile, but %s is a file. "+
			"A path: source has to point at the cookbook's directory (the one holding its metadata.rb), not at an archive or a single file",
			name, declaredPath, absOr(abs))
	}

	rel, err := filepath.Rel(policyfileDir, abs)
	if err != nil {
		rel = declaredPath
	}
	relSlash := filepath.ToSlash(rel)

	md, err := readCookbookMetadata(ctx, eng, abs)
	if err != nil {
		return nil, fmt.Errorf("cookbook %q: %w", name, err)
	}
	ver, err := ParseVersion(md.Version)
	if err != nil {
		return nil, fmt.Errorf("cookbook %q in %s sets a version we can't read: %w. Set it in the cookbook's metadata, for example version '1.2.3' in metadata.rb", name, abs, err)
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

// explainedError is an error whose message already says everything the
// wrapped error would, so it prints only msg; errors.Is and errors.As still
// reach err.
type explainedError struct {
	err error
	msg string
}

func (e *explainedError) Error() string { return e.msg }
func (e *explainedError) Unwrap() error { return e.err }

// absOr returns p made absolute for a message, or p itself if that fails.
func absOr(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
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
			md, err := eng.EvaluateMetadataFile(ctx, rbPath)
			if err != nil {
				return nil, fmt.Errorf("we couldn't evaluate %s: %w", rbPath, err)
			}
			return md, nil
		}
		return nil, fmt.Errorf("%s doesn't look like a cookbook: it has no metadata.rb or metadata.json. "+
			"Every cookbook has one at its top level, naming it and setting its version, so point the path: source at the directory that holds it", dir)
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
			return nil, fmt.Errorf("%s depends on %q with a version constraint we can't read: %w", jsonPath, n, err)
		}
		md.Dependencies = append(md.Dependencies, rubyeval.Dependency{Name: n, Constraint: normalizeConstraint(c)})
	}
	return md, nil
}
