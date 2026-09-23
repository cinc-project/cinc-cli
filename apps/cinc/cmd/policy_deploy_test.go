package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePolicyLockFixture creates a lock dir holding a path-sourced cookbook and
// a Policyfile.lock.json that pins it, returning the lock file path.
func writePolicyLockFixture(t *testing.T, identifier string) string {
	t.Helper()
	dir := t.TempDir()
	cb := filepath.Join(dir, "cookbooks", "base", "recipes")
	if err := os.MkdirAll(cb, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cookbooks", "base", "metadata.rb"), []byte("name 'base'\nversion '1.0.0'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cb, "default.rb"), []byte("log 'base'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := map[string]any{
		"name":        "appserver",
		"revision_id": "rev123",
		"run_list":    []string{"recipe[base]"},
		"cookbook_locks": map[string]any{
			"base": map[string]any{
				"version":                   "1.0.0",
				"identifier":                identifier,
				"dotted_decimal_identifier": "1.2.3",
				"source_options":            map[string]any{"path": "cookbooks/base"},
			},
		},
	}
	data, _ := json.MarshalIndent(lock, "", "  ")
	lockPath := filepath.Join(dir, "Policyfile.lock.json")
	if err := os.WriteFile(lockPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return lockPath
}

func TestPolicyPushCommandEndToEnd(t *testing.T) {
	const identifier = "0000000000000000000000000000000000000001"
	var uploadedArtifact, associated bool
	var associateBody []byte
	mux := http.NewServeMux()
	// PushRevision lists the server's artifacts first and uploads only the
	// identifiers it lacks; an empty listing means every cookbook is sent.
	mux.HandleFunc("/organizations/acme/cookbook_artifacts", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	})
	mux.HandleFunc("/organizations/acme/sandboxes", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"sandbox_id":"sb","checksums":{}}`)
	})
	mux.HandleFunc("/organizations/acme/sandboxes/sb", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	})
	mux.HandleFunc("/organizations/acme/cookbook_artifacts/base/"+identifier, func(w http.ResponseWriter, _ *http.Request) {
		uploadedArtifact = true
		_, _ = io.WriteString(w, `{}`)
	})
	mux.HandleFunc("/organizations/acme/policy_groups/prod/policies/appserver", func(w http.ResponseWriter, r *http.Request) {
		associated = true
		associateBody, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, `{"revision_id":"rev123","name":"appserver"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	lockPath := writePolicyLockFixture(t, identifier)
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"policy", "push", "prod", lockPath, "--config", writeCreateConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc policy push: %v", err)
	}
	if !uploadedArtifact {
		t.Error("cookbook artifact was not uploaded")
	}
	if !associated {
		t.Fatal("revision was not associated with the policy group")
	}
	// The lock is sent verbatim (its dotted_decimal_identifier survives).
	if !strings.Contains(string(associateBody), "dotted_decimal_identifier") {
		t.Errorf("associate body missing lock fields: %s", associateBody)
	}
	if out := buf.String(); !strings.Contains(out, "Pushed policy \"appserver\"") || !strings.Contains(out, "group \"prod\"") {
		t.Errorf("output = %q", out)
	}
}

// TestPolicyPushUploadsUnderLockName covers a cookbook whose directory name is
// not its cookbook name (a cache entry, or a path source like
// cookbooks/base-cookbook) and whose metadata.rb names it with a non-literal
// that cinc-api's static parser can't read. The artifact must still land
// under the name the lock records.
func TestPolicyPushUploadsUnderLockName(t *testing.T) {
	const identifier = "0000000000000000000000000000000000000002"
	var uploadedArtifact bool
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/cookbook_artifacts", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	})
	mux.HandleFunc("/organizations/acme/sandboxes", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"sandbox_id":"sb","checksums":{}}`)
	})
	mux.HandleFunc("/organizations/acme/sandboxes/sb", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	})
	mux.HandleFunc("/organizations/acme/cookbook_artifacts/base/"+identifier, func(w http.ResponseWriter, _ *http.Request) {
		uploadedArtifact = true
		_, _ = io.WriteString(w, `{}`)
	})
	mux.HandleFunc("/organizations/acme/policy_groups/prod/policies/appserver", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"revision_id":"rev123","name":"appserver"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	lockPath := writePolicyLockFixture(t, identifier)
	dir := filepath.Dir(lockPath)
	if err := os.Rename(filepath.Join(dir, "cookbooks", "base"), filepath.Join(dir, "cookbooks", "base-cookbook")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cookbooks", "base-cookbook", "metadata.rb"), []byte("name 'base'.dup\nversion '1.0.0'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, bytes.ReplaceAll(data, []byte(`"cookbooks/base"`), []byte(`"cookbooks/base-cookbook"`)), 0o644); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"policy", "push", "prod", lockPath, "--config", writeCreateConfig(t, srv.URL)})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc policy push: %v", err)
	}
	if !uploadedArtifact {
		t.Error("cookbook artifact was not uploaded under the lock's cookbook name")
	}
}

// TestPolicyPushReportsOnlyWhatItUploaded pushes a lock whose artifact the
// server already holds (a second push of one lock, to another group). The
// push uploads nothing, so the summary must not claim it uploaded the
// cookbook.
func TestPolicyPushReportsOnlyWhatItUploaded(t *testing.T) {
	const identifier = "0000000000000000000000000000000000000003"
	var uploadedArtifact bool
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/cookbook_artifacts", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"base":{"url":"u","versions":[{"identifier":"`+identifier+`","url":"u"}]}}`)
	})
	mux.HandleFunc("/organizations/acme/cookbook_artifacts/base/"+identifier, func(w http.ResponseWriter, _ *http.Request) {
		uploadedArtifact = true
		w.WriteHeader(http.StatusConflict)
	})
	mux.HandleFunc("/organizations/acme/policy_groups/prod/policies/appserver", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"revision_id":"rev123","name":"appserver"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	lockPath := writePolicyLockFixture(t, identifier)

	for _, tc := range []struct {
		format string
		want   string
	}{
		{"human", "Pushed policy \"appserver\" (revision rev123) to group \"prod\" with 1 cookbook(s) (0 uploaded, 1 already on the server)\n"},
		{"json", `"cookbooks_uploaded": 0`},
	} {
		t.Run(tc.format, func(t *testing.T) {
			root := newRootCmd()
			var buf bytes.Buffer
			root.SetOut(&buf)
			root.SetArgs([]string{"policy", "push", "prod", lockPath, "--format", tc.format, "--config", writeCreateConfig(t, srv.URL)})
			if err := root.Execute(); err != nil {
				t.Fatalf("cinc policy push: %v", err)
			}
			if !strings.Contains(buf.String(), tc.want) {
				t.Errorf("output = %q, want it to contain %q", buf.String(), tc.want)
			}
			if tc.format == "json" && !strings.Contains(buf.String(), `"cookbooks": 1`) {
				t.Errorf("json output = %q, want the lock's cookbook count too", buf.String())
			}
		})
	}
	if uploadedArtifact {
		t.Error("the artifact the server already had was uploaded again")
	}
}

func TestPolicyPushCommandReportsMissingLock(t *testing.T) {
	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	missing := filepath.Join(t.TempDir(), "Policyfile.lock.json")
	root.SetArgs([]string{"policy", "push", "prod", missing, "--config", writeCreateConfig(t, "http://127.0.0.1:0")})
	if err := root.Execute(); err == nil {
		t.Error("expected an error when the lock file is missing")
	}
}

func TestPolicyExportCommandEndToEnd(t *testing.T) {
	// Export of a path-sourced lock needs no server.
	lockPath := writePolicyLockFixture(t, "0000000000000000000000000000000000000002")
	destDir := filepath.Join(t.TempDir(), "bundle")

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"policy", "export", lockPath, destDir, "--archive", "--config", writeCreateConfig(t, "http://127.0.0.1:0")})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc policy export: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destDir, "cookbooks", "base-1.2.3", "metadata.rb")); err != nil {
		t.Errorf("exported cookbook missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destDir, "Policyfile.lock.json")); err != nil {
		t.Errorf("exported lock missing: %v", err)
	}
	if _, err := os.Stat(destDir + ".tar.gz"); err != nil {
		t.Errorf("archive missing: %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "Exported policy \"appserver\"") {
		t.Errorf("output = %q", out)
	}
}
