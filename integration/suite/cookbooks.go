package suite

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var cookbookFamily = family{cases: []testCase{
	{"cookbooks/lifecycle", []string{"cookbook upload", "cookbook list", "cookbook show", "cookbook delete"}, testCookbookLifecycle},
	{"cookbooks/upload-json", []string{"cookbook upload"}, testCookbookUploadJSON},
	{"cookbooks/round-trip", []string{"cookbook upload", "cookbook download", "cookbook show"}, testCookbookRoundTrip},
	{"cookbooks/symlinked-file", []string{"cookbook upload", "cookbook download"}, testCookbookSymlinkedFile},
	{"cookbooks/empty-file", []string{"cookbook upload", "cookbook download"}, testCookbookEmptyFile},
	{"cookbooks/parent-chefignore", []string{"cookbook upload", "cookbook show"}, testCookbookParentChefignore},
	{"cookbooks/metadata", []string{"cookbook upload", "cookbook show"}, testCookbookMetadata},
	{"cookbooks/metadata-json-only", []string{"cookbook upload", "cookbook show", "cookbook download"}, testCookbookMetadataJSONOnly},
	{"cookbooks/metadata-json-wins", []string{"cookbook upload", "cookbook show"}, testCookbookMetadataJSONWins},
	{"cookbooks/multiline-depends", []string{"cookbook upload", "cookbook show"}, testCookbookMultilineDepends},
	{"cookbooks/short-version", []string{"cookbook upload", "cookbook show"}, testCookbookShortVersion},
	{"cookbooks/name-from-metadata", []string{"cookbook upload", "cookbook list", "cookbook show"}, testCookbookNameFromMetadata},
	{"cookbooks/versions", []string{"cookbook upload", "cookbook show", "cookbook download", "cookbook delete", "cookbook list"}, testCookbookVersions},
	{"cookbooks/reupload", []string{"cookbook upload", "cookbook download"}, testCookbookReupload},
	{"cookbooks/shared-checksum", []string{"cookbook upload", "cookbook delete", "cookbook download"}, testCookbookSharedChecksum},
	{"cookbooks/download-existing-dir", []string{"cookbook download"}, testCookbookDownloadExistingDir},
	{"cookbooks/not-found", []string{"cookbook show", "cookbook delete", "cookbook download"}, testCookbookNotFound},
	{"cookbooks/bad-metadata", []string{"cookbook upload"}, testCookbookBadMetadata},
	{"cookbooks/upload-missing-local", []string{"cookbook upload"}, testCookbookUploadMissingLocal},
	{"cookbooks/forbidden", []string{"cookbook list", "cookbook show", "cookbook upload", "cookbook delete"}, testCookbookForbidden},
	{"cookbooks/default-version", []string{"cookbook upload", "cookbook show"}, testCookbookDefaultVersion},
	{"cookbooks/cookbook-path-list", []string{"cookbook upload"}, testCookbookPathList},
	{"cookbooks/invalid-name", []string{"cookbook upload"}, testCookbookInvalidName},
	{"cookbooks/org-isolation", []string{"cookbook list", "cookbook show", "cookbook delete"}, testCookbookOrgIsolation},
	{"cookbooks/large-file", []string{"cookbook upload", "cookbook download"}, testCookbookLargeFile},
	{"cookbooks/many-files", []string{"cookbook upload", "cookbook download"}, testCookbookManyFiles},
	{"cookbooks/download-new-dir", []string{"cookbook download"}, testCookbookDownloadNewDir},
}}

// testCookbookLifecycle is the basic upload, list, show, delete round trip,
// asserting the human output of each verb.
func testCookbookLifecycle(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "cb")
	out, _ := uploadTestCookbook(c, name, name, "1.2.0", simpleCookbookFiles(name, "1.2.0"))
	wantEqual(t, "upload output", out, fmt.Sprintf("Uploaded cookbook %q version 1.2.0\n", name))

	if list := strings.Fields(c.run("cookbook", "list")); !slices.Contains(list, name) {
		t.Errorf("cookbook list does not include %s: %v", name, list)
	}
	if names := cookbookNames(c); !slices.Contains(names, name) {
		t.Errorf("cookbook list --format json does not include %s: %v", name, names)
	}

	cb := showCookbook(c, name, "1.2.0")
	wantEqual(t, "cookbook_name", cb.CookbookName, name)
	wantEqual(t, "version", cb.Version, "1.2.0")
	wantEqual(t, "name", cb.Name, name+"-1.2.0")
	// erchef requires the metadata block on upload and serves it back.
	wantEqual(t, "metadata.name", cb.Metadata.Name, name)
	wantEqual(t, "metadata.version", cb.Metadata.Version, "1.2.0")
	wantSlice(t, "files", manifestPaths(cb), []string{"metadata.rb", "recipes/default.rb"})
	wantEqual(t, "latest version", showCookbook(c, name, "").Version, "1.2.0")

	// The human form of show is the manifest itself.
	if human := c.run("cookbook", "show", name, "1.2.0"); !strings.Contains(human, `"cookbook_name": "`+name+`"`) {
		t.Errorf("cookbook show (human) does not name the cookbook:\n%s", human)
	}

	wantEqual(t, "delete output", c.run("cookbook", "delete", name, "1.2.0"),
		fmt.Sprintf("Deleted cookbook %q version 1.2.0\n", name))
	wantNotFound(t, c.fail("cookbook", "show", name, "1.2.0"))
	wantNotFound(t, c.fail("cookbook", "show", name))
	if names := cookbookNames(c); slices.Contains(names, name) {
		t.Errorf("cookbook list still includes deleted %s", name)
	}
}

// testCookbookUploadJSON uploads two cookbooks in one command and checks the
// JSON result lists both.
func testCookbookUploadJSON(t *testing.T, _ Target, c *cli) {
	a, b := uniqueName(t, "cb"), uniqueName(t, "cb")
	path := t.TempDir()
	writeCookbookTree(t, filepath.Join(path, a), simpleCookbookFiles(a, "0.1.0"))
	writeCookbookTree(t, filepath.Join(path, b), simpleCookbookFiles(b, "0.2.0"))
	c.cleanup("cookbook", "delete", a, "0.1.0")
	c.cleanup("cookbook", "delete", b, "0.2.0")

	var results []struct {
		Cookbook string `json:"cookbook"`
		Version  string `json:"version"`
		Uploaded bool   `json:"uploaded"`
	}
	c.json(&results, "cookbook", "upload", a, b, "--cookbook-path", path)
	if len(results) != 2 ||
		results[0].Cookbook != a || results[0].Version != "0.1.0" || !results[0].Uploaded ||
		results[1].Cookbook != b || results[1].Version != "0.2.0" || !results[1].Uploaded {
		t.Fatalf("upload --format json = %+v, want %s 0.1.0 and %s 0.2.0 uploaded", results, a, b)
	}
	wantEqual(t, a+" version", showCookbook(c, a, "").Version, "0.1.0")
	wantEqual(t, b+" version", showCookbook(c, b, "").Version, "0.2.0")
}

// binaryCookbookContent is every byte value, prefixed so its checksum is unique to
// the cookbook (see cookbookContent).
func binaryCookbookContent(cookbook string) string {
	b := []byte(cookbook + "\x00")
	for i := range 256 {
		b = append(b, byte(i), byte(255-i))
	}
	return string(b)
}

// testCookbookRoundTrip uploads a cookbook using every kind of file Chef's
// cookbook loader knows about, downloads it into a fresh directory, and
// checks the result byte for byte: every file that belongs in the cookbook
// is there, and nothing Chef skips (root dot-directories, chefignored files,
// chef-zero's marker) was sent.
func testCookbookRoundTrip(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "cb")
	u := func(s string) string { return cookbookContent(name, s) }
	kept := map[string]string{
		"metadata.rb":                               "name '" + name + "'\nversion '3.1.4'\ndepends 'apt'\n",
		"README.md":                                 u("# The cookbook\n"),
		"chefignore":                                u("*.bak\nspec/*\n"),
		".rubocop.yml":                              u("AllCops: {}\n"),
		"recipes/default.rb":                        u("include_recipe '" + name + "::web'\n"),
		"recipes/web.rb":                            u("package 'nginx'\n"),
		"attributes/default.rb":                     u("default['port'] = 80\n"),
		"libraries/helpers.rb":                      u("module Helpers; end\n"),
		"resources/site.rb":                         u("property :name, String\n"),
		"templates/nginx.conf.erb":                  u("listen <%= @port %>;\n"),
		"templates/default/site.erb":                u("server_name <%= @name %>;\n"),
		"templates/ubuntu-22.04/site.erb":           u("# ubuntu\n"),
		"files/default/motd":                        u("hello\n"),
		"files/default/nested/deep/down/config.ini": u("[a]\nb = c\n"),
		"files/default/.hidden/secret":              u("dot-directories below the root are kept\n"),
		"files/default/logo.bin":                    binaryCookbookContent(name),
		"files/default/crlf.txt":                    u("line one\r\nline two\r\n"),
		"files/default/no-newline":                  u("no trailing newline"),
		"files/default/spaces in name.txt":          u("spaces\n"),
		"files/default/unicode-éè.txt":              u("café ☃\n"),
		// chefignore patterns match the way Ruby's File.fnmatch does without
		// FNM_DOTMATCH, so "*.bak" does not match a name starting with a dot.
		".kitchen.yml.bak": u("a leading dot is not matched by *\n"),
		// Only chef-zero's exact marker name is skipped.
		".uploaded-cookbook-version": u("not the chef-zero marker\n"),
	}
	skipped := map[string]string{
		".git/config":                     u("[core]\n"),
		".kitchen/default.yml":            u("state: 1\n"),
		"recipes/old.rb.bak":              u("chefignored\n"),
		"spec/default_spec.rb":            u("chefignored\n"),
		".uploaded-cookbook-version.json": u("{}\n"),
	}

	path := t.TempDir()
	dir := filepath.Join(path, name)
	writeCookbookTree(t, dir, kept)
	writeCookbookTree(t, dir, skipped)
	c.cleanup("cookbook", "delete", name, "3.1.4")
	c.run("cookbook", "upload", name, "--cookbook-path", path)

	cb := showCookbook(c, name, "3.1.4")
	var want []string
	for rel := range kept {
		want = append(want, rel)
	}
	slices.Sort(want)
	wantSlice(t, "manifest files", manifestPaths(cb), want)
	for _, f := range cb.AllFiles() {
		if f.Checksum == "" || f.URL == "" {
			t.Errorf("manifest entry %s has checksum %q and url %q, want both", f.Path, f.Checksum, f.URL)
		}
	}
	// Each file is filed under the segment chef-client looks in.
	segments := manifestSegments(cb)
	for path, segment := range map[string]string{
		"metadata.rb":                               "root_files",
		"README.md":                                 "root_files",
		"recipes/web.rb":                            "recipes",
		"attributes/default.rb":                     "attributes",
		"libraries/helpers.rb":                      "libraries",
		"resources/site.rb":                         "resources",
		"templates/nginx.conf.erb":                  "templates",
		"templates/default/site.erb":                "templates",
		"files/default/nested/deep/down/config.ini": "files",
	} {
		if segments[path] != segment {
			t.Errorf("%s is in segment %q, want %q", path, segments[path], segment)
		}
	}

	dest := t.TempDir()
	out := c.run("cookbook", "download", name, "3.1.4", "--dir", dest)
	cbDir := filepath.Join(dest, name+"-3.1.4")
	wantEqual(t, "download output", out, fmt.Sprintf("Downloaded cookbook %q version 3.1.4 to %s\n", name, cbDir))
	wantCookbookTree(t, cbDir, kept)
	if entries, err := os.ReadDir(dest); err != nil || len(entries) != 1 {
		t.Errorf("download wrote %v (err %v) into --dir, want only %s-3.1.4", entries, err, name)
	}
}

// testCookbookSymlinkedFile links one template to another, as cookbooks
// that share a file between platforms do. Chef's cookbook loader keeps any
// path that is not a directory, so knife uploads the link as a file holding
// its target's content, and chef-client finds the template.
func testCookbookSymlinkedFile(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "cb")
	files := simpleCookbookFiles(name, "1.0.0")
	files["templates/default/site.erb"] = cookbookContent(name, "server_name <%= @name %>;\n")
	path := t.TempDir()
	dir := filepath.Join(path, name)
	writeCookbookTree(t, dir, files)
	if err := os.MkdirAll(filepath.Join(dir, "templates", "ubuntu"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "default", "site.erb"), filepath.Join(dir, "templates", "ubuntu", "site.erb")); err != nil {
		t.Fatal(err)
	}
	c.cleanup("cookbook", "delete", name, "1.0.0")
	c.run("cookbook", "upload", name, "--cookbook-path", path)

	files["templates/ubuntu/site.erb"] = files["templates/default/site.erb"]
	dest := t.TempDir()
	c.run("cookbook", "download", name, "1.0.0", "--dir", dest)
	wantCookbookTree(t, filepath.Join(dest, name+"-1.0.0"), files)
}

// testCookbookEmptyFile uploads a cookbook holding an empty file. It is the
// only case in the suite with one: every empty file has the same checksum,
// so two cases sharing it could break each other (see cookbookContent).
func testCookbookEmptyFile(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "cb")
	files := simpleCookbookFiles(name, "0.0.1")
	files["files/default/empty"] = ""
	uploadTestCookbook(c, name, name, "0.0.1", files)

	dest := t.TempDir()
	c.run("cookbook", "download", name, "0.0.1", "--dir", dest)
	wantCookbookTree(t, filepath.Join(dest, name+"-0.0.1"), files)
}

// testCookbookParentChefignore puts the chefignore in the cookbook path, not
// the cookbook, as a chef-repo's cookbooks/chefignore is: Chef applies it to
// every cookbook below.
func testCookbookParentChefignore(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "cb")
	path := t.TempDir()
	writeFile(t, filepath.Join(path, "chefignore"), "*.swp\ntest/*\n")
	files := simpleCookbookFiles(name, "1.0.0")
	files["recipes/.default.rb.swp"] = cookbookContent(name, "swap\n")
	files["test/integration/default_test.rb"] = cookbookContent(name, "describe 1\n")
	writeCookbookTree(t, filepath.Join(path, name), files)
	c.cleanup("cookbook", "delete", name, "1.0.0")
	c.run("cookbook", "upload", name, "--cookbook-path", path)

	wantSlice(t, "manifest files", manifestPaths(showCookbook(c, name, "1.0.0")),
		[]string{"metadata.rb", "recipes/default.rb"})
}

// testCookbookMetadata checks that what metadata.rb declares reaches the
// server's metadata block, which chef-client and the dependency solver read.
func testCookbookMetadata(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "cb")
	files := simpleCookbookFiles(name, "2.1.0")
	files["metadata.rb"] = strings.Join([]string{
		"name '" + name + "'",
		"maintainer 'Acme Infra'",
		"maintainer_email 'infra@acme.test'",
		"license 'Apache-2.0'",
		"description 'Installs and configures the acme web server'",
		"version '2.1.0'",
		"source_url 'https://github.com/acme/webserver'",
		"issues_url 'https://github.com/acme/webserver/issues'",
		"chef_version '>= 16.0'",
		"supports 'ubuntu', '>= 20.04'",
		"supports 'debian'",
		"depends 'apt', '>= 7.0'",
		"depends 'build-essential'",
		"depends 'logrotate', '~> 2.0'",
		"",
	}, "\n")
	uploadTestCookbook(c, name, name, "2.1.0", files)

	md := showCookbook(c, name, "2.1.0").Metadata
	for _, f := range []struct{ field, got, want string }{
		{"name", md.Name, name},
		{"version", md.Version, "2.1.0"},
		{"description", md.Description, "Installs and configures the acme web server"},
		{"maintainer", md.Maintainer, "Acme Infra"},
		{"maintainer_email", md.MaintainerEmail, "infra@acme.test"},
		{"license", md.License, "Apache-2.0"},
		{"source_url", md.SourceURL, "https://github.com/acme/webserver"},
		{"issues_url", md.IssuesURL, "https://github.com/acme/webserver/issues"},
		{"dependencies[apt]", md.Dependencies["apt"], ">= 7.0"},
		{"dependencies[build-essential]", md.Dependencies["build-essential"], ">= 0.0.0"},
		{"dependencies[logrotate]", md.Dependencies["logrotate"], "~> 2.0"},
		{"platforms[ubuntu]", md.Platforms["ubuntu"], ">= 20.04"},
		{"platforms[debian]", md.Platforms["debian"], ">= 0.0.0"},
	} {
		if f.got != f.want {
			t.Errorf("metadata.%s = %q, want %q", f.field, f.got, f.want)
		}
	}
	if len(md.ChefVersions) != 1 || !slices.Equal(md.ChefVersions[0], []string{">= 16.0"}) {
		t.Errorf("metadata.chef_versions = %v, want [[>= 16.0]]", md.ChefVersions)
	}
}

// testCookbookMetadataJSONOnly uploads a cookbook that has a compiled
// metadata.json and no metadata.rb, as Berkshelf and Supermarket vendor them.
func testCookbookMetadataJSONOnly(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "cb")
	mdJSON, err := json.Marshal(map[string]any{
		"name":         name,
		"version":      "4.0.0",
		"description":  "vendored",
		"dependencies": map[string]any{"apt": ">= 7.0", "yum": []string{"~> 5.0"}},
		"platforms":    map[string]any{"centos": ">= 8.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"metadata.json":      string(mdJSON),
		"recipes/default.rb": cookbookContent(name, "package 'nginx'\n"),
	}
	out, _ := uploadTestCookbook(c, name, name, "4.0.0", files)
	wantEqual(t, "upload output", out, fmt.Sprintf("Uploaded cookbook %q version 4.0.0\n", name))

	md := showCookbook(c, name, "4.0.0").Metadata
	wantEqual(t, "description", md.Description, "vendored")
	wantEqual(t, "dependencies[apt]", md.Dependencies["apt"], ">= 7.0")
	// A legacy one-element constraint array is unwrapped, as Chef does.
	wantEqual(t, "dependencies[yum]", md.Dependencies["yum"], "~> 5.0")
	wantEqual(t, "platforms[centos]", md.Platforms["centos"], ">= 8.0")

	dest := t.TempDir()
	c.run("cookbook", "download", name, "--dir", dest)
	wantCookbookTree(t, filepath.Join(dest, name+"-4.0.0"), files)
}

// testCookbookMetadataJSONWins gives a cookbook both files, disagreeing:
// like Chef's loader, metadata.json is the one that counts.
func testCookbookMetadataJSONWins(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "cb")
	files := map[string]string{
		"metadata.rb":        "name '" + name + "'\nversion '1.0.0'\ndepends 'from-rb'\n",
		"metadata.json":      `{"name":"` + name + `","version":"1.5.0","dependencies":{"from-json":">= 1.0.0"}}`,
		"recipes/default.rb": cookbookContent(name, "package 'nginx'\n"),
	}
	out, _ := uploadTestCookbook(c, name, name, "1.5.0", files)
	c.cleanup("cookbook", "delete", name, "1.0.0")
	wantEqual(t, "upload output", out, fmt.Sprintf("Uploaded cookbook %q version 1.5.0\n", name))

	md := showCookbook(c, name, "").Metadata
	wantEqual(t, "version", md.Version, "1.5.0")
	if _, ok := md.Dependencies["from-json"]; !ok {
		t.Errorf("metadata.dependencies = %v, want from-json", md.Dependencies)
	}
	if _, ok := md.Dependencies["from-rb"]; ok {
		t.Errorf("metadata.dependencies = %v, want no from-rb (metadata.json wins)", md.Dependencies)
	}
}

// testCookbookMultilineDepends writes dependencies the way many real
// cookbooks do, one call spread over several lines. Chef evaluates
// metadata.rb as Ruby, so each reaches the server. A dependency that is
// silently dropped on upload is never pulled in by the dependency solver,
// and the node's converge fails far from the cause.
func testCookbookMultilineDepends(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "cb")
	files := simpleCookbookFiles(name, "1.0.0")
	files["metadata.rb"] = "name '" + name + "'\nversion '1.0.0'\n" +
		"depends 'apt',\n        '>= 7.0'\n" +
		"depends(\n  'yum',\n  '~> 5.0'\n)\n" +
		"depends 'plain', '= 1.0.0'\n"
	uploadTestCookbook(c, name, name, "1.0.0", files)

	deps := showCookbook(c, name, "1.0.0").Metadata.Dependencies
	for dep, want := range map[string]string{"apt": ">= 7.0", "yum": "~> 5.0", "plain": "= 1.0.0"} {
		if deps[dep] != want {
			t.Errorf("metadata.dependencies[%s] = %q, want %q (all: %v)", dep, deps[dep], want, deps)
		}
	}
}

// testCookbookShortVersion declares a two-part version. Chef normalizes
// "1.2" to "1.2.0", and knife uploads it as that.
func testCookbookShortVersion(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "cb")
	files := simpleCookbookFiles(name, "1.2")
	out, _ := uploadTestCookbook(c, name, name, "1.2.0", files)
	wantEqual(t, "upload output", out, fmt.Sprintf("Uploaded cookbook %q version 1.2.0\n", name))
	wantEqual(t, "version", showCookbook(c, name, "1.2.0").Version, "1.2.0")
}

// testCookbookNameFromMetadata keeps a cookbook in a directory whose name
// differs from the name its metadata declares. As with knife, the cookbook
// is uploaded under its metadata name, and the output must say so.
func testCookbookNameFromMetadata(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "cb")
	dirName := "chef-" + name
	out, path := uploadTestCookbook(c, dirName, name, "1.0.0", simpleCookbookFiles(name, "1.0.0"))
	c.cleanup("cookbook", "delete", dirName, "1.0.0")
	if want := fmt.Sprintf("Uploaded cookbook %q version 1.0.0\n", name); out != want {
		t.Errorf("upload output = %q, want %q", out, want)
	}

	names := cookbookNames(c)
	if !slices.Contains(names, name) {
		t.Errorf("cookbook list = %v, want the metadata name %s", names, name)
	}
	if slices.Contains(names, dirName) {
		t.Errorf("cookbook list = %v, want no cookbook named after the directory %s", names, dirName)
	}
	wantEqual(t, "cookbook_name", showCookbook(c, name, "1.0.0").CookbookName, name)

	// Naming the cookbook directory itself as the cookbook path, by its
	// metadata name, finds the same cookbook.
	out = c.run("cookbook", "upload", name, "--cookbook-path", filepath.Join(path, dirName))
	wantEqual(t, "upload by metadata name", out, fmt.Sprintf("Uploaded cookbook %q version 1.0.0\n", name))
}

// testCookbookVersions keeps several versions of one cookbook and checks
// that latest follows version order (1.10.0 beats 1.2.0), that each version
// is addressed on its own, and that deleting one leaves the others.
func testCookbookVersions(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "cb")
	path := t.TempDir()
	dir := filepath.Join(path, name)
	for _, v := range []string{"1.2.0", "1.10.0", "0.9.0"} {
		// Upload each version from the same directory, rewriting it, as a
		// developer bumping a cookbook does.
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		writeCookbookTree(t, dir, simpleCookbookFiles(name, v))
		c.cleanup("cookbook", "delete", name, v)
		c.run("cookbook", "upload", name, "--cookbook-path", path)
	}

	for _, v := range []string{"0.9.0", "1.2.0", "1.10.0"} {
		cb := showCookbook(c, name, v)
		wantEqual(t, "version", cb.Version, v)
		wantEqual(t, "metadata.version", cb.Metadata.Version, v)
	}
	wantEqual(t, "latest", showCookbook(c, name, "").Version, "1.10.0")
	wantEqual(t, "_latest", showCookbook(c, name, "_latest").Version, "1.10.0")

	dest := t.TempDir()
	out := c.run("cookbook", "download", name, "--dir", dest)
	if !strings.Contains(out, "version 1.10.0") {
		t.Errorf("download of latest = %q, want version 1.10.0", out)
	}
	wantCookbookTree(t, filepath.Join(dest, name+"-1.10.0"), simpleCookbookFiles(name, "1.10.0"))
	c.run("cookbook", "download", name, "0.9.0", "--dir", dest)
	wantCookbookTree(t, filepath.Join(dest, name+"-0.9.0"), simpleCookbookFiles(name, "0.9.0"))

	c.run("cookbook", "delete", name, "1.10.0")
	wantNotFound(t, c.fail("cookbook", "show", name, "1.10.0"))
	wantEqual(t, "latest after delete", showCookbook(c, name, "").Version, "1.2.0")
	// Deleting a version that is already gone is a 404, not a no-op.
	wantNotFound(t, c.fail("cookbook", "delete", name, "1.10.0"))
	if !slices.Contains(cookbookNames(c), name) {
		t.Errorf("cookbook list lost %s while versions remain", name)
	}

	c.run("cookbook", "delete", name, "1.2.0")
	c.run("cookbook", "delete", name, "0.9.0")
	wantNotFound(t, c.fail("cookbook", "show", name))
	if slices.Contains(cookbookNames(c), name) {
		t.Errorf("cookbook list still includes %s after its last version was deleted", name)
	}
}

// testCookbookReupload uploads the same version twice with a changed file.
// A version that is not frozen may be replaced, so the server serves the
// new content, and a file dropped from the cookbook is gone.
func testCookbookReupload(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "cb")
	files := simpleCookbookFiles(name, "1.0.0")
	files["recipes/extra.rb"] = cookbookContent(name, "# dropped on re-upload\n")
	_, path := uploadTestCookbook(c, name, name, "1.0.0", files)

	dir := filepath.Join(path, name)
	if err := os.Remove(filepath.Join(dir, "recipes", "extra.rb")); err != nil {
		t.Fatal(err)
	}
	delete(files, "recipes/extra.rb")
	files["recipes/default.rb"] = cookbookContent(name, "package 'apache2'\n")
	writeCookbookTree(t, dir, files)
	c.run("cookbook", "upload", name, "--cookbook-path", path)

	dest := t.TempDir()
	c.run("cookbook", "download", name, "1.0.0", "--dir", dest)
	wantCookbookTree(t, filepath.Join(dest, name+"-1.0.0"), files)
}

// testCookbookSharedChecksum uploads two cookbooks that share a file's
// content. The server stores it once; deleting one cookbook must leave the
// content in place for the other.
func testCookbookSharedChecksum(t *testing.T, _ Target, c *cli) {
	a, b := uniqueName(t, "cb"), uniqueName(t, "cb")
	shared := cookbookContent(a+" "+b, "shared between two cookbooks\n")
	filesA := simpleCookbookFiles(a, "1.0.0")
	filesA["files/default/shared.txt"] = shared
	filesB := simpleCookbookFiles(b, "1.0.0")
	filesB["files/default/shared.txt"] = shared
	filesB["templates/shared.erb"] = shared // and twice within one cookbook
	uploadTestCookbook(c, a, a, "1.0.0", filesA)
	uploadTestCookbook(c, b, b, "1.0.0", filesB)

	c.run("cookbook", "delete", a, "1.0.0")
	dest := t.TempDir()
	c.run("cookbook", "download", b, "1.0.0", "--dir", dest)
	wantCookbookTree(t, filepath.Join(dest, b+"-1.0.0"), filesB)
}

// testCookbookDownloadExistingDir downloads into a directory that already
// holds the cookbook, with one file changed and one extra: the changed file
// is restored and the extra left alone.
func testCookbookDownloadExistingDir(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "cb")
	files := simpleCookbookFiles(name, "1.0.0")
	uploadTestCookbook(c, name, name, "1.0.0", files)

	dest := t.TempDir()
	c.run("cookbook", "download", name, "--dir", dest)
	cbDir := filepath.Join(dest, name+"-1.0.0")
	writeFile(t, filepath.Join(cbDir, "recipes", "default.rb"), "locally edited\n")
	writeFile(t, filepath.Join(cbDir, "local-only.txt"), "mine\n")
	c.run("cookbook", "download", name, "--dir", dest)

	want := map[string]string{"local-only.txt": "mine\n"}
	for k, v := range files {
		want[k] = v
	}
	wantCookbookTree(t, cbDir, want)
}

func testCookbookNotFound(t *testing.T, _ Target, c *cli) {
	ghost := uniqueName(t, "ghost")
	wantNotFound(t, c.fail("cookbook", "show", ghost))
	wantNotFound(t, c.fail("cookbook", "show", ghost, "1.0.0"))
	wantNotFound(t, c.fail("cookbook", "delete", ghost, "0.0.1"))
	dest := t.TempDir()
	wantNotFound(t, c.fail("cookbook", "download", ghost, "--dir", dest))
	wantNotFound(t, c.fail("cookbook", "download", ghost, "1.0.0", "--dir", dest))
	if entries, _ := os.ReadDir(dest); len(entries) != 0 {
		t.Errorf("a failed download left %v in --dir", entries)
	}

	// A cookbook that exists, at a version that does not.
	name := uniqueName(t, "cb")
	uploadTestCookbook(c, name, name, "1.0.0", simpleCookbookFiles(name, "1.0.0"))
	wantNotFound(t, c.fail("cookbook", "show", name, "9.9.9"))
	wantNotFound(t, c.fail("cookbook", "delete", name, "9.9.9"))
	wantNotFound(t, c.fail("cookbook", "download", name, "9.9.9", "--dir", dest))
	wantEqual(t, "surviving version", showCookbook(c, name, "").Version, "1.0.0")
}

// testCookbookBadMetadata uploads cookbooks whose metadata Chef rejects; each
// must fail with a message naming the problem and leave nothing on the
// server.
func testCookbookBadMetadata(t *testing.T, _ Target, c *cli) {
	for _, tc := range []struct {
		what, metadata, want string
	}{
		{"malformed version", "version 'one.two'\n", "version"},
		{"bad constraint", "version '1.0.0'\ndepends 'apt', 'soon'\n", "constraint"},
		{"depends on itself", "version '1.0.0'\ndepends '%s'\n", "itself"},
	} {
		name := uniqueName(t, "cb")
		path := t.TempDir()
		md := "name '" + name + "'\n" + strings.ReplaceAll(tc.metadata, "%s", name)
		writeCookbookTree(t, filepath.Join(path, name), map[string]string{
			"metadata.rb":        md,
			"recipes/default.rb": cookbookContent(name, "package 'nginx'\n"),
		})
		c.cleanup("cookbook", "delete", name, "1.0.0")
		r := c.fail("cookbook", "upload", name, "--cookbook-path", path)
		if !strings.Contains(strings.ToLower(r.stderr), tc.want) {
			t.Errorf("%s: upload error does not mention %q: %s", tc.what, tc.want, r)
		}
		if slices.Contains(cookbookNames(c), name) {
			t.Errorf("%s: a rejected upload left %s on the server", tc.what, name)
		}
	}
}

func testCookbookUploadMissingLocal(t *testing.T, _ Target, c *cli) {
	ghost := uniqueName(t, "ghost")
	path := t.TempDir()
	r := c.fail("cookbook", "upload", ghost, "--cookbook-path", path)
	if !strings.Contains(r.stderr, ghost) || !strings.Contains(r.stderr, "not found") {
		t.Errorf("uploading a cookbook that is not on disk should name it and say not found: %s", r)
	}
}

// testCookbookForbidden acts as a plain client of the organization. Chef
// Server gives the clients group read on the cookbooks container and nothing
// else, so the client can read cookbooks but not upload or delete them.
func testCookbookForbidden(t *testing.T, tgt Target, c *cli) {
	name := uniqueName(t, "cb")
	uploadTestCookbook(c, name, name, "1.0.0", simpleCookbookFiles(name, "1.0.0"))

	client := uniqueName(t, "client")
	key := filepath.Join(t.TempDir(), "client.pem")
	c.run("client", "create", client, "--key-file", key)
	c.cleanup("client", "delete", client)
	c.addProfile("client", tgt.Org, client, key)

	if names := cookbookNames(c); !slices.Contains(names, name) {
		t.Fatalf("admin cannot see %s: %v", name, names)
	}
	var names []string
	c.json(&names, "cookbook", "list", "--profile", "client")
	if !slices.Contains(names, name) {
		t.Errorf("client's cookbook list = %v, want %s", names, name)
	}
	c.run("cookbook", "show", name, "1.0.0", "--profile", "client")

	other := uniqueName(t, "cb")
	path := t.TempDir()
	writeCookbookTree(t, filepath.Join(path, other), simpleCookbookFiles(other, "1.0.0"))
	c.cleanup("cookbook", "delete", other, "1.0.0")
	wantCookbookForbidden(t, c.fail("cookbook", "upload", other, "--cookbook-path", path, "--profile", "client"))
	wantCookbookForbidden(t, c.fail("cookbook", "delete", name, "1.0.0", "--profile", "client"))
	wantEqual(t, "version after refused delete", showCookbook(c, name, "").Version, "1.0.0")
}

// testCookbookDefaultVersion declares no version: Chef's default is 0.0.0.
func testCookbookDefaultVersion(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "cb")
	files := map[string]string{
		"metadata.rb":        "name '" + name + "'\n",
		"recipes/default.rb": cookbookContent(name, "package 'nginx'\n"),
	}
	out, _ := uploadTestCookbook(c, name, name, "0.0.0", files)
	wantEqual(t, "upload output", out, fmt.Sprintf("Uploaded cookbook %q version 0.0.0\n", name))
	wantEqual(t, "version", showCookbook(c, name, "").Version, "0.0.0")
}

// testCookbookPathList searches a list of cookbook directories, as knife's
// cookbook_path does, finding each cookbook in whichever holds it.
func testCookbookPathList(t *testing.T, _ Target, c *cli) {
	a, b := uniqueName(t, "cb"), uniqueName(t, "cb")
	first, second := t.TempDir(), t.TempDir()
	writeCookbookTree(t, filepath.Join(first, a), simpleCookbookFiles(a, "1.0.0"))
	writeCookbookTree(t, filepath.Join(second, b), simpleCookbookFiles(b, "1.0.0"))
	c.cleanup("cookbook", "delete", a, "1.0.0")
	c.cleanup("cookbook", "delete", b, "1.0.0")
	c.run("cookbook", "upload", a, b, "--cookbook-path", first+string(os.PathListSeparator)+second)
	names := cookbookNames(c)
	if !slices.Contains(names, a) || !slices.Contains(names, b) {
		t.Errorf("cookbook list = %v, want both %s and %s", names, a, b)
	}
}

// testCookbookInvalidName declares a name Chef Server's cookbook-name rule
// (letters, digits, '.', '_' and '-') refuses. The upload must fail and leave
// nothing behind.
func testCookbookInvalidName(t *testing.T, _ Target, c *cli) {
	dirName := uniqueName(t, "cb")
	// '@' is outside the rule but needs no escaping in a URL path.
	bad := dirName + "@bad"
	path := t.TempDir()
	writeCookbookTree(t, filepath.Join(path, dirName), map[string]string{
		"metadata.rb":        "name '" + bad + "'\nversion '1.0.0'\n",
		"recipes/default.rb": cookbookContent(dirName, "package 'nginx'\n"),
	})
	c.cleanup("cookbook", "delete", bad, "1.0.0")
	c.fail("cookbook", "upload", dirName, "--cookbook-path", path)
	if names := cookbookNames(c); slices.Contains(names, bad) {
		t.Errorf("cookbook list = %v, want no %q", names, bad)
	}
}

// testCookbookOrgIsolation uploads to one organization and checks the other
// neither lists nor serves it, and cannot delete it.
func testCookbookOrgIsolation(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "cb")
	uploadTestCookbook(c, name, name, "1.0.0", simpleCookbookFiles(name, "1.0.0"))
	var other []string
	c.json(&other, "cookbook", "list", "--profile", "other")
	if slices.Contains(other, name) {
		t.Errorf("the other organization lists %s", name)
	}
	wantNotFound(t, c.fail("cookbook", "show", name, "--profile", "other"))
	wantNotFound(t, c.fail("cookbook", "delete", name, "1.0.0", "--profile", "other"))
	wantEqual(t, "version in its own organization", showCookbook(c, name, "").Version, "1.0.0")
}

// testCookbookLargeFile round-trips a file of several megabytes, larger than
// any buffer in the upload or download path.
func testCookbookLargeFile(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "cb")
	var b strings.Builder
	b.WriteString(cookbookContent(name, ""))
	for i := 0; b.Len() < 6<<20; i++ {
		fmt.Fprintf(&b, "%08d %s\n", i, strings.Repeat("x", 90))
	}
	files := simpleCookbookFiles(name, "1.0.0")
	files["files/default/large.txt"] = b.String()
	uploadTestCookbook(c, name, name, "1.0.0", files)
	dest := t.TempDir()
	c.run("cookbook", "download", name, "--dir", dest)
	wantCookbookTree(t, filepath.Join(dest, name+"-1.0.0"), files)
}

// testCookbookManyFiles round-trips a cookbook with more files than the
// client transfers at once, so uploads and downloads run in parallel.
func testCookbookManyFiles(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "cb")
	files := simpleCookbookFiles(name, "1.0.0")
	for i := range 150 {
		files[fmt.Sprintf("files/default/set%d/file%03d.txt", i%5, i)] = cookbookContent(name, fmt.Sprintf("file %d\n", i))
	}
	uploadTestCookbook(c, name, name, "1.0.0", files)
	wantEqual(t, "manifest files", len(manifestPaths(showCookbook(c, name, "1.0.0"))), len(files))
	dest := t.TempDir()
	c.run("cookbook", "download", name, "--dir", dest)
	wantCookbookTree(t, filepath.Join(dest, name+"-1.0.0"), files)
}

// testCookbookDownloadNewDir downloads into a --dir that does not exist yet,
// several levels deep.
func testCookbookDownloadNewDir(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "cb")
	files := simpleCookbookFiles(name, "1.0.0")
	uploadTestCookbook(c, name, name, "1.0.0", files)
	dest := filepath.Join(t.TempDir(), "not", "there", "yet")
	c.run("cookbook", "download", name, "--dir", dest)
	wantCookbookTree(t, filepath.Join(dest, name+"-1.0.0"), files)
}

// wantCookbookForbidden fails the case unless r is the CLI's permission error.
func wantCookbookForbidden(t *testing.T, r result) {
	t.Helper()
	low := strings.ToLower(r.stderr)
	denied := strings.Contains(low, "403") || strings.Contains(low, "permission") ||
		strings.Contains(low, "forbidden") || strings.Contains(low, "not allowed")
	if r.exitCode == 0 || !denied {
		t.Fatalf("want a permission error: %s", r)
	}
}
