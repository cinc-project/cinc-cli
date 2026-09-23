package suite

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// cookbookContent prefixes a cookbook file's content with a comment naming the
// cookbook, so no two cases ever upload a file with the same checksum. A Chef
// server stores file contents once per checksum and deletes them when the
// last cookbook referring to them is deleted, so shared content lets one
// case's cleanup delete a file another case's sandbox was told it need not
// upload, and that case's sandbox commit then fails.
func cookbookContent(cookbook, content string) string {
	return "# " + cookbook + "\n" + content
}

// writeCookbookTree writes files (cookbook-relative slash paths to contents) under
// dir and returns dir.
func writeCookbookTree(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	for rel, content := range files {
		writeFile(t, filepath.Join(dir, filepath.FromSlash(rel)), content)
	}
	return dir
}

// simpleCookbookFiles returns the files of a minimal cookbook: a literal
// metadata.rb and a default recipe, both unique to name and version.
func simpleCookbookFiles(name, version string) map[string]string {
	return map[string]string{
		"metadata.rb":        "name '" + name + "'\nversion '" + version + "'\n",
		"recipes/default.rb": cookbookContent(name+" "+version, "package 'nginx'\n"),
	}
}

// uploadTestCookbook writes files as cookbook dirName in a fresh cookbook path,
// uploads it by dirName, registers the delete of name at version, and
// returns the upload's output and the cookbook path.
func uploadTestCookbook(c *cli, dirName, name, version string, files map[string]string) (out, cookbookPath string) {
	c.t.Helper()
	cookbookPath = c.t.TempDir()
	writeCookbookTree(c.t, filepath.Join(cookbookPath, dirName), files)
	c.cleanup("cookbook", "delete", name, version)
	out = c.run("cookbook", "upload", dirName, "--cookbook-path", cookbookPath)
	return out, cookbookPath
}

// showCookbook returns the manifest of name at version ("" for the latest).
func showCookbook(c *cli, name, version string) cinc.Cookbook {
	c.t.Helper()
	args := []string{"cookbook", "show", name}
	if version != "" {
		args = append(args, version)
	}
	var cb cinc.Cookbook
	c.json(&cb, args...)
	return cb
}

// cookbookNames is `cookbook list --format json`.
func cookbookNames(c *cli) []string {
	c.t.Helper()
	var names []string
	c.json(&names, "cookbook", "list")
	return names
}

// manifestPaths returns the sorted paths of every file a manifest lists,
// whichever layout (all_files or per-segment) the server returned.
func manifestPaths(cb cinc.Cookbook) []string {
	var paths []string
	for _, f := range cb.AllFiles() {
		paths = append(paths, f.Path)
	}
	slices.Sort(paths)
	return paths
}

// manifestSegments maps each file's path to the segment the server filed it
// under. The per-segment layout (erchef at API v0/v1) lists the file in the
// segment's slice; all_files (API v2, and cinc-server-ng at any version)
// prefixes the file's name with the segment.
func manifestSegments(cb cinc.Cookbook) map[string]string {
	out := map[string]string{}
	for segment, files := range map[string][]cinc.CookbookFileRef{
		"attributes": cb.Attributes, "definitions": cb.Definitions, "files": cb.Files,
		"libraries": cb.Libraries, "providers": cb.Providers, "recipes": cb.Recipes,
		"resources": cb.Resources, "root_files": cb.RootFiles, "templates": cb.Templates,
	} {
		for _, f := range files {
			out[f.Path] = segment
		}
	}
	for _, f := range cb.AllFilesManifest {
		segment, _, nested := strings.Cut(f.Name, "/")
		if !nested {
			segment = "root_files"
		}
		out[f.Path] = segment
	}
	return out
}

// readCookbookTree returns every regular file under dir as a slash path to its
// content.
func readCookbookTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	return out
}

// wantCookbookTree fails the case unless dir holds exactly want: every file, byte for
// byte, and nothing else.
func wantCookbookTree(t *testing.T, dir string, want map[string]string) {
	t.Helper()
	got := readCookbookTree(t, dir)
	var problems []string
	for _, rel := range slices.Sorted(maps.Keys(want)) {
		g, ok := got[rel]
		switch {
		case !ok:
			problems = append(problems, "missing "+rel)
		case g != want[rel]:
			problems = append(problems, fmt.Sprintf("%s differs: got %d bytes %q, want %d bytes %q",
				rel, len(g), clipContent(g), len(want[rel]), clipContent(want[rel])))
		}
	}
	for _, rel := range slices.Sorted(maps.Keys(got)) {
		if _, ok := want[rel]; !ok {
			problems = append(problems, "unexpected "+rel)
		}
	}
	if len(problems) > 0 {
		t.Fatalf("downloaded cookbook in %s does not match:\n  %s", dir, strings.Join(problems, "\n  "))
	}
}

func clipContent(s string) string {
	if len(s) > 40 {
		return s[:40] + "..."
	}
	return s
}
