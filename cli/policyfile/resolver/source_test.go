package resolver

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cinc-project/cinc-cli/cli/policyfile/rubyeval"
)

// TestReadCookbookMetadataJSONAcceptsLegacyConstraintArrays reads a
// metadata.json written by an old knife, whose dependencies carry their
// constraint in a one-element array. Chef unwraps it, so we must too.
func TestReadCookbookMetadataJSONAcceptsLegacyConstraintArrays(t *testing.T) {
	dir := t.TempDir()
	body := `{"name":"web","version":"1.2.0","dependencies":{"util":[">= 1.0"],"apt":"~> 7.0"}}`
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	// metadata.json never reaches the Ruby engine, so none is needed.
	md, err := readCookbookMetadata(context.Background(), nil, dir)
	if err != nil {
		t.Fatalf("readCookbookMetadata: %v", err)
	}
	if md.Name != "web" || md.Version != "1.2.0" {
		t.Errorf("name/version = %s/%s, want web/1.2.0", md.Name, md.Version)
	}
	want := []rubyeval.Dependency{{Name: "apt", Constraint: "~> 7.0"}, {Name: "util", Constraint: ">= 1.0.0"}}
	if !slices.Equal(md.Dependencies, want) {
		t.Errorf("dependencies = %v, want %v", md.Dependencies, want)
	}
}
