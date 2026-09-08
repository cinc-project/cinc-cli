package explore

import (
	"context"
	"fmt"
	"sort"

	cinc "github.com/cinc-project/cinc-api"
)

// cookbookKind is the top-level Cookbooks kind. A cookbook is a named
// set of versions; you drill into it to reach individual versions.
// Cookbooks have no name-level mutation — versions are uploaded by the
// `cinc cookbook upload` command and deleted/downloaded per version.
type cookbookKind struct{}

func (cookbookKind) Title() string     { return "Cookbooks" }
func (cookbookKind) Columns() []string { return []string{"NAME", "VERSIONS"} }

func (cookbookKind) List(ctx context.Context, c *cinc.Client) ([]Row, error) {
	index, _, err := c.Cookbooks.List(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]Row, 0, len(index))
	for name, entry := range index {
		rows = append(rows, Row{
			Name:  name,
			Cells: []string{name, fmt.Sprintf("%d", len(entry.Versions))},
		})
	}
	sortRows(rows)
	return rows, nil
}

func (cookbookKind) Child(parent string) Kind { return cookbookVersionsKind{name: parent} }

// Summary shows how many versions a cookbook has and which is newest, then the
// latest version's identity metadata — so the cookbook list surfaces what a
// cookbook is and who maintains it without having to drill into a version. The
// metadata lives on the per-version manifest, so we fetch the latest version to
// read it; the JSON carried along for the detail view is that same manifest.
func (cookbookKind) Summary(ctx context.Context, c *cinc.Client, name string) (summaryView, error) {
	index, _, err := c.Cookbooks.List(ctx)
	if err != nil {
		return summaryView{}, err
	}
	entry, ok := index[name]
	if !ok {
		return summaryView{}, fmt.Errorf("cookbook %q not found", name)
	}
	versions := len(entry.Versions)
	latest := latestCookbookVersion(entry.Versions)

	return summarize(ctx, c, latest,
		func(ctx context.Context, c *cinc.Client, v string) (*cinc.Cookbook, error) {
			if v == "" {
				// A cookbook with no versions has no manifest to read.
				return &cinc.Cookbook{}, nil
			}
			cb, _, err := c.Cookbooks.Get(ctx, name, v)
			return cb, err
		},
		nil,
		func(_ context.Context, _ *cinc.Client, cb *cinc.Cookbook) []summaryField {
			fields := []summaryField{
				{"Versions", count(versions)},
				{"Latest", orDash(latest)},
			}
			return append(fields, cookbookIdentityFields(cb)...)
		})
}

// latestCookbookVersion returns the newest version string, picking the
// greatest by string comparison to match the newest-first ordering the
// version list itself uses.
func latestCookbookVersion(versions []cinc.CookbookVersion) string {
	latest := ""
	for _, v := range versions {
		if v.Version > latest {
			latest = v.Version
		}
	}
	return latest
}

// cookbookVersionsKind lists the versions of one cookbook. A version
// can be viewed (its manifest), downloaded, or deleted.
type cookbookVersionsKind struct{ name string }

func (k cookbookVersionsKind) Title() string   { return k.name }
func (cookbookVersionsKind) Columns() []string { return []string{"VERSION"} }

func (k cookbookVersionsKind) List(ctx context.Context, c *cinc.Client) ([]Row, error) {
	index, _, err := c.Cookbooks.List(ctx)
	if err != nil {
		return nil, err
	}
	entry, ok := index[k.name]
	if !ok {
		return nil, fmt.Errorf("cookbook %q not found", k.name)
	}
	rows := make([]Row, 0, len(entry.Versions))
	for _, v := range entry.Versions {
		rows = append(rows, Row{Name: v.Version, Cells: []string{v.Version}})
	}
	// Versions sort newest-first so the latest is at the top.
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name > rows[j].Name })
	return rows, nil
}

func (k cookbookVersionsKind) Describe(ctx context.Context, c *cinc.Client, version string) (string, error) {
	cb, _, err := c.Cookbooks.Get(ctx, k.name, version)
	if err != nil {
		return "", err
	}
	return prettyJSON(cb)
}

// Summary shows a version's identity — what it does, who maintains it, and
// under what license — drawn from the cookbook metadata, plus how many files
// its manifest carries.
func (k cookbookVersionsKind) Summary(ctx context.Context, c *cinc.Client, version string) (summaryView, error) {
	return summarize(ctx, c, version,
		func(ctx context.Context, c *cinc.Client, v string) (*cinc.Cookbook, error) {
			cb, _, err := c.Cookbooks.Get(ctx, k.name, v)
			return cb, err
		},
		nil,
		func(_ context.Context, _ *cinc.Client, cb *cinc.Cookbook) []summaryField {
			return cookbookVersionSummaryFields(cb)
		})
}

// cookbookVersionSummaryFields builds the curated facts panel for a cookbook
// version: its version, the shared identity metadata, then the manifest file
// count (which, unlike the identity, is specific to this one version).
func cookbookVersionSummaryFields(cb *cinc.Cookbook) []summaryField {
	fields := []summaryField{{"Version", orDash(cb.Version)}}
	fields = append(fields, cookbookIdentityFields(cb)...)
	return append(fields, summaryField{"Files", count(len(cb.AllFiles()))})
}

// cookbookIdentityFields renders the metadata that identifies a cookbook
// independent of any one version's file manifest: what it does, who maintains it
// and how to reach them, its license and project links, and what it depends on.
// Both the cookbook-level pane (reading the latest version) and the per-version
// pane render through this so the two stay consistent.
func cookbookIdentityFields(cb *cinc.Cookbook) []summaryField {
	return []summaryField{
		{"Description", orDash(cb.Metadata.Description)},
		{"Maintainer", orDash(cb.Metadata.Maintainer)},
		{"Maintainer email", orDash(cb.Metadata.MaintainerEmail)},
		{"License", orDash(cb.Metadata.License)},
		{"Source URL", orDash(cb.Metadata.SourceURL)},
		{"Issues URL", orDash(cb.Metadata.IssuesURL)},
		{"Dependencies", cookbookDependencies(cb)},
	}
}

// cookbookDepsShown caps how many dependencies the summary lists before
// collapsing the rest into a "+N more" count, keeping the pane from blowing out
// on a cookbook with a sprawling dependency set.
const cookbookDepsShown = 6

// cookbookDependencies renders a version's dependency map as a sorted,
// human-readable list — "name constraint" per entry, e.g. "apt >= 7.0" — capped
// at cookbookDepsShown. Maps have no stable order, so we sort by cookbook name
// for deterministic output. The no-op ">= 0.0.0" constraint that a bare
// `depends 'foo'` produces is dropped as noise, leaving just the name.
func cookbookDependencies(cb *cinc.Cookbook) string {
	deps := cb.Metadata.Dependencies
	names := make([]string, 0, len(deps))
	for name := range deps {
		names = append(names, name)
	}
	sort.Strings(names)
	items := make([]string, 0, len(names))
	for _, name := range names {
		if c := deps[name]; c != "" && c != ">= 0.0.0" {
			items = append(items, name+" "+c)
		} else {
			items = append(items, name)
		}
	}
	return list(items, cookbookDepsShown)
}

func (k cookbookVersionsKind) Delete(ctx context.Context, c *cinc.Client, version string) error {
	_, err := c.Cookbooks.Delete(ctx, k.name, version)
	return err
}

func (k cookbookVersionsKind) Download(ctx context.Context, c *cinc.Client, version, destDir string) (string, error) {
	// Download writes the cookbook's manifest files (recipes/…,
	// metadata.rb, …) directly under destDir, so that's the path to
	// report back.
	if err := c.Cookbooks.Download(ctx, k.name, version, destDir); err != nil {
		return "", err
	}
	return destDir, nil
}
