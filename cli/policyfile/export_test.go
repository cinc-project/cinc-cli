package policyfile

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// TestCopyTreeSkipsSymlinks pins how a symlink inside a cookbook is treated.
// filepath.Walk stats with Lstat, so a symlink is neither a directory nor
// skipped by default: opening it follows the link and copies whatever it
// points at. For a cookbook fetched from a repository that means a link such
// as "files/creds -> ~/.ssh/id_rsa" lands in the export bundle as a real file
// and is pushed to the server. A dangling link fails the export outright.
//
// cli/cookbook's archiveEntries already skips non-regular files; the export
// path should agree.
func TestCopyTreeSkipsSymlinks(t *testing.T) {
	root := t.TempDir()

	secret := filepath.Join(root, "id_rsa")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY MATERIAL\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(root, "cookbook")
	if err := os.MkdirAll(filepath.Join(src, "files"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "metadata.rb"), []byte("name 'cb'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(src, "files", "innocuous.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// Cookbooks legitimately carry links whose target is absent on this
	// machine; they must not break the export.
	if err := os.Symlink(filepath.Join(root, "absent"), filepath.Join(src, "files", "dangling.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	dst := filepath.Join(root, "export")
	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree failed on a cookbook containing symlinks: %v", err)
	}

	if body, err := os.ReadFile(filepath.Join(dst, "files", "innocuous.txt")); err == nil {
		t.Errorf("symlink was dereferenced into the bundle, exposing %s:\n%s", secret, body)
	}
	if _, err := os.Lstat(filepath.Join(dst, "files", "dangling.txt")); err == nil {
		t.Error("dangling symlink should not have been copied")
	}
	// Regular files still come across.
	if _, err := os.Stat(filepath.Join(dst, "metadata.rb")); err != nil {
		t.Errorf("metadata.rb was not copied: %v", err)
	}
}

func TestExportAssemblesChefCompatibleTree(t *testing.T) {
	lockDir := t.TempDir()
	cbDir := filepath.Join(lockDir, "cookbooks", "mycb", "recipes")
	if err := os.MkdirAll(cbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, "cookbooks", "mycb", "metadata.rb"), []byte("name 'mycb'\nversion '0.1.0'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cbDir, "default.rb"), []byte("log 'hi'\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	lock := &cinc.PolicyRevision{
		Name:       "web",
		RevisionID: "rev1",
		RunList:    []string{"recipe[mycb]"},
		CookbookLocks: map[string]cinc.CookbookLock{
			"mycb": {
				Version:                 "0.1.0",
				Identifier:              "abc123",
				DottedDecimalIdentifier: "1.2.3",
				SourceOptions:           map[string]any{"path": "cookbooks/mycb"},
			},
		},
	}
	lockJSON, _ := json.Marshal(lock)

	dest := filepath.Join(t.TempDir(), "export")
	f := &Fetcher{CacheRoot: filepath.Join(t.TempDir(), "cache"), LockDir: lockDir}
	result, err := Export(context.Background(), f, lock, lockJSON, dest, true)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	// Cookbook copied under cookbooks/<name>-<dotted_decimal_identifier>/.
	if _, err := os.Stat(filepath.Join(dest, "cookbooks", "mycb-1.2.3", "metadata.rb")); err != nil {
		t.Errorf("cookbook metadata not exported: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "cookbooks", "mycb-1.2.3", "recipes", "default.rb")); err != nil {
		t.Errorf("cookbook recipe not exported: %v", err)
	}
	// Lock written in both places.
	if _, err := os.Stat(filepath.Join(dest, "policies", "web-rev1.json")); err != nil {
		t.Errorf("policies/web-rev1.json missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "Policyfile.lock.json")); err != nil {
		t.Errorf("Policyfile.lock.json missing: %v", err)
	}
	// Generated client config selects the policy.
	clientRB, err := os.ReadFile(filepath.Join(dest, "client.rb"))
	if err != nil || !contains(string(clientRB), `policy_name "web"`) {
		t.Errorf("client.rb = %q (err %v), want policy_name \"web\"", clientRB, err)
	}
	// Archive written.
	if result.Archive == "" {
		t.Error("ExportResult.Archive empty despite archive=true")
	}
	if _, err := os.Stat(result.Archive); err != nil {
		t.Errorf("archive not written: %v", err)
	}
}

func TestExportRejectsUnsafeCookbookName(t *testing.T) {
	// A cookbook lock named to escape destDir must be refused, not written
	// outside the export directory.
	lockDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(lockDir, "realcb"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, "realcb", "metadata.rb"), []byte("name 'realcb'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := &cinc.PolicyRevision{
		Name: "p", RevisionID: "r",
		CookbookLocks: map[string]cinc.CookbookLock{
			"../../../../tmp/evil": {Identifier: "abc", SourceOptions: map[string]any{"path": "realcb"}},
		},
	}
	lockJSON, _ := json.Marshal(lock)
	dest := filepath.Join(t.TempDir(), "out")
	f := &Fetcher{CacheRoot: t.TempDir(), LockDir: lockDir}
	if _, err := Export(context.Background(), f, lock, lockJSON, dest, false); err == nil {
		t.Error("expected Export to reject a traversal cookbook name")
	}
}

func TestExportUsesIdentifierWhenNoDottedDecimal(t *testing.T) {
	lockDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(lockDir, "cb"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, "cb", "metadata.rb"), []byte("name 'cb'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := &cinc.PolicyRevision{
		Name: "p", RevisionID: "r",
		CookbookLocks: map[string]cinc.CookbookLock{
			"cb": {Identifier: "deadbeef", SourceOptions: map[string]any{"path": "cb"}},
		},
	}
	lockJSON, _ := json.Marshal(lock)
	dest := filepath.Join(t.TempDir(), "out")
	f := &Fetcher{CacheRoot: t.TempDir(), LockDir: lockDir}
	if _, err := Export(context.Background(), f, lock, lockJSON, dest, false); err != nil {
		t.Fatalf("Export: %v", err)
	}
	// Falls back to the plain identifier for the directory name.
	if _, err := os.Stat(filepath.Join(dest, "cookbooks", "cb-deadbeef")); err != nil {
		t.Errorf("expected cookbooks/cb-deadbeef: %v", err)
	}
}
