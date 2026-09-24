package cookbook

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// writeFiles writes files (cookbook-relative path -> content) under dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// archiveEntries returns a gzipped tarball's entries, name -> content, and
// their names in archive order.
func archiveEntries(t *testing.T, data []byte) (map[string][]byte, []string) {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	entries := map[string][]byte{}
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return entries, names
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		entries[hdr.Name] = body
		names = append(names, hdr.Name)
	}
}

func loadArchive(t *testing.T, dir string, skipChefignore bool, overlay map[string][]byte) Archive {
	t.Helper()
	cb, err := Load(dir, skipChefignore)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	archive, err := BuildArchive(cb, overlay)
	if err != nil {
		t.Fatalf("BuildArchive: %v", err)
	}
	return archive
}

func TestBuildArchiveRootsFilesAtCookbookName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "chef-nginx")
	writeFiles(t, dir, map[string]string{
		"metadata.json":      `{"name":"nginx","version":"1.2.0"}`,
		"recipes/default.rb": "package 'nginx'\n",
	})

	archive := loadArchive(t, dir, false, nil)
	if archive.Name != "nginx.tgz" {
		t.Errorf("archive name = %q, want nginx.tgz", archive.Name)
	}
	_, names := archiveEntries(t, archive.Bytes)
	want := []string{"nginx/metadata.json", "nginx/recipes/default.rb"}
	if !slices.Equal(names, want) {
		t.Fatalf("archive entries = %v, want %v", names, want)
	}
	if !slices.Equal(archive.Files, want) {
		t.Errorf("archive.Files = %v, want %v", archive.Files, want)
	}
}

func TestBuildArchiveOverlaysFilesInOrder(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"metadata.rb":        "name 'nginx'\nversion '1.2.0'\n",
		"recipes/default.rb": "package 'nginx'\n",
		"README.md":          "on disk\n",
	})

	archive := loadArchive(t, dir, false, map[string][]byte{
		"metadata.json": []byte("{}\n"),
		"README.md":     []byte("overlaid\n"),
	})
	entries, names := archiveEntries(t, archive.Bytes)
	want := []string{"nginx/README.md", "nginx/metadata.json", "nginx/metadata.rb", "nginx/recipes/default.rb"}
	if !slices.Equal(names, want) {
		t.Fatalf("archive entries = %v, want %v", names, want)
	}
	if got := string(entries["nginx/README.md"]); got != "overlaid\n" {
		t.Errorf("README.md = %q, want the overlay", got)
	}
	if got := string(entries["nginx/metadata.json"]); got != "{}\n" {
		t.Errorf("metadata.json = %q, want the overlay", got)
	}
}

func TestBuildArchiveIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"metadata.json":      `{"name":"nginx","version":"1.2.0"}`,
		"recipes/default.rb": "package 'nginx'\n",
	})
	first := loadArchive(t, dir, false, nil)
	second := loadArchive(t, dir, false, nil)
	if !bytes.Equal(first.Bytes, second.Bytes) {
		t.Fatal("two archives of the same cookbook differ")
	}
}

func TestLoadRespectsChefignore(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"metadata.json":          `{"name":"nginx","version":"1.0.0"}`,
		"chefignore":             "*.bak\nspec/*\nBerksfile.lock\n",
		"recipes/default.rb":     "package 'nginx'\n",
		"recipes/default.bak":    "old\n",
		"Berksfile.lock":         "lock\n",
		"spec/spec_helper.rb":    "# spec\n",
		"spec/fixtures/foo.json": "{}\n",
	})

	_, names := archiveEntries(t, loadArchive(t, dir, false, nil).Bytes)
	want := []string{"nginx/chefignore", "nginx/metadata.json", "nginx/recipes/default.rb"}
	if !slices.Equal(names, want) {
		t.Fatalf("archive entries = %v, want %v", names, want)
	}
}

func TestLoadSkipChefignoreIncludesEverything(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"metadata.json":       `{"name":"nginx","version":"1.0.0"}`,
		"chefignore":          "*.bak\n",
		"recipes/default.rb":  "package 'nginx'\n",
		"recipes/default.bak": "old\n",
	})

	_, names := archiveEntries(t, loadArchive(t, dir, true, nil).Bytes)
	want := []string{"nginx/chefignore", "nginx/metadata.json", "nginx/recipes/default.bak", "nginx/recipes/default.rb"}
	if !slices.Equal(names, want) {
		t.Fatalf("archive entries = %v, want %v", names, want)
	}
}

// TestLoadNamesUnnamedCookbookAfterItsDirectory loads a cookbook by the
// relative path "." (what Locate returns from inside it): the name is the
// directory's, not ".".
func TestLoadNamesUnnamedCookbookAfterItsDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nginx")
	writeFiles(t, dir, map[string]string{"metadata.rb": "version '1.0.0'\n"})
	t.Chdir(dir)

	cb, err := Load(".", false)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cb.Name != "nginx" {
		t.Errorf("name = %q, want nginx", cb.Name)
	}
}

func TestLoadExplainsAComputedVersion(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"metadata.rb": "name 'nginx'\nversion File.read('VERSION')\n"})

	_, err := Load(dir, false)
	if !errors.Is(err, cinc.ErrMetadataVersionNotLiteral) {
		t.Fatalf("error = %v, want ErrMetadataVersionNotLiteral", err)
	}
	for _, want := range []string{"version '1.2.3'", "metadata.json"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
}
