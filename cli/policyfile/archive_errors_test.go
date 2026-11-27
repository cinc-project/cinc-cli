package policyfile

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func TestOpenBundleExplainsADirectoryThatIsNoBundle(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, cleanup, err := OpenBundle(dir)
	defer cleanup()
	wantAll(t, err, dir, BundleLockName, "policy export")
}

func TestOpenBundleExplainsAnArchiveThatIsNoBundle(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "README"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "other.tar.gz")
	if err := tarGzDir(src, archive); err != nil {
		t.Fatal(err)
	}
	_, cleanup, err := OpenBundle(archive)
	defer cleanup()
	wantAll(t, err, archive, BundleLockName, "policy export")
}

func TestOpenBundleExplainsAFileThatIsNoArchive(t *testing.T) {
	notArchive := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if err := os.WriteFile(notArchive, []byte("just text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, cleanup, err := OpenBundle(notArchive)
	defer cleanup()
	wantAll(t, err, notArchive, "gzip")
}

func TestLoadBundleCookbooksExplainsAMissingCookbook(t *testing.T) {
	dir, lock := writeBundle(t)
	if err := os.RemoveAll(filepath.Join(dir, "cookbooks", "base-1.2.3")); err != nil {
		t.Fatal(err)
	}
	_, err := LoadBundleCookbooks(dir, lock)
	wantAll(t, err, `cookbook "base"`, filepath.Join("cookbooks", "base-1.2.3"), "policy export")
}

func TestExportExplainsAnUnsafePolicyName(t *testing.T) {
	f := &Fetcher{CacheRoot: t.TempDir(), LockDir: t.TempDir()}
	lock := &cinc.PolicyRevision{Name: "../evil", RevisionID: "abc"}
	_, err := Export(context.Background(), f, lock, []byte("{}"), filepath.Join(t.TempDir(), "out"), false)
	wantAll(t, err, `"../evil"`, "policy name", "slash")
}
