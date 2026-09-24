package suite

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var policyFamily = family{cases: []testCase{
	{"policies/install-push", []string{"policy install", "policy push", "policy list", "policy show", "policy-group show", "policy-group list"}, testPolicyInstallPush},
	{"policies/push-again-uploads-nothing", []string{"policy push"}, testPolicyPushAgainUploadsNothing},
	{"policies/push-default-lock", []string{"policy push"}, testPolicyPushDefaultLock},
	{"policies/chef-server-source", []string{"policy push", "policy export"}, testPolicyChefServerSource},
	{"policies/git-source", []string{"policy push"}, testPolicyGitSource},
	{"policies/git-source-rel", []string{"policy push"}, testPolicyGitSourceRel},
	{"policies/diff", []string{"policy diff"}, testPolicyDiff},
	{"policies/diff-errors", []string{"policy diff"}, testPolicyDiffErrors},
	{"policies/export", []string{"policy export"}, testPolicyExport},
	{"policies/push-archive", []string{"policy export", "policy push-archive"}, testPolicyPushArchive},
	{"policies/clean", []string{"policy clean"}, testPolicyClean},
	{"policies/clean-cookbooks", []string{"policy clean", "policy clean-cookbooks"}, testPolicyCleanCookbooks},
	{"policies/delete", []string{"policy delete", "policy show", "policy list"}, testPolicyDelete},
	{"policies/delete-leaves-group-empty", []string{"policy delete", "policy-group show"}, testPolicyDeleteLeavesGroupEmpty},
	{"policies/group-delete", []string{"policy-group delete", "policy-group show", "policy-group list", "policy show"}, testPolicyGroupDelete},
	{"policies/not-found", []string{"policy show", "policy delete", "policy-group show", "policy-group delete"}, testPolicyNotFound},
	{"policies/push-rejects-invalid-lock", []string{"policy push"}, testPolicyPushRejectsInvalidLock},
	{"policies/push-errors", []string{"policy push", "policy push-archive"}, testPolicyPushErrors},
	{"policies/forbidden", []string{"policy delete", "policy-group delete", "policy push", "policy list"}, testPolicyForbidden},
	{"policies/create", []string{"policy create"}, testPolicyCreate},
	{"policies/install-errors", []string{"policy install"}, testPolicyInstallErrors},
}}

// pushJSON pushes lockPath to group and returns the --format json summary.
func pushJSON(c *cli, group, lockPath string, flags ...string) pushResult {
	c.t.Helper()
	var r pushResult
	c.json(&r, append([]string{"policy", "push", group, lockPath}, flags...)...)
	return r
}

// testPolicyInstallPush is the whole Policyfile workflow on the server:
// install evaluates a dynamic Policyfile.rb and resolves a transitive
// dependency between two path cookbooks, push uploads both as cookbook
// artifacts under the identifiers install computed and pins the revision in
// a group, and every read verb sees the result.
func testPolicyInstallPush(t *testing.T, tgt Target, c *cli) {
	requireRuby(t, c)
	api := adminAPI(t, tgt, tgt.Org)
	policy, group := uniqueName(t, "policy"), uniqueName(t, "pgroup")
	app, dep := uniqueName(t, "cb"), uniqueName(t, "cb")
	cleanupArtifacts(c, api, app, dep)
	cleanupPolicy(c, policy, group)

	dir := t.TempDir()
	appCB := writeCookbook(t, dir, "cookbooks/"+app, app, "1.0.0", fmt.Sprintf("'%s', '~> 2.0'", dep))
	depCB := writeCookbook(t, dir, "cookbooks/"+dep, dep, "2.3.0")
	writeFile(t, filepath.Join(dir, "Policyfile.rb"), fmt.Sprintf(`name '%[1]s'

# Built by Ruby, so the evaluator is real Ruby and not a static parser.
run_list(%%w[%[2]s].map { |c| "#{c}::default" })

cookbook '%[2]s', path: 'cookbooks/%[2]s'
cookbook '%[3]s', path: 'cookbooks/%[3]s'
cookbook 'extras', path: 'cookbooks/extras' if ENV['CINC_SUITE_EXTRAS'] == '1'

default['%[2]s']['port'] = 8080
`, policy, app, dep))

	var summary struct {
		Name       string `json:"name"`
		RevisionID string `json:"revision_id"`
		Lock       string `json:"lock"`
		Cookbooks  []struct {
			Name, Version, Identifier string
		} `json:"cookbooks"`
	}
	c.json(&summary, "policy", "install", filepath.Join(dir, "Policyfile.rb"))
	wantEqual(t, "install name", summary.Name, policy)
	lockPath := filepath.Join(dir, "Policyfile.lock.json")
	wantEqual(t, "install lock path", summary.Lock, lockPath)
	if len(summary.RevisionID) != 64 {
		t.Errorf("install revision_id = %q, want a SHA-256", summary.RevisionID)
	}

	var lock struct {
		RevisionID    string   `json:"revision_id"`
		RunList       []string `json:"run_list"`
		CookbookLocks map[string]struct {
			Version    string `json:"version"`
			Identifier string `json:"identifier"`
			Dotted     string `json:"dotted_decimal_identifier"`
		} `json:"cookbook_locks"`
		Defaults map[string]map[string]any `json:"default_attributes"`
	}
	readJSONFile(t, lockPath, &lock)
	wantEqual(t, "lock revision_id", lock.RevisionID, summary.RevisionID)
	wantSlice(t, "lock run_list", lock.RunList, []string{"recipe[" + app + "::default]"})
	if lock.Defaults[app]["port"] != float64(8080) {
		t.Errorf("lock default_attributes = %v, want %s.port 8080", lock.Defaults, app)
	}
	if len(lock.CookbookLocks) != 2 {
		t.Fatalf("lock cookbook_locks = %v, want %s and its dependency %s only", lock.CookbookLocks, app, dep)
	}
	for _, cb := range []testCookbook{appCB, depCB} {
		l := lock.CookbookLocks[cb.name]
		wantEqual(t, cb.name+" locked version", l.Version, cb.version)
		if len(l.Identifier) != 40 || l.Dotted == "" {
			t.Errorf("%s lock = %+v, want a 40-hex identifier and a dotted-decimal one", cb.name, l)
		}
	}

	got := pushJSON(c, group, lockPath)
	wantEqual(t, "push policy", got.Policy, policy)
	wantEqual(t, "push group", got.Group, group)
	wantEqual(t, "push revision", got.RevisionID, lock.RevisionID)
	wantEqual(t, "push cookbooks_uploaded", got.CookbooksUploaded, 2)
	for _, cb := range []testCookbook{appCB, depCB} {
		wantArtifact(t, api, cb.name, lock.CookbookLocks[cb.name].Identifier, cb.version, cb.recipe)
	}

	// Every read verb, in both formats.
	var names []string
	c.json(&names, "policy", "list")
	if !slices.Contains(names, policy) {
		t.Errorf("policy list --format json = %v, want %s", names, policy)
	}
	if !slices.Contains(strings.Fields(c.run("policy", "list")), policy) {
		t.Errorf("policy list does not name %s", policy)
	}
	var otherNames []string
	c.json(&otherNames, "policy", "list", "--profile", "other")
	if slices.Contains(otherNames, policy) {
		t.Errorf("policy list in %s names %s, which was pushed to %s", tgt.OtherOrg, policy, tgt.Org)
	}
	wantSlice(t, "policy show revisions", policyRevisions(c, policy), []string{lock.RevisionID})
	if human := c.run("policy", "show", policy); !strings.Contains(human, lock.RevisionID) {
		t.Errorf("policy show does not name revision %s:\n%s", lock.RevisionID, human)
	}
	g := showPolicyGroup(c, group)
	wantEqual(t, "policy-group show revision", g.Policies[policy].RevisionID, lock.RevisionID)
	if len(g.Policies) != 1 {
		t.Errorf("policy-group show policies = %v, want only %s", g.Policies, policy)
	}
	if human := c.run("policy-group", "show", group); !strings.Contains(human, policy) || !strings.Contains(human, lock.RevisionID) {
		t.Errorf("policy-group show does not name %s at %s:\n%s", policy, lock.RevisionID, human)
	}
	var groups []string
	c.json(&groups, "policy-group", "list")
	if !slices.Contains(groups, group) {
		t.Errorf("policy-group list --format json = %v, want %s", groups, group)
	}
	if !slices.Contains(strings.Fields(c.run("policy-group", "list")), group) {
		t.Errorf("policy-group list does not name %s", group)
	}
}

// testPolicyPushAgainUploadsNothing pushes one lock to two groups. The
// second push finds every artifact already on the server and uploads none,
// so its summary must say so rather than repeat the first push's count.
func testPolicyPushAgainUploadsNothing(t *testing.T, tgt Target, c *cli) {
	api := adminAPI(t, tgt, tgt.Org)
	policy, first, second := uniqueName(t, "policy"), uniqueName(t, "pgroup"), uniqueName(t, "pgroup")
	cb := uniqueName(t, "cb")
	cleanupArtifacts(c, api, cb)
	cleanupPolicy(c, policy, first, second)
	lockPath, _, lock := pathPolicy(t, t.TempDir(), policy, cb, "1.0.0")

	wantEqual(t, "first push cookbooks_uploaded", pushJSON(c, first, lockPath).CookbooksUploaded, 1)
	again := pushJSON(c, second, lockPath)
	wantEqual(t, "second push revision", again.RevisionID, lock.revision)
	wantSlice(t, "artifact identifiers", artifactIdentifiers(t, api, cb), []string{lock.cookbooks[0].identifier})
	wantEqual(t, "second push cookbooks_uploaded", again.CookbooksUploaded, 0)
}

// testPolicyPushDefaultLock runs push with no lock argument, from the
// directory holding Policyfile.lock.json, and checks the human summary.
func testPolicyPushDefaultLock(t *testing.T, tgt Target, c *cli) {
	api := adminAPI(t, tgt, tgt.Org)
	policy, group, cb := uniqueName(t, "policy"), uniqueName(t, "pgroup"), uniqueName(t, "cb")
	cleanupArtifacts(c, api, cb)
	cleanupPolicy(c, policy, group)
	// The binary runs in the case's HOME, so the lock goes there.
	_, written, lock := pathPolicy(t, c.home, policy, cb, "0.1.0")

	out := c.run("policy", "push", group)
	wantEqual(t, "push output", out, fmt.Sprintf("Pushed policy %q (revision %s) to group %q with 1 cookbook(s)\n", policy, lock.revision, group))
	wantArtifact(t, api, cb, lock.cookbooks[0].identifier, "0.1.0", written.recipe)
	wantEqual(t, "group revision", showPolicyGroup(c, group).Policies[policy].RevisionID, lock.revision)
}

// testPolicyChefServerSource pushes and exports a lock whose cookbook comes
// from the Cinc Server itself (a chef_server source, as `default_source
// :chef_server` writes). The CLI downloads the uploaded cookbook version
// into its cache, whose directory is named after the lock's cache_key, and
// must still upload the artifact under the cookbook's own name.
func testPolicyChefServerSource(t *testing.T, tgt Target, c *cli) {
	api := adminAPI(t, tgt, tgt.Org)
	policy, group, cb := uniqueName(t, "policy"), uniqueName(t, "pgroup"), uniqueName(t, "cb")

	repo := t.TempDir()
	written := writeCookbook(t, repo, cb, cb, "1.4.2")
	c.run("cookbook", "upload", cb, "--cookbook-path", repo)
	c.cleanup("cookbook", "delete", cb, "1.4.2")
	cleanupArtifacts(c, api, cb)
	cleanupPolicy(c, policy, group)

	lock := policyLock{
		name: policy, revision: identifier(t),
		runList: []string{"recipe[" + cb + "::default]"},
		cookbooks: []lockCookbook{{
			name: cb, version: "1.4.2", identifier: identifier(t),
			cacheKey:      cb + "-1.4.2-chefserver",
			sourceOptions: map[string]any{"chef_server": c.orgURL(tgt.Org), "version": "1.4.2"},
		}},
	}
	lockPath := lock.write(t, t.TempDir())

	got := pushJSON(c, group, lockPath)
	wantEqual(t, "push revision", got.RevisionID, lock.revision)
	wantEqual(t, "push cookbooks_uploaded", got.CookbooksUploaded, 1)
	wantArtifact(t, api, cb, lock.cookbooks[0].identifier, "1.4.2", written.recipe)
	if ids := artifactIdentifiers(t, api, lock.cookbooks[0].cacheKey); len(ids) != 0 {
		t.Errorf("push uploaded an artifact named after the cache key %s: %v", lock.cookbooks[0].cacheKey, ids)
	}

	// Export reads the same cookbook, from the cache push filled.
	bundle := filepath.Join(t.TempDir(), "bundle")
	c.run("policy", "export", lockPath, bundle)
	recipe := filepath.Join(bundle, "cookbooks", cb+"-"+dottedDecimal(lock.cookbooks[0].identifier), "recipes", "default.rb")
	wantEqual(t, "exported recipe", readFile(t, recipe), written.recipe)

	// And a fresh HOME, with an empty cache, downloads it again.
	fresh := newCLI(t, tgt)
	bundle2 := filepath.Join(t.TempDir(), "bundle")
	fresh.run("policy", "export", lockPath, bundle2)
	recipe2 := filepath.Join(bundle2, "cookbooks", cb+"-"+dottedDecimal(lock.cookbooks[0].identifier), "recipes", "default.rb")
	wantEqual(t, "exported recipe from a cold cache", readFile(t, recipe2), written.recipe)
}

// testPolicyGitSource pushes a lock pinning a cookbook at the root of a
// git repository by revision: the CLI clones the repository, checks out the
// pinned commit (not the later one) and uploads the artifact.
func testPolicyGitSource(t *testing.T, tgt Target, c *cli) {
	pushGitCookbook(t, tgt, c, "", nil)
}

// testPolicyGitSourceRel is the monorepo layout: the cookbook lives in a
// subdirectory of the repository, which chef records in the lock as the
// git source's "rel" option.
func testPolicyGitSourceRel(t *testing.T, tgt Target, c *cli) {
	pushGitCookbook(t, tgt, c, "cookbooks/app", map[string]any{"rel": "cookbooks/app"})
}

// pushGitCookbook commits a cookbook at sub (the repository root when
// empty), commits a later change the lock does not pin, pushes a lock with
// a git source plus extra source options, and checks the artifact holds the
// pinned content.
func pushGitCookbook(t *testing.T, tgt Target, c *cli, sub string, extra map[string]any) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	api := adminAPI(t, tgt, tgt.Org)
	policy, group, cb := uniqueName(t, "policy"), uniqueName(t, "pgroup"), uniqueName(t, "cb")
	cleanupArtifacts(c, api, cb)
	cleanupPolicy(c, policy, group)

	repo := t.TempDir()
	written := writeCookbook(t, repo, sub, cb, "3.0.0")
	commit := []string{"-c", "user.email=t@example.test", "-c", "user.name=Test", "commit", "--quiet"}
	gitRun(t, repo, "init", "--quiet")
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, append(commit, "-m", "cookbook")...)
	sha := strings.TrimSpace(gitRun(t, repo, "rev-parse", "HEAD"))
	writeFile(t, filepath.Join(repo, sub, "recipes", "default.rb"), "log 'not pinned'\n")
	gitRun(t, repo, append(commit, "-am", "later")...)

	opts := map[string]any{"git": repo, "revision": sha}
	for k, v := range extra {
		opts[k] = v
	}
	lock := policyLock{
		name: policy, revision: identifier(t),
		runList: []string{"recipe[" + cb + "::default]"},
		cookbooks: []lockCookbook{{
			name: cb, version: "3.0.0", identifier: identifier(t),
			cacheKey: cb + "-" + sha, sourceOptions: opts,
		}},
	}
	got := pushJSON(c, group, lock.write(t, t.TempDir()))
	wantEqual(t, "push revision", got.RevisionID, lock.revision)
	wantArtifact(t, api, cb, lock.cookbooks[0].identifier, "3.0.0", written.recipe)
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// twoRevisions pushes two revisions of one policy, one to each group: the
// second bumps the cookbook, adds a recipe and changes an attribute.
func twoRevisions(t *testing.T, tgt Target, c *cli) (policy, from, to, cb string, revA, revB policyLock) {
	t.Helper()
	api := adminAPI(t, tgt, tgt.Org)
	policy, from, to, cb = uniqueName(t, "policy"), uniqueName(t, "pgroup"), uniqueName(t, "pgroup"), uniqueName(t, "cb")
	cleanupArtifacts(c, api, cb)
	cleanupPolicy(c, policy, from, to)

	dirA := t.TempDir()
	writeCookbook(t, dirA, "cookbooks/"+cb, cb, "1.0.0")
	revA = policyLock{
		name: policy, revision: identifier(t),
		runList: []string{"recipe[" + cb + "::default]"},
		cookbooks: []lockCookbook{{name: cb, version: "1.0.0", identifier: identifier(t),
			sourceOptions: map[string]any{"path": "cookbooks/" + cb}}},
		defaults: map[string]any{cb: map[string]any{"port": 80, "user": "web"}},
	}
	pushJSON(c, from, revA.write(t, dirA))

	dirB := t.TempDir()
	writeCookbook(t, dirB, "cookbooks/"+cb, cb, "1.1.0")
	revB = policyLock{
		name: policy, revision: identifier(t),
		runList: []string{"recipe[" + cb + "::default]", "recipe[" + cb + "::ssl]"},
		cookbooks: []lockCookbook{{name: cb, version: "1.1.0", identifier: identifier(t),
			sourceOptions: map[string]any{"path": "cookbooks/" + cb}}},
		defaults: map[string]any{cb: map[string]any{"port": 443, "user": "web"}},
	}
	pushJSON(c, to, revB.write(t, dirB))
	return policy, from, to, cb, revA, revB
}

type policyDiffJSON struct {
	Policy string `json:"policy"`
	From   struct {
		Ref        string `json:"ref"`
		RevisionID string `json:"revision_id"`
	} `json:"from"`
	To struct {
		Ref        string `json:"ref"`
		RevisionID string `json:"revision_id"`
	} `json:"to"`
	Cookbooks []struct {
		Name string `json:"name"`
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"cookbooks"`
	RunList struct {
		Added   []string `json:"added"`
		Removed []string `json:"removed"`
	} `json:"run_list"`
	Attributes []struct {
		Path string `json:"path"`
		From any    `json:"from"`
		To   any    `json:"to"`
	} `json:"attributes"`
}

// testPolicyDiff compares the revisions two groups pin, then the same two
// revisions by id, then a group with itself.
func testPolicyDiff(t *testing.T, tgt Target, c *cli) {
	policy, from, to, cb, revA, revB := twoRevisions(t, tgt, c)

	var d policyDiffJSON
	c.json(&d, "policy", "diff", policy, from, to)
	wantEqual(t, "diff policy", d.Policy, policy)
	wantEqual(t, "diff from", d.From.Ref+"@"+d.From.RevisionID, from+"@"+revA.revision)
	wantEqual(t, "diff to", d.To.Ref+"@"+d.To.RevisionID, to+"@"+revB.revision)
	if len(d.Cookbooks) != 1 || d.Cookbooks[0].Name != cb || d.Cookbooks[0].From != "1.0.0" || d.Cookbooks[0].To != "1.1.0" {
		t.Errorf("diff cookbooks = %+v, want %s 1.0.0 -> 1.1.0", d.Cookbooks, cb)
	}
	wantSlice(t, "diff run_list added", d.RunList.Added, []string{"recipe[" + cb + "::ssl]"})
	wantSlice(t, "diff run_list removed", d.RunList.Removed, []string{})
	port := fmt.Sprintf("default['%s']['port']", cb)
	if len(d.Attributes) != 1 || d.Attributes[0].Path != port || d.Attributes[0].From != float64(80) || d.Attributes[0].To != float64(443) {
		t.Errorf("diff attributes = %+v, want only %s 80 -> 443", d.Attributes, port)
	}

	human := c.run("policy", "diff", policy, from, to)
	for _, want := range []string{
		fmt.Sprintf("%s: %s (%s) -> %s (%s)", policy, from, revA.revision, to, revB.revision),
		fmt.Sprintf("cookbook  %s  1.0.0 -> 1.1.0", cb),
		fmt.Sprintf("run_list  + recipe[%s::ssl]", cb),
		fmt.Sprintf("attr  %s  80 -> 443", port),
	} {
		if !strings.Contains(human, want) {
			t.Errorf("policy diff missing %q:\n%s", want, human)
		}
	}

	// The same comparison by revision id, in the other direction.
	var back policyDiffJSON
	c.json(&back, "policy", "diff", policy, "--revisions", revB.revision, revA.revision)
	wantEqual(t, "revisions diff from", back.From.RevisionID, revB.revision)
	wantSlice(t, "revisions diff run_list removed", back.RunList.Removed, []string{"recipe[" + cb + "::ssl]"})
	wantSlice(t, "revisions diff run_list added", back.RunList.Added, []string{})

	if same := c.run("policy", "diff", policy, from, from); !strings.Contains(same, "No differences.") {
		t.Errorf("diff of a group with itself = %q, want No differences.", same)
	}
}

// testPolicyDiffErrors checks every way a diff can fail to find a side.
func testPolicyDiffErrors(t *testing.T, tgt Target, c *cli) {
	policy, from, _, _, revA, _ := twoRevisions(t, tgt, c)
	ghost := uniqueName(t, "ghost")

	wantNotFound(t, c.fail("policy", "diff", policy, from, ghost))
	wantNotFound(t, c.fail("policy", "diff", policy, "--revisions", revA.revision, identifier(t)))
	wantNotFound(t, c.fail("policy", "diff", ghost, "--revisions", revA.revision, revA.revision))

	// A group that exists but does not pin this policy.
	other, otherGroup, otherCB := uniqueName(t, "policy"), uniqueName(t, "pgroup"), uniqueName(t, "cb")
	cleanupArtifacts(c, adminAPI(t, tgt, tgt.Org), otherCB)
	cleanupPolicy(c, other, otherGroup)
	lockPath, _, _ := pathPolicy(t, t.TempDir(), other, otherCB, "1.0.0")
	pushJSON(c, otherGroup, lockPath)
	r := c.fail("policy", "diff", policy, from, otherGroup)
	if !strings.Contains(r.stderr, fmt.Sprintf("policy %q is not assigned to group %q", policy, otherGroup)) {
		t.Errorf("diff against a group without the policy should say so: %s", r)
	}

	// Argument checks come before any request.
	if r := c.fail("policy", "diff", policy, from); !strings.Contains(r.stderr, "give two policy group names") {
		t.Errorf("diff with one group should ask for two: %s", r)
	}
	if r := c.fail("policy", "diff", policy, "--revisions", revA.revision); !strings.Contains(r.stderr, "--revisions takes exactly two revision ids") {
		t.Errorf("diff --revisions with one id should ask for two: %s", r)
	}
}

// testPolicyExport exports a path-sourced lock to a directory and a
// tarball and checks the bundle layout cinc-client -z expects.
func testPolicyExport(t *testing.T, _ Target, c *cli) {
	policy, cb := uniqueName(t, "policy"), uniqueName(t, "cb")
	lockPath, written, lock := pathPolicy(t, t.TempDir(), policy, cb, "2.0.0")
	bundle := filepath.Join(t.TempDir(), "bundle")

	var res struct {
		Dir, Archive, Policy string
	}
	c.json(&res, "policy", "export", lockPath, bundle, "--archive")
	wantEqual(t, "export policy", res.Policy, policy)
	wantEqual(t, "export dir", res.Dir, bundle)
	wantEqual(t, "export archive", res.Archive, bundle+".tar.gz")
	if _, err := os.Stat(res.Archive); err != nil {
		t.Errorf("export archive: %v", err)
	}
	cbDir := filepath.Join(bundle, "cookbooks", cb+"-"+dottedDecimal(lock.cookbooks[0].identifier))
	wantEqual(t, "exported recipe", readFile(t, filepath.Join(cbDir, "recipes", "default.rb")), written.recipe)
	wantEqual(t, "exported lock", readFile(t, filepath.Join(bundle, "Policyfile.lock.json")), readFile(t, lockPath))
	wantEqual(t, "exported policy document", readFile(t, filepath.Join(bundle, "policies", policy+"-"+lock.revision+".json")), readFile(t, lockPath))
	if rb := readFile(t, filepath.Join(bundle, "client.rb")); !strings.Contains(rb, fmt.Sprintf("policy_name %q", policy)) && !strings.Contains(rb, "policy_name '"+policy+"'") {
		t.Errorf("client.rb does not select policy %s:\n%s", policy, rb)
	}

	human := c.run("policy", "export", lockPath, filepath.Join(t.TempDir(), "again"))
	if !strings.HasPrefix(human, fmt.Sprintf("Exported policy %q to ", policy)) {
		t.Errorf("export output = %q", human)
	}
}

// testPolicyPushArchive deploys an exported bundle, as a directory and as a
// tarball, and checks the artifact and group assignment each produces.
func testPolicyPushArchive(t *testing.T, tgt Target, c *cli) {
	api := adminAPI(t, tgt, tgt.Org)
	policy, dirGroup, tarGroup, cwdGroup := uniqueName(t, "policy"), uniqueName(t, "pgroup"), uniqueName(t, "pgroup"), uniqueName(t, "pgroup")
	cb := uniqueName(t, "cb")
	cleanupArtifacts(c, api, cb)
	cleanupPolicy(c, policy, dirGroup, tarGroup, cwdGroup)
	lockPath, written, lock := pathPolicy(t, t.TempDir(), policy, cb, "1.2.0")
	bundle := filepath.Join(t.TempDir(), "bundle")
	c.run("policy", "export", lockPath, bundle, "--archive")

	var got pushResult
	c.json(&got, "policy", "push-archive", dirGroup, bundle)
	wantEqual(t, "push-archive (dir) policy", got.Policy, policy)
	wantEqual(t, "push-archive (dir) revision", got.RevisionID, lock.revision)
	wantEqual(t, "push-archive (dir) cookbooks_uploaded", got.CookbooksUploaded, 1)
	wantArtifact(t, api, cb, lock.cookbooks[0].identifier, "1.2.0", written.recipe)
	wantEqual(t, "dir group revision", showPolicyGroup(c, dirGroup).Policies[policy].RevisionID, lock.revision)

	out := c.run("policy", "push-archive", tarGroup, bundle+".tar.gz")
	if !strings.Contains(out, fmt.Sprintf("Pushed policy %q (revision %s) to group %q", policy, lock.revision, tarGroup)) {
		t.Errorf("push-archive (tarball) output = %q", out)
	}
	wantEqual(t, "tarball group revision", showPolicyGroup(c, tarGroup).Policies[policy].RevisionID, lock.revision)

	// With no archive named, push-archive picks the only tarball in the
	// working directory (the case's HOME).
	writeFile(t, filepath.Join(c.home, "bundle.tar.gz"), readFile(t, bundle+".tar.gz"))
	c.run("policy", "push-archive", cwdGroup)
	wantEqual(t, "cwd group revision", showPolicyGroup(c, cwdGroup).Policies[policy].RevisionID, lock.revision)

	wantSlice(t, "artifact identifiers", artifactIdentifiers(t, api, cb), []string{lock.cookbooks[0].identifier})
	wantSlice(t, "policy revisions", policyRevisions(c, policy), []string{lock.revision})
}

// testPolicyClean replaces a group's revision so the old one is pinned by no
// group, then cleans it, scoped to this case's policy so a parallel case's
// revisions are never touched.
func testPolicyClean(t *testing.T, tgt Target, c *cli) {
	policy, group, _, _, revA, revB := twoRevisions(t, tgt, c)
	// Move group from revA to revB; revB is also pinned by the other group
	// twoRevisions made, revA by nothing.
	dir := t.TempDir()
	cb := revB.cookbooks[0].name
	writeCookbook(t, dir, "cookbooks/"+cb, cb, "1.1.0")
	pushJSON(c, group, revB.write(t, dir))

	dry := c.run("policy", "clean", policy, "--dry-run")
	if !strings.Contains(dry, fmt.Sprintf("Would delete %s (1 orphaned revision(s)): [%s]", policy, revA.revision)) {
		t.Errorf("clean --dry-run output = %q, want it to name %s", dry, revA.revision)
	}
	revs := policyRevisions(c, policy)
	slices.Sort(revs)
	want := []string{revA.revision, revB.revision}
	slices.Sort(want)
	wantSlice(t, "revisions after dry run", revs, want)

	// The unscoped dry run reads every policy and includes this one.
	if all := c.run("policy", "clean", "--dry-run"); !strings.Contains(all, revA.revision) {
		t.Errorf("unscoped clean --dry-run does not name %s:\n%s", revA.revision, all)
	}

	out := c.run("policy", "clean", policy)
	if !strings.Contains(out, fmt.Sprintf("Deleted %s (1 orphaned revision(s)): [%s]", policy, revA.revision)) ||
		!strings.Contains(out, "Kept 1 revision(s) still in use by a policy group") {
		t.Errorf("clean output = %q", out)
	}
	wantSlice(t, "revisions after clean", policyRevisions(c, policy), []string{revB.revision})
	if again := c.run("policy", "clean", policy); again != "" {
		t.Errorf("a second clean found more to do: %q", again)
	}
}

// testPolicyCleanCookbooks orphans a cookbook artifact the way a real
// workflow does (a newer revision replaces the old one, and policy clean
// removes it), then cleans cookbook artifacts. clean-cookbooks takes no
// scope: it deletes every unreferenced artifact in the organization, which
// would race with other cases' pushes (their artifacts are unreferenced
// until the group assignment lands). So the destructive run happens in the
// other organization, which no other case pushes policies to.
func testPolicyCleanCookbooks(t *testing.T, tgt Target, c *cli) {
	api := adminAPI(t, tgt, tgt.OtherOrg)
	other := []string{"--profile", "other"}
	policy, group, cb := uniqueName(t, "policy"), uniqueName(t, "pgroup"), uniqueName(t, "cb")
	cleanupArtifacts(c, api, cb)
	c.cleanup("policy", "delete", policy, "--profile", "other")
	c.cleanup("policy-group", "delete", group, "--profile", "other")

	oldLock, _, old := pathPolicy(t, t.TempDir(), policy, cb, "1.0.0")
	pushJSON(c, group, oldLock, other...)
	newLock, newCB, cur := pathPolicy(t, t.TempDir(), policy, cb, "1.0.1")
	pushJSON(c, group, newLock, other...)
	oldID, curID := old.cookbooks[0].identifier, cur.cookbooks[0].identifier

	// The old revision still references the old artifact.
	type report struct {
		DryRun  bool `json:"dry_run"`
		Deleted []struct {
			Name       string `json:"name"`
			Identifier string `json:"identifier"`
		} `json:"deleted"`
	}
	mine := func(r report) []string {
		var ids []string
		for _, d := range r.Deleted {
			if d.Name == cb {
				ids = append(ids, d.Identifier)
			}
		}
		return ids
	}
	var r report
	c.json(&r, append([]string{"policy", "clean-cookbooks", "--dry-run"}, other...)...)
	wantSlice(t, "orphans while both revisions exist", mine(r), nil)

	c.run(append([]string{"policy", "clean", policy}, other...)...)
	c.json(&r, append([]string{"policy", "clean-cookbooks", "--dry-run"}, other...)...)
	wantEqual(t, "dry_run", r.DryRun, true)
	wantSlice(t, "orphans after policy clean", mine(r), []string{oldID})
	dry := c.run(append([]string{"policy", "clean-cookbooks", "--dry-run"}, other...)...)
	if !strings.Contains(dry, "Would delete") || !strings.Contains(dry, cb+"@"+oldID) {
		t.Errorf("clean-cookbooks --dry-run output = %q, want it to name %s@%s", dry, cb, oldID)
	}
	ids := artifactIdentifiers(t, api, cb)
	slices.Sort(ids)
	want := []string{oldID, curID}
	slices.Sort(want)
	wantSlice(t, "artifacts after dry run", ids, want)

	out := c.run(append([]string{"policy", "clean-cookbooks"}, other...)...)
	if !strings.Contains(out, "Deleted") || !strings.Contains(out, cb+"@"+oldID) || strings.Contains(out, cb+"@"+curID) {
		t.Errorf("clean-cookbooks output = %q, want only %s@%s deleted", out, cb, oldID)
	}
	wantSlice(t, "artifacts after clean-cookbooks", artifactIdentifiers(t, api, cb), []string{curID})
	wantArtifact(t, api, cb, curID, "1.0.1", newCB.recipe)

	c.json(&r, append([]string{"policy", "clean-cookbooks"}, other...)...)
	wantEqual(t, "dry_run on a real run", r.DryRun, false)
	wantSlice(t, "orphans after clean-cookbooks", mine(r), nil)
}

// testPolicyDelete deletes a policy with every revision, and checks it is
// gone from show and list.
func testPolicyDelete(t *testing.T, tgt Target, c *cli) {
	policy, _, _, _, _, _ := twoRevisions(t, tgt, c)
	wantEqual(t, "delete output", c.run("policy", "delete", policy), fmt.Sprintf("Deleted policy %q\n", policy))
	wantNotFound(t, c.fail("policy", "show", policy))
	var names []string
	c.json(&names, "policy", "list")
	if slices.Contains(names, policy) {
		t.Errorf("policy list still names deleted %s", policy)
	}
	wantNotFound(t, c.fail("policy", "delete", policy))
}

// testPolicyDeleteLeavesGroupEmpty checks that deleting a policy removes its
// assignment from the groups that pinned it. erchef deletes the
// association with the revisions; a group left pointing at a revision that
// no longer exists would make chef-client fail on every node in it.
func testPolicyDeleteLeavesGroupEmpty(t *testing.T, tgt Target, c *cli) {
	policy, from, _, _, _, _ := twoRevisions(t, tgt, c)
	c.run("policy", "delete", policy)
	g := showPolicyGroup(c, from)
	if _, ok := g.Policies[policy]; ok {
		t.Errorf("group %s still pins deleted policy %s: %+v", from, policy, g.Policies)
	}
}

// testPolicyGroupDelete deletes a group and checks the policy it pinned
// survives.
func testPolicyGroupDelete(t *testing.T, tgt Target, c *cli) {
	policy, from, to, _, revA, revB := twoRevisions(t, tgt, c)
	wantEqual(t, "delete output", c.run("policy-group", "delete", from), fmt.Sprintf("Deleted policy group %q\n", from))
	wantNotFound(t, c.fail("policy-group", "show", from))
	var groups []string
	c.json(&groups, "policy-group", "list")
	if slices.Contains(groups, from) || !slices.Contains(groups, to) {
		t.Errorf("policy-group list = %v, want %s gone and %s kept", groups, from, to)
	}
	revs := policyRevisions(c, policy)
	slices.Sort(revs)
	want := []string{revA.revision, revB.revision}
	slices.Sort(want)
	wantSlice(t, "revisions after the group delete", revs, want)
	wantNotFound(t, c.fail("policy-group", "delete", from))
}

func testPolicyNotFound(t *testing.T, _ Target, c *cli) {
	ghost := uniqueName(t, "ghost")
	for _, args := range [][]string{
		{"policy", "show", ghost},
		{"policy", "show", ghost, "--format", "json"},
		{"policy", "delete", ghost},
		{"policy-group", "show", ghost},
		{"policy-group", "show", ghost, "--format", "json"},
		{"policy-group", "delete", ghost},
	} {
		r := c.fail(args...)
		wantNotFound(t, r)
		if r.stdout != "" {
			t.Errorf("cinc %s printed to stdout on a 404: %q", strings.Join(args, " "), r.stdout)
		}
	}
}

// testPolicyPushRejectsInvalidLock pushes locks erchef's policy validation
// refuses with 400: a revision_id outside letters, digits, '_', '-', '.'
// and ':', and a run list item that is not fully qualified (policies need
// recipe[cookbook::recipe]). Each push must fail and store nothing.
func testPolicyPushRejectsInvalidLock(t *testing.T, _ Target, c *cli) {
	for _, bad := range []struct {
		what     string
		revision string
		runList  []string
	}{
		{"revision_id with spaces", "not a revision!", []string{"recipe[base::default]"}},
		{"unqualified run list item", "0123456789abcdef", []string{"recipe[base]"}},
	} {
		policy, group := uniqueName(t, "policy"), uniqueName(t, "pgroup")
		cleanupPolicy(c, policy, group)
		lock := policyLock{name: policy, revision: bad.revision, runList: bad.runList}
		r := c.exec(runOpts{}, "policy", "push", group, lock.write(t, t.TempDir()))
		if r.exitCode == 0 {
			t.Errorf("push of a lock with a %s succeeded: %s", bad.what, r)
		}
		if r := c.exec(runOpts{}, "policy", "show", policy); r.exitCode == 0 {
			t.Errorf("the refused push (%s) stored policy %s", bad.what, policy)
		}
	}
}

// testPolicyPushErrors checks the push failures the CLI reports before it
// sends anything.
func testPolicyPushErrors(t *testing.T, _ Target, c *cli) {
	group := uniqueName(t, "pgroup")
	c.cleanup("policy-group", "delete", group)
	missing := filepath.Join(t.TempDir(), "Policyfile.lock.json")
	if r := c.fail("policy", "push", group, missing); !strings.Contains(r.stderr, missing) {
		t.Errorf("push of a missing lock should name it: %s", r)
	}

	// A path cookbook the lock names but the disk lacks.
	policy, cb := uniqueName(t, "policy"), uniqueName(t, "cb")
	c.cleanup("policy", "delete", policy)
	dir := t.TempDir()
	lockPath, _, _ := pathPolicy(t, dir, policy, cb, "1.0.0")
	if err := os.RemoveAll(filepath.Join(dir, "cookbooks")); err != nil {
		t.Fatal(err)
	}
	if r := c.fail("policy", "push", group, lockPath); !strings.Contains(r.stderr, cb) {
		t.Errorf("push with a missing cookbook should name it: %s", r)
	}
	wantNotFound(t, c.fail("policy", "show", policy))
	wantNotFound(t, c.fail("policy-group", "show", group))

	ghost := filepath.Join(t.TempDir(), "ghost.tar.gz")
	if r := c.fail("policy", "push-archive", group, ghost); !strings.Contains(r.stderr, "we couldn't find the bundle") {
		t.Errorf("push-archive of a missing bundle: %s", r)
	}
	// Nothing to pick in an empty working directory.
	if r := c.fail("policy", "push-archive", group); !strings.Contains(r.stderr, "we couldn't find a bundle to push here") {
		t.Errorf("push-archive with no bundle here: %s", r)
	}
	notBundle := t.TempDir()
	writeFile(t, filepath.Join(notBundle, "README"), "not a bundle\n")
	if r := c.fail("policy", "push-archive", group, notBundle); !strings.Contains(r.stderr, "Policyfile.lock.json") {
		t.Errorf("push-archive of a directory that is no bundle should say what is missing: %s", r)
	}
}

// testPolicyForbidden acts as a new API client, which the default ACLs let
// read policies but not change them, and checks the server refuses each
// change and nothing changed.
func testPolicyForbidden(t *testing.T, tgt Target, c *cli) {
	policy, group, _, _, revA, _ := twoRevisions(t, tgt, c)
	robot := uniqueName(t, "client")
	keyPath := filepath.Join(t.TempDir(), "robot.pem")
	c.run("client", "create", robot, "--key-file", keyPath)
	c.cleanup("client", "delete", robot)
	c.addProfile("robot", tgt.Org, robot, keyPath)
	as := []string{"--profile", "robot"}

	wantForbidden := func(r result) {
		t.Helper()
		low := strings.ToLower(r.stderr)
		if !hasStatus(low, 403) && !strings.Contains(low, "permission") && !strings.Contains(low, "forbidden") {
			t.Errorf("want a 403: %s", r)
		}
	}
	wantForbidden(c.fail(append([]string{"policy", "delete", policy}, as...)...))
	wantForbidden(c.fail(append([]string{"policy-group", "delete", group}, as...)...))

	dir := t.TempDir()
	cb := revA.cookbooks[0].name
	writeCookbook(t, dir, "cookbooks/"+cb, cb, "1.0.0")
	lock := revA
	lock.revision = identifier(t)
	wantForbidden(c.fail(append([]string{"policy", "push", group, lock.write(t, dir)}, as...)...))

	wantEqual(t, "group revision", showPolicyGroup(c, group).Policies[policy].RevisionID, revA.revision)
	if revs := policyRevisions(c, policy); slices.Contains(revs, lock.revision) {
		t.Errorf("the refused push stored revision %s", lock.revision)
	}
}

// testPolicyCreate scaffolds Policyfiles on disk; no server is involved.
func testPolicyCreate(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "policy")
	path := filepath.Join(t.TempDir(), "p.rb")
	wantEqual(t, "create output", c.run("policy", "create", name, "--file", path), "Created Policyfile "+path+"\n")
	body := readFile(t, path)
	for _, want := range []string{"name '" + name + "'", "default_source :supermarket", "run_list '" + name + "::default'"} {
		if !strings.Contains(body, want) {
			t.Errorf("scaffold missing %q:\n%s", want, body)
		}
	}
	if r := c.fail("policy", "create", name, "--file", path); !strings.Contains(r.stderr, "already exists") {
		t.Errorf("a second create should refuse to overwrite: %s", r)
	}
	writeFile(t, path, "stale\n")
	c.run("policy", "create", name, "--file", path, "--force")
	if !strings.Contains(readFile(t, path), "name '"+name+"'") {
		t.Errorf("--force did not overwrite the file")
	}

	// With no --file, the scaffold lands in the working directory.
	c.run("policy", "create", name)
	if !strings.Contains(readFile(t, filepath.Join(c.home, name+".rb")), "name '"+name+"'") {
		t.Errorf("create without --file did not write ./%s.rb", name)
	}
}

// testPolicyInstallErrors checks what install says when it cannot produce a
// lock: no Policyfile, and a cookbook from the Cinc Server, a source the
// resolver does not handle yet.
func testPolicyInstallErrors(t *testing.T, tgt Target, c *cli) {
	missing := filepath.Join(t.TempDir(), "Policyfile.rb")
	if r := c.fail("policy", "install", missing); !strings.Contains(r.stderr, "we couldn't find a Policyfile at "+missing) {
		t.Errorf("install of a missing Policyfile: %s", r)
	}

	requireRuby(t, c)
	dir := t.TempDir()
	cb := uniqueName(t, "cb")
	writeFile(t, filepath.Join(dir, "Policyfile.rb"), fmt.Sprintf(
		"name 'srv'\ndefault_source :chef_server, %q\nrun_list '%s::default'\ncookbook '%s'\n", c.orgURL(tgt.Org), cb, cb))
	r := c.fail("policy", "install", filepath.Join(dir, "Policyfile.rb"))
	if !strings.Contains(r.stderr, cb) || !strings.Contains(r.stderr, "path") {
		t.Errorf("install of a chef_server cookbook should say only path sources resolve: %s", r)
	}
	if _, err := os.Stat(filepath.Join(dir, "Policyfile.lock.json")); err == nil {
		t.Errorf("a failed install wrote a lock")
	}
}

// readJSONFile decodes the JSON file at path into v.
func readJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(readFile(t, path)), v); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}
