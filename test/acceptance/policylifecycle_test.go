//go:build acceptance

package acceptance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
	cliclient "github.com/cinc-project/cinc-cli/cli/client"
	"github.com/cinc-project/cinc-cli/cli/config"
)

// acceptanceClient builds a cinc-api client from the acceptance profile,
// exactly as the CLI does. Tests use it to seed policy revisions and group
// pinnings that the cinc CLI can't yet create on its own (the heavy
// install/update/push verbs are not implemented).
func acceptanceClient(t *testing.T, env acceptanceEnv) *cinc.Client {
	t.Helper()
	cfg, err := config.Load(env.cfgPath)
	if err != nil {
		t.Fatalf("load acceptance config: %v", err)
	}
	profile, err := cfg.Profile("")
	if err != nil {
		t.Fatalf("resolve default profile: %v", err)
	}
	c, err := cliclient.New(profile)
	if err != nil {
		t.Fatalf("build acceptance client: %v", err)
	}
	return c
}

// appserverRevision builds a minimal Policyfile lock document for the seeded
// "appserver" policy at the given revision id, mirroring the seed's shape.
func appserverRevision(revisionID string, runList ...string) map[string]any {
	if len(runList) == 0 {
		runList = []string{"recipe[appserver::default]"}
	}
	return map[string]any{
		"name":           "appserver",
		"revision_id":    revisionID,
		"run_list":       runList,
		"cookbook_locks": map[string]any{},
	}
}

// TestPolicyCreateAgainstCincZero scaffolds a Policyfile with the real binary.
// It is local-only, so it needs the compiled binary but no running server.
func TestPolicyCreateAgainstCincZero(t *testing.T) {
	binary := buildCinc(t)
	path := filepath.Join(t.TempDir(), "appserver.rb")

	out := runCinc(t, binary, "policy", "create", "appserver", "--file", path)
	if !strings.Contains(out, "Created Policyfile") || !strings.Contains(out, path) {
		t.Errorf("policy create output = %q", out)
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

	// A second run without --force must refuse to clobber the file.
	_, stderr, err := runCincRaw(binary, "policy", "create", "appserver", "--file", path)
	if err == nil {
		t.Fatal("second create without --force unexpectedly succeeded")
	}
	if !strings.Contains(stderr, "already exists") {
		t.Errorf("overwrite error did not mention the existing file: %s", stderr)
	}
}

// TestPolicyDiffAgainstCincZero seeds a second policy group pinned to a fresh
// revision, then diffs the two groups and the two revisions directly.
func TestPolicyDiffAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	c := acceptanceClient(t, env)
	// staging gets a new 1.1.0 revision that adds a recipe; prod keeps the
	// seeded 1.0.0. PutPolicy creates the group and the revision in one call.
	rev := appserverRevision("1.1.0", "recipe[appserver::default]", "recipe[appserver::ssl]")
	if _, _, err := c.PolicyGroups.PutPolicy(context.Background(), "staging", "appserver", rev); err != nil {
		t.Fatalf("seed staging revision: %v", err)
	}

	// Group form.
	jsonOut := runCinc(t, env.binary, "policy", "diff", "appserver", "staging", "prod", "--config", env.cfgPath, "--format", "json")
	var d struct {
		From, To struct {
			Ref        string `json:"ref"`
			RevisionID string `json:"revision_id"`
		}
		RunList struct {
			Added   []string `json:"added"`
			Removed []string `json:"removed"`
		} `json:"run_list"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &d); err != nil {
		t.Fatalf("diff (json) not valid JSON: %v\n%s", err, jsonOut)
	}
	if d.From.Ref != "staging" || d.From.RevisionID != "1.1.0" || d.To.Ref != "prod" || d.To.RevisionID != "1.0.0" {
		t.Errorf("diff sides = %+v", d)
	}
	// staging (1.1.0) has the extra recipe; going staging->prod removes it.
	if !contains(d.RunList.Removed, "recipe[appserver::ssl]") {
		t.Errorf("run_list removed = %v, want recipe[appserver::ssl]", d.RunList.Removed)
	}

	// Revisions form against the same two revisions.
	human := runCinc(t, env.binary, "policy", "diff", "appserver", "--revisions", "1.0.0", "1.1.0", "--config", env.cfgPath)
	if !strings.Contains(human, "recipe[appserver::ssl]") {
		t.Errorf("revisions-form diff missing the changed recipe:\n%s", human)
	}
}

// TestPolicyCleanAgainstCincZero seeds an orphaned revision (not in any group)
// and asserts clean removes it while keeping the in-use one.
func TestPolicyCleanAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	c := acceptanceClient(t, env)
	// 1.1.0 exists as a revision but is pinned to no group -> orphaned.
	if _, _, err := c.Policies.CreateRevision(context.Background(), "appserver", appserverRevision("1.1.0")); err != nil {
		t.Fatalf("seed orphaned revision: %v", err)
	}

	// Dry run must not delete anything.
	dry := runCinc(t, env.binary, "policy", "clean", "--dry-run", "--config", env.cfgPath)
	if !strings.Contains(dry, "Would delete") || !strings.Contains(dry, "1.1.0") {
		t.Errorf("dry-run output = %q, want it to name the orphaned 1.1.0", dry)
	}
	if revs := runCinc(t, env.binary, "policy", "show", "appserver", "--config", env.cfgPath); !strings.Contains(revs, "1.1.0") {
		t.Errorf("dry-run deleted the revision: show = %s", revs)
	}

	// Real run deletes the orphan, keeps 1.0.0.
	out := runCinc(t, env.binary, "policy", "clean", "--config", env.cfgPath)
	if !strings.Contains(out, "Deleted") || !strings.Contains(out, "1.1.0") {
		t.Errorf("clean output = %q", out)
	}
	revs := runCinc(t, env.binary, "policy", "show", "appserver", "--config", env.cfgPath, "--format", "json")
	if strings.Contains(revs, "1.1.0") {
		t.Errorf("orphaned revision 1.1.0 survived clean:\n%s", revs)
	}
	if !strings.Contains(revs, "1.0.0") {
		t.Errorf("in-use revision 1.0.0 was removed by clean:\n%s", revs)
	}
}

// TestPolicyCleanCookbooksAgainstCincZero uploads a cookbook artifact that no
// policy revision references, then asserts clean-cookbooks reports it on a dry
// run and deletes it on a real run.
func TestPolicyCleanCookbooksAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	c := acceptanceClient(t, env)

	// Build a throwaway cookbook on disk and upload it as an unreferenced
	// artifact under a known identifier.
	const identifier = "1234567890abcdef1234567890abcdef12345678"
	cbDir := filepath.Join(t.TempDir(), "orphancb")
	if err := os.MkdirAll(filepath.Join(cbDir, "recipes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cbDir, "metadata.rb"), []byte("name 'orphancb'\nversion '1.0.0'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cbDir, "recipes", "default.rb"), []byte("log 'orphan'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cb, err := cinc.LocalCookbookFromDir(cbDir, "1.0.0")
	if err != nil {
		t.Fatalf("load orphan cookbook: %v", err)
	}
	if err := c.CookbookArtifacts.Upload(context.Background(), cb, identifier); err != nil {
		t.Fatalf("upload orphan artifact: %v", err)
	}

	// Dry run names the orphan but must not delete it.
	dry := runCinc(t, env.binary, "policy", "clean-cookbooks", "--dry-run", "--config", env.cfgPath)
	if !strings.Contains(dry, "Would delete") || !strings.Contains(dry, "orphancb@"+identifier) {
		t.Errorf("dry-run output = %q, want it to name the orphaned artifact", dry)
	}
	if _, _, err := c.CookbookArtifacts.Get(context.Background(), "orphancb", identifier); err != nil {
		t.Errorf("dry-run deleted the artifact: %v", err)
	}

	// Real run deletes the orphan.
	out := runCinc(t, env.binary, "policy", "clean-cookbooks", "--config", env.cfgPath)
	if !strings.Contains(out, "Deleted") || !strings.Contains(out, "orphancb@"+identifier) {
		t.Errorf("clean-cookbooks output = %q", out)
	}
	if _, _, err := c.CookbookArtifacts.Get(context.Background(), "orphancb", identifier); err == nil {
		t.Errorf("orphaned artifact orphancb@%s survived clean-cookbooks", identifier)
	}
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}
