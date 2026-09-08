package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func writePolicyConfig(t *testing.T, serverURL string) string {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "credentials")
	cfg := fmt.Sprintf(`[default]
cinc_server_url = "%s/organizations/acme"
client_name     = "tim"
client_key      = %q
`, serverURL, writeTestKey(t))
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

// --- pure diff function -------------------------------------------------

func TestComputePolicyDiffCookbookVersionChange(t *testing.T) {
	a := &cinc.PolicyRevision{
		RevisionID:    "1.1.0",
		CookbookLocks: map[string]cinc.CookbookLock{"web": {Version: "1.3.0", Identifier: "aaa"}},
	}
	b := &cinc.PolicyRevision{
		RevisionID:    "1.0.0",
		CookbookLocks: map[string]cinc.CookbookLock{"web": {Version: "1.2.0", Identifier: "bbb"}},
	}
	d := computePolicyDiff("appserver", "staging", "prod", a, b)

	if d.From.Ref != "staging" || d.From.RevisionID != "1.1.0" {
		t.Errorf("from = %+v", d.From)
	}
	if d.To.Ref != "prod" || d.To.RevisionID != "1.0.0" {
		t.Errorf("to = %+v", d.To)
	}
	if len(d.Cookbooks) != 1 {
		t.Fatalf("cookbooks = %+v, want one change", d.Cookbooks)
	}
	got := d.Cookbooks[0]
	if got.Name != "web" || got.From != "1.3.0" || got.To != "1.2.0" {
		t.Errorf("cookbook delta = %+v, want web 1.3.0->1.2.0 (version wins)", got)
	}
}

func TestComputePolicyDiffIdentifierOnlyChange(t *testing.T) {
	a := &cinc.PolicyRevision{CookbookLocks: map[string]cinc.CookbookLock{"web": {Version: "1.0.0", Identifier: "aaaaaa"}}}
	b := &cinc.PolicyRevision{CookbookLocks: map[string]cinc.CookbookLock{"web": {Version: "1.0.0", Identifier: "bbbbbb"}}}
	d := computePolicyDiff("p", "x", "y", a, b)
	if len(d.Cookbooks) != 1 || d.Cookbooks[0].From != "aaaaaa" || d.Cookbooks[0].To != "bbbbbb" {
		t.Errorf("identifier-only change = %+v, want aaaaaa->bbbbbb", d.Cookbooks)
	}
}

func TestComputePolicyDiffAddedRemovedCookbook(t *testing.T) {
	a := &cinc.PolicyRevision{CookbookLocks: map[string]cinc.CookbookLock{"base": {Version: "1.0.0"}}}
	b := &cinc.PolicyRevision{CookbookLocks: map[string]cinc.CookbookLock{"web": {Version: "2.0.0"}}}
	d := computePolicyDiff("p", "a", "b", a, b)

	var added, removed *cookbookDelta
	for i := range d.Cookbooks {
		switch d.Cookbooks[i].Name {
		case "web":
			added = &d.Cookbooks[i]
		case "base":
			removed = &d.Cookbooks[i]
		}
	}
	if added == nil || added.From != "" || added.To != "2.0.0" {
		t.Errorf("added cookbook = %+v, want web ->2.0.0", added)
	}
	if removed == nil || removed.From != "1.0.0" || removed.To != "" {
		t.Errorf("removed cookbook = %+v, want base 1.0.0->", removed)
	}
}

func TestComputePolicyDiffRunListAndAttributes(t *testing.T) {
	a := &cinc.PolicyRevision{
		RunList:           []string{"recipe[base]"},
		DefaultAttributes: map[string]any{"web": map[string]any{"port": float64(8080)}},
	}
	b := &cinc.PolicyRevision{
		RunList:           []string{"recipe[base]", "recipe[web::ssl]"},
		DefaultAttributes: map[string]any{"web": map[string]any{"port": float64(443)}},
	}
	d := computePolicyDiff("p", "a", "b", a, b)

	if !slices.Equal(d.RunList.Added, []string{"recipe[web::ssl]"}) {
		t.Errorf("run_list added = %v", d.RunList.Added)
	}
	if len(d.RunList.Removed) != 0 {
		t.Errorf("run_list removed = %v, want none", d.RunList.Removed)
	}
	if len(d.Attributes) != 1 {
		t.Fatalf("attributes = %+v, want one changed leaf", d.Attributes)
	}
	at := d.Attributes[0]
	if at.Path != "default['web']['port']" || at.From != float64(8080) || at.To != float64(443) {
		t.Errorf("attribute delta = %+v", at)
	}
}

func TestComputePolicyDiffIdentical(t *testing.T) {
	r := &cinc.PolicyRevision{
		RunList:       []string{"recipe[base]"},
		CookbookLocks: map[string]cinc.CookbookLock{"base": {Version: "1.0.0"}},
	}
	d := computePolicyDiff("p", "a", "b", r, r)
	if !d.empty() {
		t.Errorf("identical revisions should produce an empty diff, got %+v", d)
	}
}

// --- create -------------------------------------------------------------

func TestPolicyCreateCommandWritesScaffold(t *testing.T) {
	path := filepath.Join(t.TempDir(), "appserver.rb")

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"policy", "create", "appserver", "--file", path})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc policy create: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("scaffold not written: %v", err)
	}
	for _, want := range []string{"name 'appserver'", "default_source :supermarket", "run_list 'appserver::default'"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("scaffold missing %q:\n%s", want, body)
		}
	}
	if got := buf.String(); !strings.Contains(got, "Created Policyfile") || !strings.Contains(got, path) {
		t.Errorf("create output = %q", got)
	}
}

func TestPolicyCreateCommandRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "appserver.rb")
	if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"policy", "create", "appserver", "--file", path})

	if err := root.Execute(); err == nil {
		t.Fatal("expected an error when the target file already exists")
	}
	if body, _ := os.ReadFile(path); string(body) != "existing" {
		t.Errorf("file was overwritten without --force: %q", body)
	}
}

func TestPolicyCreateCommandForceOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "appserver.rb")
	if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"policy", "create", "appserver", "--file", path, "--force"})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc policy create --force: %v", err)
	}
	if body, _ := os.ReadFile(path); !strings.Contains(string(body), "name 'appserver'") {
		t.Errorf("--force did not overwrite: %q", body)
	}
}

// --- diff (command) -----------------------------------------------------

// policyDiffServer serves policy revisions and, optionally, policy-group
// pinnings for the diff command tests.
func policyDiffServer(t *testing.T, revisions map[string]cinc.PolicyRevision, groups map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for rev, doc := range revisions {
		mux.HandleFunc("/organizations/acme/policies/appserver/revisions/"+rev, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(doc)
		})
	}
	for group, rev := range groups {
		mux.HandleFunc("/organizations/acme/policy_groups/"+group, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(cinc.PolicyGroup{
				Policies: map[string]cinc.PolicyAssignment{"appserver": {RevisionID: rev}},
			})
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestPolicyDiffCommandGroupsForm(t *testing.T) {
	revisions := map[string]cinc.PolicyRevision{
		"1.1.0": {RevisionID: "1.1.0", CookbookLocks: map[string]cinc.CookbookLock{"web": {Version: "1.3.0"}}},
		"1.0.0": {RevisionID: "1.0.0", CookbookLocks: map[string]cinc.CookbookLock{"web": {Version: "1.2.0"}}},
	}
	srv := policyDiffServer(t, revisions, map[string]string{"staging": "1.1.0", "prod": "1.0.0"})

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"policy", "diff", "appserver", "staging", "prod", "--config", writePolicyConfig(t, srv.URL), "--format", "json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc policy diff (groups): %v", err)
	}
	var d policyDiff
	if err := json.Unmarshal(buf.Bytes(), &d); err != nil {
		t.Fatalf("diff output not valid JSON: %v\n%s", err, buf.String())
	}
	if d.From.Ref != "staging" || d.To.Ref != "prod" {
		t.Errorf("refs = %+v / %+v", d.From, d.To)
	}
	if len(d.Cookbooks) != 1 || d.Cookbooks[0].From != "1.3.0" || d.Cookbooks[0].To != "1.2.0" {
		t.Errorf("cookbooks = %+v", d.Cookbooks)
	}
}

func TestPolicyDiffCommandRevisionsForm(t *testing.T) {
	revisions := map[string]cinc.PolicyRevision{
		"1.0.0": {RevisionID: "1.0.0", RunList: []string{"recipe[base]"}},
		"1.1.0": {RevisionID: "1.1.0", RunList: []string{"recipe[base]", "recipe[web::ssl]"}},
	}
	srv := policyDiffServer(t, revisions, nil)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"policy", "diff", "appserver", "--revisions", "1.0.0", "1.1.0", "--config", writePolicyConfig(t, srv.URL), "--format", "json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc policy diff (revisions): %v", err)
	}
	var d policyDiff
	if err := json.Unmarshal(buf.Bytes(), &d); err != nil {
		t.Fatalf("diff output not valid JSON: %v\n%s", err, buf.String())
	}
	if d.From.Ref != "1.0.0" || d.To.Ref != "1.1.0" {
		t.Errorf("refs = %+v / %+v", d.From, d.To)
	}
	if !slices.Equal(d.RunList.Added, []string{"recipe[web::ssl]"}) {
		t.Errorf("run_list added = %v", d.RunList.Added)
	}
}

func TestPolicyDiffCommandRejectsMixedForms(t *testing.T) {
	srv := policyDiffServer(t, map[string]cinc.PolicyRevision{"1.0.0": {RevisionID: "1.0.0"}}, nil)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	// --revisions plus positional group args is contradictory.
	root.SetArgs([]string{"policy", "diff", "appserver", "staging", "--revisions", "1.0.0", "1.1.0", "--config", writePolicyConfig(t, srv.URL)})

	if err := root.Execute(); err == nil {
		t.Fatal("expected an error when mixing group args with --revisions")
	}
}

// --- clean --------------------------------------------------------------

// policyCleanServer serves the policy-group and policy indexes and records
// which revisions get deleted.
func policyCleanServer(t *testing.T, deleted *[]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/policy_groups", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]cinc.PolicyGroup{
			"prod": {Policies: map[string]cinc.PolicyAssignment{"appserver": {RevisionID: "1.0.0"}}},
		})
	})
	mux.HandleFunc("/organizations/acme/policies", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]cinc.PolicyListEntry{
			"appserver": {
				URI: "https://example.test/policies/appserver",
				Revisions: map[string]json.RawMessage{
					"1.0.0": json.RawMessage(`{}`),
					"1.1.0": json.RawMessage(`{}`),
					"0.9.0": json.RawMessage(`{}`),
				},
			},
		})
	})
	mux.HandleFunc("/organizations/acme/policies/appserver/revisions/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q on revision, want DELETE", r.Method)
		}
		rev := strings.TrimPrefix(r.URL.Path, "/organizations/acme/policies/appserver/revisions/")
		*deleted = append(*deleted, rev)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"revision_id": rev})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestPolicyCleanCommandDeletesOrphans(t *testing.T) {
	var deleted []string
	srv := policyCleanServer(t, &deleted)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"policy", "clean", "--config", writePolicyConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc policy clean: %v", err)
	}
	slices.Sort(deleted)
	if !slices.Equal(deleted, []string{"0.9.0", "1.1.0"}) {
		t.Errorf("deleted = %v, want the two orphaned revisions (1.0.0 is in use)", deleted)
	}
	if strings.Contains(buf.String(), "1.0.0") && !strings.Contains(buf.String(), "Kept") {
		t.Errorf("in-use revision 1.0.0 should not be reported as deleted:\n%s", buf.String())
	}
}

func TestPolicyCleanCommandDryRun(t *testing.T) {
	var deleted []string
	srv := policyCleanServer(t, &deleted)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"policy", "clean", "--dry-run", "--config", writePolicyConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc policy clean --dry-run: %v", err)
	}
	if len(deleted) != 0 {
		t.Errorf("--dry-run deleted revisions on the server: %v", deleted)
	}
	if !strings.Contains(buf.String(), "Would delete") {
		t.Errorf("--dry-run output = %q, want a 'Would delete' report", buf.String())
	}
}

// --- clean-cookbooks ----------------------------------------------------

// cleanCookbooksServer serves a two-policy/two-revision world plus a cookbook
// artifact index. The revisions reference some (cookbook, identifier) pairs;
// the artifact list adds extra "orphan" identifiers no revision points at. It
// records every DELETE so tests can assert exactly the orphans were removed.
func cleanCookbooksServer(t *testing.T, deleted *[]string) *httptest.Server {
	t.Helper()
	// referenced identifiers, per cookbook.
	const (
		baseRef = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		webRef  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		baseOld = "cccccccccccccccccccccccccccccccccccccccc" // orphan
		webOld  = "dddddddddddddddddddddddddddddddddddddddd" // orphan
	)
	revisions := map[string]map[string]cinc.PolicyRevision{
		"appserver": {
			"1.0.0": {RevisionID: "1.0.0", CookbookLocks: map[string]cinc.CookbookLock{
				"base": {Version: "1.0.0", Identifier: baseRef},
				"web":  {Version: "2.0.0", Identifier: webRef},
			}},
		},
		"db": {
			"1.0.0": {RevisionID: "1.0.0", CookbookLocks: map[string]cinc.CookbookLock{
				"base": {Version: "1.0.0", Identifier: baseRef},
			}},
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/policies", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]cinc.PolicyListEntry{
			"appserver": {Revisions: map[string]json.RawMessage{"1.0.0": json.RawMessage(`{}`)}},
			"db":        {Revisions: map[string]json.RawMessage{"1.0.0": json.RawMessage(`{}`)}},
		})
	})
	for policy, revs := range revisions {
		mux.HandleFunc("/organizations/acme/policies/"+policy, func(w http.ResponseWriter, _ *http.Request) {
			out := cinc.PolicyRevisions{Revisions: map[string]json.RawMessage{}}
			for rev := range revs {
				out.Revisions[rev] = json.RawMessage(`{}`)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)
		})
		mux.HandleFunc("/organizations/acme/policies/"+policy+"/revisions/", func(w http.ResponseWriter, r *http.Request) {
			rev := strings.TrimPrefix(r.URL.Path, "/organizations/acme/policies/"+policy+"/revisions/")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(revs[rev])
		})
	}
	mux.HandleFunc("/organizations/acme/cookbook_artifacts", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]cinc.CookbookArtifactListEntry{
			"base": {Versions: []cinc.CookbookArtifactVersion{{Identifier: baseRef}, {Identifier: baseOld}}},
			"web":  {Versions: []cinc.CookbookArtifactVersion{{Identifier: webRef}, {Identifier: webOld}}},
		})
	})
	mux.HandleFunc("/organizations/acme/cookbook_artifacts/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q on artifact, want DELETE", r.Method)
		}
		*deleted = append(*deleted, strings.TrimPrefix(r.URL.Path, "/organizations/acme/cookbook_artifacts/"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestPolicyCleanCookbooksDeletesOrphans(t *testing.T) {
	var deleted []string
	srv := cleanCookbooksServer(t, &deleted)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"policy", "clean-cookbooks", "--config", writePolicyConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc policy clean-cookbooks: %v", err)
	}
	slices.Sort(deleted)
	want := []string{
		"base/cccccccccccccccccccccccccccccccccccccccc",
		"web/dddddddddddddddddddddddddddddddddddddddd",
	}
	if !slices.Equal(deleted, want) {
		t.Errorf("deleted = %v, want only the two orphaned artifacts %v", deleted, want)
	}
	// Output is sorted by name then identifier and lists only orphans.
	out := buf.String()
	if !strings.Contains(out, "base@cccc") || !strings.Contains(out, "web@dddd") {
		t.Errorf("output missing orphan listing:\n%s", out)
	}
	if strings.Contains(out, "@aaaa") || strings.Contains(out, "@bbbb") {
		t.Errorf("referenced artifacts should not appear in output:\n%s", out)
	}
	if i, j := strings.Index(out, "base@"), strings.Index(out, "web@"); i < 0 || j < 0 || i > j {
		t.Errorf("output not sorted (base should precede web):\n%s", out)
	}
}

func TestPolicyCleanCookbooksDryRun(t *testing.T) {
	var deleted []string
	srv := cleanCookbooksServer(t, &deleted)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"policy", "clean-cookbooks", "--dry-run", "--config", writePolicyConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc policy clean-cookbooks --dry-run: %v", err)
	}
	if len(deleted) != 0 {
		t.Errorf("--dry-run deleted artifacts on the server: %v", deleted)
	}
	if !strings.Contains(buf.String(), "Would delete") {
		t.Errorf("--dry-run output = %q, want a 'Would delete' report", buf.String())
	}
}
