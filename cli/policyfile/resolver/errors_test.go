package resolver

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cinc-project/cinc-cli/cli/policyfile/rubyeval"
)

// writeJSONCookbook writes a cookbook with a metadata.json (so resolving it
// needs no Ruby) under dir/name.
func writeJSONCookbook(t *testing.T, dir, name, version string, deps map[string]string) string {
	t.Helper()
	cb := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Join(cb, "recipes"), 0o755); err != nil {
		t.Fatal(err)
	}
	md, err := json.Marshal(map[string]any{"name": name, "version": version, "dependencies": deps})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cb, "metadata.json"), md, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cb, "recipes", "default.rb"), []byte("# "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return cb
}

func resolveErr(t *testing.T, eval *rubyeval.EvaluatedPolicy, dir string) error {
	t.Helper()
	_, err := Resolve(context.Background(), nil, eval, []byte("{}"), dir)
	if err == nil {
		t.Fatal("Resolve succeeded, want an error")
	}
	return err
}

func wantAll(t *testing.T, err error, wants ...string) {
	t.Helper()
	msg := err.Error()
	for _, want := range wants {
		if !strings.Contains(msg, want) {
			t.Errorf("error should mention %q:\n%s", want, msg)
		}
	}
}

func pathSpec(path string) rubyeval.CookbookSpec {
	return rubyeval.CookbookSpec{VersionConstraint: ">= 0.0.0", SourceOptions: map[string]any{"path": path}}
}

// A scaffolded Policyfile names a cookbook in its run_list but has no
// cookbook line for it. The error says who needs it and shows the line to add.
func TestResolveExplainsARunListCookbookWithNoSource(t *testing.T) {
	dir := t.TempDir()
	eval := &rubyeval.EvaluatedPolicy{
		Name:           "base",
		RunList:        []string{"recipe[base::default]"},
		DefaultSources: []rubyeval.DefaultSource{{Type: "community", URI: "https://supermarket.chef.io"}},
	}
	err := resolveErr(t, eval, dir)
	if _, ok := errors.AsType[*NoSolutionError](err); !ok {
		t.Errorf("want a *NoSolutionError, got %T", err)
	}
	wantAll(t, err,
		`cookbook "base"`,
		"run_list",
		`cookbook 'base', path:`,
		"default_source",
	)
}

// A cookbook another cookbook depends on, with no cookbook line of its own,
// names the cookbook that depends on it.
func TestResolveExplainsATransitiveCookbookWithNoSource(t *testing.T) {
	dir := t.TempDir()
	writeJSONCookbook(t, dir, "app", "1.2.0", map[string]string{"lib": "~> 2.0"})
	eval := &rubyeval.EvaluatedPolicy{
		Name:      "app",
		RunList:   []string{"recipe[app::default]"},
		Cookbooks: map[string]rubyeval.CookbookSpec{"app": pathSpec("app")},
	}
	err := resolveErr(t, eval, dir)
	wantAll(t, err,
		`cookbook "lib"`,
		"app 1.2.0",
		"~> 2.0",
		`cookbook 'lib', path:`,
	)
}

// When the only version on disk doesn't satisfy what depends on it, the
// error lists each requirement, who made it, and the version available.
func TestResolveExplainsAVersionConflict(t *testing.T) {
	dir := t.TempDir()
	writeJSONCookbook(t, dir, "app", "1.2.0", map[string]string{"lib": "~> 3.0"})
	writeJSONCookbook(t, dir, "lib", "1.0.0", nil)
	eval := &rubyeval.EvaluatedPolicy{
		Name:    "app",
		RunList: []string{"recipe[app::default]"},
		Cookbooks: map[string]rubyeval.CookbookSpec{
			"app": pathSpec("app"),
			"lib": pathSpec("lib"),
		},
	}
	err := resolveErr(t, eval, dir)
	if _, ok := errors.AsType[*NoSolutionError](err); !ok {
		t.Errorf("want a *NoSolutionError, got %T", err)
	}
	wantAll(t, err,
		`cookbook "lib"`,
		"app 1.2.0",
		"~> 3.0",
		"1.0.0",
		"metadata",
	)
}

func TestResolveExplainsIncludePolicy(t *testing.T) {
	eval := &rubyeval.EvaluatedPolicy{
		Name:             "app",
		RunList:          []string{"recipe[app::default]"},
		IncludedPolicies: []rubyeval.IncludedPolicy{{Name: "base"}},
	}
	err := resolveErr(t, eval, t.TempDir())
	wantAll(t, err, "include_policy", `"base"`, "path:")
}

func TestResolveExplainsAnUnsupportedSource(t *testing.T) {
	eval := &rubyeval.EvaluatedPolicy{
		Name:    "app",
		RunList: []string{"recipe[app::default]"},
		Cookbooks: map[string]rubyeval.CookbookSpec{
			"app": {SourceOptions: map[string]any{"git": "https://example.test/app.git"}},
		},
	}
	err := resolveErr(t, eval, t.TempDir())
	wantAll(t, err, `cookbook "app"`, "git", "https://example.test/app.git", `cookbook 'app', path:`)
}

func TestResolveExplainsACookbookLineWithNoSource(t *testing.T) {
	eval := &rubyeval.EvaluatedPolicy{
		Name:      "app",
		RunList:   []string{"recipe[app::default]"},
		Cookbooks: map[string]rubyeval.CookbookSpec{"app": {VersionConstraint: ">= 0.0.0"}},
	}
	err := resolveErr(t, eval, t.TempDir())
	wantAll(t, err, `cookbook "app"`, `cookbook 'app', path:`)
}

func TestResolveExplainsAMissingPathSource(t *testing.T) {
	dir := t.TempDir()
	eval := &rubyeval.EvaluatedPolicy{
		Name:      "app",
		RunList:   []string{"recipe[app::default]"},
		Cookbooks: map[string]rubyeval.CookbookSpec{"app": pathSpec("../nowhere")},
	}
	err := resolveErr(t, eval, dir)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("errors.Is(err, fs.ErrNotExist) = false for %v", err)
	}
	if strings.Contains(err.Error(), "%!") {
		t.Errorf("error has a formatting bug: %s", err)
	}
	wantAll(t, err, `cookbook "app"`, "../nowhere", filepath.Join(filepath.Dir(dir), "nowhere"), "relative to")
}

func TestResolveExplainsAPathSourceThatIsAFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.tar.gz"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	eval := &rubyeval.EvaluatedPolicy{
		Name:      "app",
		RunList:   []string{"recipe[app::default]"},
		Cookbooks: map[string]rubyeval.CookbookSpec{"app": pathSpec("app.tar.gz")},
	}
	err := resolveErr(t, eval, dir)
	if strings.Contains(err.Error(), "%!") {
		t.Errorf("error has a formatting bug: %s", err)
	}
	wantAll(t, err, `cookbook "app"`, "app.tar.gz", "file")
}

func TestResolveExplainsAPathSourceWithNoMetadata(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "cookbooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	eval := &rubyeval.EvaluatedPolicy{
		Name:      "app",
		RunList:   []string{"recipe[app::default]"},
		Cookbooks: map[string]rubyeval.CookbookSpec{"app": pathSpec("cookbooks")},
	}
	err := resolveErr(t, eval, dir)
	wantAll(t, err, `cookbook "app"`, filepath.Join(dir, "cookbooks"), "metadata.rb", "metadata.json")
}

func TestResolveExplainsABadMetadataVersion(t *testing.T) {
	dir := t.TempDir()
	writeJSONCookbook(t, dir, "app", "one.two", nil)
	eval := &rubyeval.EvaluatedPolicy{
		Name:      "app",
		RunList:   []string{"recipe[app::default]"},
		Cookbooks: map[string]rubyeval.CookbookSpec{"app": pathSpec("app")},
	}
	err := resolveErr(t, eval, dir)
	wantAll(t, err, `cookbook "app"`, `"one.two"`, `1.2.3`)
}

func TestParseErrorsShowValidForms(t *testing.T) {
	if _, err := ParseVersion("latest"); err == nil {
		t.Fatal("ParseVersion accepted latest")
	} else {
		wantAll(t, err, `"latest"`, "1.2.3")
	}
	if _, err := ParseConstraint("about 2"); err == nil {
		t.Fatal("ParseConstraint accepted about 2")
	} else {
		wantAll(t, err, `"about 2"`, "~> 1.2", ">= 2.0")
	}
}
