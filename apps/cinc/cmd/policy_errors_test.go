package cmd

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func wantErrMentions(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got no error, want one mentioning %q", wants)
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q:\n%s", want, err)
		}
	}
}

// A missing lock is the first thing a new user hits after `policy create`:
// the error says what a lock is and which command writes one.
func TestPolicyPushExplainsAMissingLock(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "Policyfile.lock.json")
	_, _, err := runRoot(t, "policy", "push", "prod", missing, "--config", writeCreateConfig(t, "http://127.0.0.1:0"))
	wantErrMentions(t, err, missing, "cinc policy install")
}

func TestPolicyExportExplainsAMissingLock(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "Policyfile.lock.json")
	_, _, err := runRoot(t, "policy", "export", missing, filepath.Join(t.TempDir(), "out"))
	wantErrMentions(t, err, missing, "cinc policy install")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("errors.Is(err, fs.ErrNotExist) = false for %v", err)
	}
}

func TestPolicyCreateExplainsAnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "appserver.rb")
	if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := runRoot(t, "policy", "create", "appserver", "--file", path)
	wantErrMentions(t, err, path, "already exists", "--force", "--file")
}

func TestPolicyInstallExplainsAnUnwritableLock(t *testing.T) {
	requireRubyEngine(t)
	dir, path := writePolicyfile(t, "name 'app'\nrun_list 'app::default'\ncookbook 'app', path: 'cookbooks/app'\n")
	writeCookbook(t, dir, "app", "1.0.0")
	out := filepath.Join(dir, "missing-dir", "Policyfile.lock.json")
	_, _, err := runRoot(t, "policy", "install", path, "--output", out)
	wantErrMentions(t, err, out, "--output")
}

func TestPolicyDiffExplainsMissingRefs(t *testing.T) {
	_, _, err := runRoot(t, "policy", "diff", "appserver", "staging")
	wantErrMentions(t, err, "give two policy group names", "cinc policy diff appserver staging production", "--revisions")
	_, _, err = runRoot(t, "policy", "diff", "appserver", "--revisions", "abc")
	wantErrMentions(t, err, "--revisions takes exactly two revision ids", "cinc policy show appserver")
}

func TestPolicyDiffExplainsAGroupWithoutThePolicy(t *testing.T) {
	revisions := map[string]cinc.PolicyRevision{"1.0.0": {RevisionID: "1.0.0"}}
	srv, _ := policyDiffServer(t, revisions, map[string]string{"staging": "1.0.0", "empty": ""})
	_, _, err := runRoot(t, "policy", "diff", "appserver", "staging", "empty", "--config", writePolicyConfig(t, srv.URL))
	wantErrMentions(t, err, `policy "appserver" is not assigned to group "empty"`, "cinc policy-group show empty", "cinc policy push empty")
}
