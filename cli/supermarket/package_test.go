package supermarket

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeCookbookFiles writes files (cookbook-relative path -> content) into a
// cookbook directory named name under a fresh root, and returns the root.
func writeCookbookFiles(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(root, name, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// tarballEntries returns every entry of a gzipped tarball, name -> content.
func tarballEntries(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	entries := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return entries
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		entries[hdr.Name] = body
	}
}

func sortedNames(entries map[string][]byte) []string {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// TestShareTarballHoldsTheFilesChefWouldLoad pins the tarball to Chef's
// cookbook loader: dot-directories at the cookbook root (.kitchen, .github)
// and chef-zero's sentinel stay out, dotfiles and deeper dot-directories stay
// in, and a symlink to a file inside the cookbook ships its content.
func TestShareTarballHoldsTheFilesChefWouldLoad(t *testing.T) {
	root := writeCookbookFiles(t, "nginx", map[string]string{
		"metadata.json":                   `{"name":"nginx","version":"1.2.0"}`,
		"recipes/default.rb":              "package 'nginx'\n",
		".rubocop.yml":                    "AllCops: {}\n",
		"files/.hidden/conf":              "conf\n",
		".kitchen/state.yml":              "state\n",
		".github/workflows/ci.yml":        "on: push\n",
		".uploaded-cookbook-version.json": "{}\n",
	})
	if err := os.Symlink("default.rb", filepath.Join(root, "nginx", "recipes", "linked.rb")); err != nil {
		t.Fatal(err)
	}

	_, archive, err := packageCookbook(ShareOptions{Cookbook: "nginx", CookbookPath: root}, "Other", true)
	if err != nil {
		t.Fatalf("packageCookbook: %v", err)
	}
	entries := tarballEntries(t, archive.Bytes)
	want := []string{
		"nginx/.rubocop.yml",
		"nginx/files/.hidden/conf",
		"nginx/metadata.json",
		"nginx/recipes/default.rb",
		"nginx/recipes/linked.rb",
	}
	if got := sortedNames(entries); !slices.Equal(got, want) {
		t.Fatalf("tarball entries = %v, want %v", got, want)
	}
	if got := string(entries["nginx/recipes/linked.rb"]); got != "package 'nginx'\n" {
		t.Errorf("linked.rb = %q, want the link target's content", got)
	}
	if !slices.Equal(archive.Files, want) {
		t.Errorf("archive.Files = %v, want %v", archive.Files, want)
	}
}

// TestShareCompiledMetadataKeepsPrivacyAndEagerLoad covers the metadata.json
// a metadata.rb-only cookbook is shared with: it must say what metadata.rb
// says, including privacy (a private cookbook must not go up as public) and
// eager_load_libraries.
func TestShareCompiledMetadataKeepsPrivacyAndEagerLoad(t *testing.T) {
	root := writeCookbookFiles(t, "nginx", map[string]string{
		"metadata.rb": "name 'nginx'\nversion '1.2'\nprivacy true\neager_load_libraries false\n" +
			"depends 'apt', '~> 7.0'\n",
		"recipes/default.rb": "package 'nginx'\n",
	})

	result, archive, err := packageCookbook(ShareOptions{Cookbook: "nginx", CookbookPath: root}, "Other", true)
	if err != nil {
		t.Fatalf("packageCookbook: %v", err)
	}
	if result.Version != "1.2.0" {
		t.Errorf("version = %q, want 1.2.0", result.Version)
	}
	var md map[string]any
	if err := json.Unmarshal(tarballEntries(t, archive.Bytes)["nginx/metadata.json"], &md); err != nil {
		t.Fatalf("metadata.json: %v", err)
	}
	if md["privacy"] != true {
		t.Errorf("privacy = %v, want true", md["privacy"])
	}
	if md["eager_load_libraries"] != false {
		t.Errorf("eager_load_libraries = %v, want false", md["eager_load_libraries"])
	}
	if md["version"] != "1.2.0" || md["name"] != "nginx" {
		t.Errorf("name/version = %v/%v, want nginx/1.2.0", md["name"], md["version"])
	}
	if deps, _ := md["dependencies"].(map[string]any); deps["apt"] != "~> 7.0" {
		t.Errorf("dependencies = %v, want apt ~> 7.0", md["dependencies"])
	}
}

// TestShareKeepsTheCookbooksOwnMetadataJSON ships a metadata.json exactly as
// the cookbook has it, rather than a recompiled copy.
func TestShareKeepsTheCookbooksOwnMetadataJSON(t *testing.T) {
	body := `{"name":"nginx","version":"1.2.0","attributes":{"x":{}}}`
	root := writeCookbookFiles(t, "nginx", map[string]string{
		"metadata.json":      body,
		"recipes/default.rb": "package 'nginx'\n",
	})

	_, archive, err := packageCookbook(ShareOptions{Cookbook: "nginx", CookbookPath: root}, "Other", true)
	if err != nil {
		t.Fatalf("packageCookbook: %v", err)
	}
	if got := string(tarballEntries(t, archive.Bytes)["nginx/metadata.json"]); got != body {
		t.Errorf("metadata.json = %q, want the file as written", got)
	}
}

// TestShareNamesTheCookbookAfterItsDirectory covers a metadata.rb with no
// name, shared from inside the cookbook: like knife, the name is the
// directory's, never ".".
func TestShareNamesTheCookbookAfterItsDirectory(t *testing.T) {
	root := writeCookbookFiles(t, "nginx", map[string]string{
		"metadata.rb":        "version '1.0.0'\n",
		"recipes/default.rb": "package 'nginx'\n",
	})
	supermarketPushdir(t, filepath.Join(root, "nginx"))

	_, archive, err := packageCookbook(ShareOptions{Cookbook: "nginx"}, "Other", true)
	if err != nil {
		t.Fatalf("packageCookbook: %v", err)
	}
	var md map[string]any
	if err := json.Unmarshal(tarballEntries(t, archive.Bytes)["nginx/metadata.json"], &md); err != nil {
		t.Fatalf("metadata.json: %v", err)
	}
	if md["name"] != "nginx" {
		t.Errorf("metadata.json name = %v, want nginx", md["name"])
	}
}

// TestShareRefusesAComputedVersion refuses a metadata.rb whose version only
// Ruby could work out, telling the user how to get past it, rather than
// sharing the cookbook as 0.0.0.
func TestShareRefusesAComputedVersion(t *testing.T) {
	root := writeCookbookFiles(t, "nginx", map[string]string{
		"metadata.rb":        "name 'nginx'\nversion IO.read('VERSION').strip\n",
		"recipes/default.rb": "package 'nginx'\n",
	})

	_, _, err := packageCookbook(ShareOptions{Cookbook: "nginx", CookbookPath: root}, "Other", true)
	if err == nil {
		t.Fatal("packageCookbook succeeded, want an error for a computed version")
	}
	if !strings.Contains(err.Error(), "metadata.json") || !strings.Contains(err.Error(), "version") {
		t.Errorf("error = %q, want it to explain the version and suggest a metadata.json", err)
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Errorf("error = %q, should not read as a missing file", err)
	}
}
