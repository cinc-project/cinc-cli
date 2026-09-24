package suite

import (
	"context"
	"crypto/md5"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	cinc "github.com/cinc-project/cinc-api"

	"github.com/cinc-project/cinc-cli/cli/policyfile/rubyeval"
)

// adminAPI is a cinc-api client signing as the target's admin in org. The
// CLI has no command for cookbook artifacts, so the policy cases use it to
// check that a push stored the artifacts it should have, and to delete them
// afterwards so a long-lived server does not accumulate them. Every request
// the case is testing still goes through the binary.
func adminAPI(t *testing.T, tgt Target, org string) *cinc.Client {
	t.Helper()
	key, err := cinc.LoadKeyFile(tgt.KeyPath)
	if err != nil {
		t.Fatalf("load admin key: %v", err)
	}
	var opts []cinc.Option
	if tgt.CACertPath != "" {
		pem, err := os.ReadFile(tgt.CACertPath)
		if err != nil {
			t.Fatalf("read CA certificate: %v", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			t.Fatalf("no certificate in %s", tgt.CACertPath)
		}
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
		opts = append(opts, cinc.WithHTTPClient(&http.Client{Transport: tr}))
	}
	c, err := cinc.NewClient(cinc.Config{
		ServerURL: tgt.ServerURL, Org: org, ClientName: tgt.Admin, Key: key,
	}, opts...)
	if err != nil {
		t.Fatalf("build admin API client: %v", err)
	}
	return c
}

// artifactIdentifiers returns the identifiers the server holds for the
// cookbook artifact name, or nil when it has none.
func artifactIdentifiers(t *testing.T, api *cinc.Client, name string) []string {
	t.Helper()
	entry, _, err := api.CookbookArtifacts.GetVersions(context.Background(), name)
	if errors.Is(err, cinc.ErrNotFound) {
		return nil
	}
	if err != nil {
		t.Fatalf("list cookbook artifact %s: %v", name, err)
	}
	ids := make([]string, 0, len(entry.Versions))
	for _, v := range entry.Versions {
		ids = append(ids, v.Identifier)
	}
	return ids
}

// cleanupArtifacts deletes, when the case ends, every cookbook artifact
// stored under each name. Cookbook names are unique per case, so this never
// touches another case's artifacts. Register it before the policy cleanup:
// cleanups run last-registered first, so the policy is gone by then.
func cleanupArtifacts(c *cli, api *cinc.Client, names ...string) {
	c.t.Helper()
	c.t.Cleanup(func() {
		for _, name := range names {
			for _, id := range artifactIdentifiers(c.t, api, name) {
				if _, err := api.CookbookArtifacts.Delete(context.Background(), name, id); err != nil && !errors.Is(err, cinc.ErrNotFound) {
					c.t.Errorf("cleanup: delete cookbook artifact %s@%s: %v", name, id, err)
				}
			}
		}
	})
}

// wantArtifact fails the case unless the server stores cookbook artifact
// name under identifier, as that cookbook (not a cache directory's name) at
// version, with a default recipe whose content is recipe.
func wantArtifact(t *testing.T, api *cinc.Client, name, identifier, version, recipe string) {
	t.Helper()
	cb, _, err := api.CookbookArtifacts.Get(context.Background(), name, identifier)
	if err != nil {
		t.Fatalf("cookbook artifact %s@%s is not on the server: %v (have %v)",
			name, identifier, err, artifactIdentifiers(t, api, name))
	}
	wantEqual(t, "artifact name", cb.Name, name)
	wantEqual(t, "artifact version", cb.Version, version)
	wantEqual(t, "artifact metadata name", cb.Metadata.Name, name)
	sum := md5.Sum([]byte(recipe))
	want := hex.EncodeToString(sum[:])
	for _, f := range append(append([]cinc.CookbookFileRef{}, cb.AllFilesManifest...), cb.Recipes...) {
		if strings.HasSuffix(f.Path, "recipes/default.rb") || f.Name == "recipes/default.rb" {
			wantEqual(t, "recipes/default.rb checksum", f.Checksum, want)
			return
		}
	}
	t.Fatalf("cookbook artifact %s@%s has no recipes/default.rb: %+v", name, identifier, cb)
}

// testCookbook is a cookbook written to disk for a case. Its recipe content
// is unique, because the server stores file contents by checksum and shares
// them between cookbooks: two cases uploading identical files would race on
// the same stored blob.
type testCookbook struct {
	name, version, recipe string
}

// writeCookbook writes a cookbook to dir/<subdir> with a unique default
// recipe and the given metadata depends lines, and returns it.
func writeCookbook(t *testing.T, dir, subdir, name, version string, depends ...string) testCookbook {
	t.Helper()
	md := fmt.Sprintf("name '%s'\nversion '%s'\n", name, version)
	for _, d := range depends {
		md += "depends " + d + "\n"
	}
	cb := testCookbook{name: name, version: version, recipe: fmt.Sprintf("log '%s %s %s'\n", name, version, randomHex(t, 8))}
	writeFile(t, filepath.Join(dir, subdir, "metadata.rb"), md)
	writeFile(t, filepath.Join(dir, subdir, "recipes", "default.rb"), cb.recipe)
	return cb
}

// lockCookbook is one cookbook_locks entry of a hand-written lock.
type lockCookbook struct {
	name, version, identifier string
	sourceOptions             map[string]any
	cacheKey                  string
}

// policyLock describes a hand-written Policyfile.lock.json. Hand-written
// locks let a case choose identifiers and sources that `policy install`
// cannot produce (a chef_server source, two revisions of one policy)
// without paying for the Ruby engine.
type policyLock struct {
	name, revision string
	runList        []string
	cookbooks      []lockCookbook
	defaults       map[string]any
}

// write writes the lock to dir/Policyfile.lock.json and returns its path.
func (l policyLock) write(t *testing.T, dir string) string {
	t.Helper()
	locks := map[string]any{}
	deps := map[string]any{}
	for _, cb := range l.cookbooks {
		entry := map[string]any{
			"version":                   cb.version,
			"identifier":                cb.identifier,
			"dotted_decimal_identifier": dottedDecimal(cb.identifier),
			"source_options":            cb.sourceOptions,
		}
		if cb.cacheKey != "" {
			entry["cache_key"] = cb.cacheKey
		}
		locks[cb.name] = entry
		deps[cb.name+" ("+cb.version+")"] = []any{}
	}
	doc := map[string]any{
		"name":                  l.name,
		"revision_id":           l.revision,
		"run_list":              l.runList,
		"named_run_lists":       map[string]any{},
		"cookbook_locks":        locks,
		"default_attributes":    orEmpty(l.defaults),
		"override_attributes":   map[string]any{},
		"solution_dependencies": map[string]any{"Policyfile": []any{}, "dependencies": deps},
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "Policyfile.lock.json")
	writeFile(t, path, string(b))
	return path
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// dottedDecimal is chef's dotted-decimal form of a 40-hex identifier: digits
// 0-13, 14-27 and 28-39 read as three integers. Export and push-archive name
// bundle directories after it.
func dottedDecimal(id string) string {
	parts := make([]string, 0, 3)
	for _, span := range [][2]int{{0, 14}, {14, 28}, {28, 40}} {
		n, _ := strconv.ParseUint(id[span[0]:span[1]], 16, 64)
		parts = append(parts, strconv.FormatUint(n, 10))
	}
	return strings.Join(parts, ".")
}

// identifier returns a random 40-hex cookbook identifier or revision id, the
// shape chef computes.
func identifier(t *testing.T) string {
	t.Helper()
	return randomHex(t, 20)
}

// pathPolicy writes a lock for policy with one path-sourced cookbook of its
// own under dir and returns the lock path and the cookbook.
func pathPolicy(t *testing.T, dir, policy, cookbook, version string) (string, testCookbook, policyLock) {
	t.Helper()
	cb := writeCookbook(t, dir, filepath.Join("cookbooks", cookbook), cookbook, version)
	lock := policyLock{
		name:     policy,
		revision: identifier(t),
		runList:  []string{"recipe[" + cookbook + "::default]"},
		cookbooks: []lockCookbook{{
			name: cookbook, version: version, identifier: identifier(t),
			sourceOptions: map[string]any{"path": "cookbooks/" + cookbook},
		}},
	}
	return lock.write(t, dir), cb, lock
}

// pushResult is the --format json output of policy push and push-archive.
type pushResult struct {
	Policy            string `json:"policy"`
	Group             string `json:"group"`
	RevisionID        string `json:"revision_id"`
	CookbooksUploaded int    `json:"cookbooks_uploaded"`
}

// cleanupPolicy registers deletes for a policy and the groups it is pushed
// to. Groups go first (cleanups run in reverse), then the policy.
func cleanupPolicy(c *cli, policy string, groups ...string) {
	c.t.Helper()
	c.cleanup("policy", "delete", policy)
	for _, g := range groups {
		c.cleanup("policy-group", "delete", g)
	}
}

func showPolicyGroup(c *cli, group string, flags ...string) cinc.PolicyGroup {
	c.t.Helper()
	var g cinc.PolicyGroup
	c.json(&g, append([]string{"policy-group", "show", group}, flags...)...)
	return g
}

func policyRevisions(c *cli, policy string, flags ...string) []string {
	c.t.Helper()
	var p cinc.PolicyRevisions
	c.json(&p, append([]string{"policy", "show", policy}, flags...)...)
	revs := make([]string, 0, len(p.Revisions))
	for r := range p.Revisions {
		revs = append(revs, r)
	}
	return revs
}

// The Policyfile evaluation runtime (ruby.wasm) is downloaded once per
// machine into the OS cache directory, and its compiled form is cached
// there too. Each case runs the binary with its own HOME, and on macOS (and
// on Linux without XDG_CACHE_HOME) the cache lives under HOME, so without
// help every case would download 25 MB and spend seconds compiling it,
// enough to hit the evaluation timeout on a busy machine. requireRuby warms
// the real cache once and links each case's cache directory to it.
var (
	rubyOnce    sync.Once
	rubySkip    string // non-empty: the runtime cannot be fetched here
	rubyErr     error
	rubyCache   string // the real <UserCacheDir>/cinc-cli
	rubyCacheIn string // that directory relative to HOME, or "" if outside it
)

// requireRuby skips the case when the Ruby runtime cannot be fetched, and
// otherwise points the case's cache at the warmed one.
func requireRuby(t *testing.T, c *cli) {
	t.Helper()
	rubyOnce.Do(func() { warmRuby(c.bin) })
	if rubySkip != "" {
		t.Skipf("skipping: the Policyfile runtime (ruby.wasm) is unavailable, and fetching it needs the network on first use: %s", rubySkip)
	}
	if rubyErr != nil {
		t.Fatalf("preparing the Policyfile runtime: %v", rubyErr)
	}
	if rubyCacheIn == "" {
		return // the cache is outside HOME (XDG_CACHE_HOME), which every case inherits
	}
	link := filepath.Join(c.home, rubyCacheIn, "cinc-cli")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rubyCache, link); err != nil && !os.IsExist(err) {
		t.Fatalf("link the Policyfile runtime cache: %v", err)
	}
}

// warmRuby downloads the runtime in process, then runs the binary once on a
// trivial Policyfile with the real HOME, so the compiled module is cached
// by the very binary the cases run.
func warmRuby(bin string) {
	if err := rubyeval.NewEngine().Available(); err != nil {
		if rubyeval.IsUnavailable(err) {
			rubySkip = err.Error()
			return
		}
		rubyErr = err
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		rubyErr = err
		return
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		rubyErr = err
		return
	}
	rubyCache = filepath.Join(cache, "cinc-cli")
	if rel, err := filepath.Rel(home, cache); err == nil && !strings.HasPrefix(rel, "..") {
		rubyCacheIn = rel
	}

	dir, err := os.MkdirTemp("", "cinc-ruby-warm-*")
	if err != nil {
		rubyErr = err
		return
	}
	defer os.RemoveAll(dir)
	files := map[string]string{
		"Policyfile.rb":    "name 'warm'\nrun_list 'warm::default'\ncookbook 'warm', path: 'warm'\n",
		"warm/metadata.rb": "name 'warm'\nversion '1.0.0'\n",
		filepath.Join("warm", "recipes", "default.rb"): "log 'warm'\n",
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			rubyErr = err
			return
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			rubyErr = err
			return
		}
	}
	cmd := exec.Command(bin, "policy", "install", filepath.Join(dir, "Policyfile.rb"))
	cmd.Dir = dir
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); !strings.HasPrefix(k, "CINC_") && !strings.HasPrefix(k, "CHEF_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		rubyErr = fmt.Errorf("warm-up policy install: %v\n%s", err, out)
	}
}
