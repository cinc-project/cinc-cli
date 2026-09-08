//go:build acceptance

package acceptance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// JSON output is part of what cinc ships, but the acceptance suite asserts it
// mostly on read commands. These tests cover --format json on the mutation,
// push, and clean commands that emit structured results, confirming the
// emitted JSON is valid and carries the expected fields end-to-end.

// TestPolicyPushJSONAgainstCincZero pushes a path-sourced lock with
// --format json and asserts the machine-readable push summary.
func TestPolicyPushJSONAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	const identifier = "abcdef0123456789abcdef0123456789abcdef01"
	lockPath := writeDeployFixture(t, "jsonpush", identifier)

	out := runCinc(t, env.binary, "policy", "push", "jsonqa", lockPath,
		"--config", env.cfgPath, "--format", "json")

	var res struct {
		Policy            string `json:"policy"`
		Group             string `json:"group"`
		RevisionID        string `json:"revision_id"`
		CookbooksUploaded int    `json:"cookbooks_uploaded"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("policy push (json) not valid JSON: %v\n%s", err, out)
	}
	if res.Policy != "jsonpush" || res.Group != "jsonqa" || res.RevisionID != identifier {
		t.Errorf("policy push json = %+v, want policy=jsonpush group=jsonqa revision_id=%s", res, identifier)
	}
	if res.CookbooksUploaded != 1 {
		t.Errorf("policy push json cookbooks_uploaded = %d, want 1", res.CookbooksUploaded)
	}
}

// TestCookbookUploadJSONAgainstCincZero uploads a cookbook with --format json
// and asserts the per-cookbook result array.
func TestCookbookUploadJSONAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	cookbookPath := writeAcceptanceCookbook(t, "nginx")
	out := runCinc(t, env.binary, "cookbook", "upload", "nginx",
		"--cookbook-path", cookbookPath, "--config", env.cfgPath, "--format", "json")

	var results []struct {
		Cookbook string `json:"cookbook"`
		Version  string `json:"version"`
		Uploaded bool   `json:"uploaded"`
	}
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("cookbook upload (json) not valid JSON: %v\n%s", err, out)
	}
	if len(results) != 1 {
		t.Fatalf("cookbook upload json returned %d results, want 1:\n%s", len(results), out)
	}
	if results[0].Cookbook != "nginx" || results[0].Version != "1.2.0" || !results[0].Uploaded {
		t.Errorf("cookbook upload json = %+v, want nginx/1.2.0/uploaded=true", results[0])
	}
}

// TestPolicyCleanCookbooksJSONAgainstCincZero uploads an unreferenced cookbook
// artifact, then runs `policy clean-cookbooks --dry-run --format json` and
// asserts the structured report names the orphan.
func TestPolicyCleanCookbooksJSONAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	c := acceptanceClient(t, env)

	const identifier = "fedcba9876543210fedcba9876543210fedcba98"
	cbDir := filepath.Join(t.TempDir(), "orphanjson")
	if err := os.MkdirAll(filepath.Join(cbDir, "recipes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cbDir, "metadata.rb"), []byte("name 'orphanjson'\nversion '1.0.0'\n"), 0o644); err != nil {
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

	out := runCinc(t, env.binary, "policy", "clean-cookbooks", "--dry-run",
		"--config", env.cfgPath, "--format", "json")

	var report struct {
		DryRun  bool `json:"dry_run"`
		Deleted []struct {
			Name       string `json:"name"`
			Identifier string `json:"identifier"`
		} `json:"deleted"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("clean-cookbooks (json) not valid JSON: %v\n%s", err, out)
	}
	if !report.DryRun {
		t.Errorf("clean-cookbooks json dry_run = false, want true (ran with --dry-run)")
	}
	found := false
	for _, d := range report.Deleted {
		if d.Name == "orphanjson" && d.Identifier == identifier {
			found = true
		}
	}
	if !found {
		t.Errorf("clean-cookbooks json deleted = %+v, want it to name orphanjson@%s", report.Deleted, identifier)
	}
}
