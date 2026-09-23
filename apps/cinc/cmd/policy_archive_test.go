package cmd

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pushArchiveServer mimics the server endpoints a push needs: the sandbox
// upload dance, the cookbook-artifact PUT, and the policy-group association.
// It flips uploadedArtifact/associated so tests can assert the bundle's
// cookbook and revision landed.
func pushArchiveServer(t *testing.T, identifier string, uploadedArtifact, associated *bool, associateBody *[]byte) *httptest.Server {
	t.Helper()
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
		*uploadedArtifact = true
		_, _ = io.WriteString(w, `{}`)
	})
	mux.HandleFunc("/organizations/acme/policy_groups/prod/policies/appserver", func(w http.ResponseWriter, r *http.Request) {
		*associated = true
		*associateBody, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, `{"revision_id":"rev123","name":"appserver"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// exportBundle runs `cinc policy export` on a path-sourced lock to produce a
// real bundle directory and its .tar.gz, returning both paths.
func exportBundle(t *testing.T, identifier string) (dir, archive string) {
	t.Helper()
	lockPath := writePolicyLockFixture(t, identifier)
	dir = filepath.Join(t.TempDir(), "appserver")
	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"policy", "export", lockPath, dir, "--archive", "--config", writeCreateConfig(t, "http://127.0.0.1:0")})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc policy export: %v", err)
	}
	return dir, dir + ".tar.gz"
}

func TestPolicyPushArchiveCommandDirectoryInput(t *testing.T) {
	const identifier = "0000000000000000000000000000000000000001"
	var uploadedArtifact, associated bool
	var associateBody []byte
	srv := pushArchiveServer(t, identifier, &uploadedArtifact, &associated, &associateBody)

	bundleDir, _ := exportBundle(t, identifier)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"policy", "push-archive", "prod", bundleDir, "--config", writeCreateConfig(t, srv.URL)})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc policy push-archive (dir): %v", err)
	}
	if !uploadedArtifact {
		t.Error("cookbook artifact from the bundle was not uploaded")
	}
	if !associated {
		t.Fatal("revision was not associated with the policy group")
	}
	if !strings.Contains(string(associateBody), "dotted_decimal_identifier") {
		t.Errorf("associate body missing lock fields: %s", associateBody)
	}
	if out := buf.String(); !strings.Contains(out, "Pushed policy \"appserver\"") || !strings.Contains(out, "group \"prod\"") {
		t.Errorf("output = %q", out)
	}
}

func TestPolicyPushArchiveCommandTarballInput(t *testing.T) {
	const identifier = "0000000000000000000000000000000000000002"
	var uploadedArtifact, associated bool
	var associateBody []byte
	srv := pushArchiveServer(t, identifier, &uploadedArtifact, &associated, &associateBody)

	_, archive := exportBundle(t, identifier)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"policy", "push-archive", "prod", archive, "--config", writeCreateConfig(t, srv.URL)})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc policy push-archive (tarball): %v", err)
	}
	if !uploadedArtifact {
		t.Error("cookbook artifact from the tarball was not uploaded")
	}
	if !associated {
		t.Fatal("revision from the tarball was not associated with the policy group")
	}
	if out := buf.String(); !strings.Contains(out, "Pushed policy \"appserver\"") {
		t.Errorf("output = %q", out)
	}
}

// TestPolicyPushArchiveDefaultsToCwdArchive omits the archive argument; the
// command should discover the single .tar.gz in the working directory.
func TestPolicyPushArchiveDefaultsToCwdArchive(t *testing.T) {
	const identifier = "0000000000000000000000000000000000000003"
	var uploadedArtifact, associated bool
	var associateBody []byte
	srv := pushArchiveServer(t, identifier, &uploadedArtifact, &associated, &associateBody)

	_, archive := exportBundle(t, identifier)
	work := t.TempDir()
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "appserver.tar.gz"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(work)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"policy", "push-archive", "prod", "--config", writeCreateConfig(t, srv.URL)})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc policy push-archive (default cwd): %v", err)
	}
	if !uploadedArtifact || !associated {
		t.Errorf("default-archive push did not deploy: uploaded=%v associated=%v", uploadedArtifact, associated)
	}
}

func TestPolicyPushArchiveReportsMissingArchive(t *testing.T) {
	t.Chdir(t.TempDir()) // empty: no bundle, no .tar.gz

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"policy", "push-archive", "prod", "--config", writeCreateConfig(t, "http://127.0.0.1:0")})
	if err := root.Execute(); err == nil {
		t.Error("expected an error when no bundle can be found to push")
	}
}
