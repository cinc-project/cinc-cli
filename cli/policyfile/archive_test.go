package policyfile

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// writeBundle builds a minimal extracted bundle on disk: a Policyfile.lock.json
// and one cookbook under cookbooks/<name>-<ddi>/, mirroring what Export writes.
func writeBundle(t *testing.T) (dir string, lock *cinc.PolicyRevision) {
	t.Helper()
	dir = t.TempDir()
	cb := filepath.Join(dir, "cookbooks", "base-1.2.3", "recipes")
	if err := os.MkdirAll(cb, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cookbooks", "base-1.2.3", "metadata.rb"), []byte("name 'base'\nversion '1.0.0'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cb, "default.rb"), []byte("log 'base'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, BundleLockName), []byte(`{"name":"appserver"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	lock = &cinc.PolicyRevision{
		Name: "appserver",
		CookbookLocks: map[string]cinc.CookbookLock{
			"base": {Version: "1.0.0", Identifier: "abc", DottedDecimalIdentifier: "1.2.3"},
		},
	}
	return dir, lock
}

func TestLoadBundleCookbooksUsesLockNameNotDirName(t *testing.T) {
	dir, lock := writeBundle(t)

	cookbooks, err := LoadBundleCookbooks(dir, lock)
	if err != nil {
		t.Fatalf("LoadBundleCookbooks: %v", err)
	}
	cb, ok := cookbooks["base"]
	if !ok {
		t.Fatalf("cookbook %q missing from %v", "base", cookbooks)
	}
	// The on-disk directory is base-1.2.3, but the upload name must be the bare
	// cookbook name from the lock so the artifact lands at the right path.
	if cb.Name != "base" {
		t.Errorf("cookbook name = %q, want %q (lock name, not directory name)", cb.Name, "base")
	}
	if cb.Version != "1.0.0" {
		t.Errorf("cookbook version = %q, want 1.0.0", cb.Version)
	}
}

func TestOpenBundleAcceptsDirectory(t *testing.T) {
	dir, _ := writeBundle(t)
	got, cleanup, err := OpenBundle(dir)
	if err != nil {
		t.Fatalf("OpenBundle(dir): %v", err)
	}
	defer cleanup()
	if got != dir {
		t.Errorf("OpenBundle returned %q, want the directory itself %q", got, dir)
	}
}

func TestOpenBundleExtractsTarball(t *testing.T) {
	dir, _ := writeBundle(t)
	archive := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if err := tarGzDir(dir, archive); err != nil {
		t.Fatal(err)
	}

	got, cleanup, err := OpenBundle(archive)
	if err != nil {
		t.Fatalf("OpenBundle(tarball): %v", err)
	}
	defer cleanup()
	if _, err := os.Stat(filepath.Join(got, BundleLockName)); err != nil {
		t.Errorf("extracted bundle missing lock: %v", err)
	}
	if _, err := os.Stat(filepath.Join(got, "cookbooks", "base-1.2.3", "metadata.rb")); err != nil {
		t.Errorf("extracted bundle missing cookbook: %v", err)
	}
}

// TestOpenBundleRejectsPathTraversal builds a malicious archive whose entry
// escapes the extraction directory and asserts extraction refuses it.
func TestOpenBundleRejectsPathTraversal(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "evil.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	body := []byte("pwned")
	if err := tw.WriteHeader(&tar.Header{Name: "../escape.txt", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	for _, c := range []interface{ Close() error }{tw, gz, f} {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}

	if _, cleanup, err := OpenBundle(archive); err == nil {
		cleanup()
		t.Fatal("expected OpenBundle to reject a path-traversal archive entry")
	}
	// And the escape target must not have been written next to the archive.
	if _, err := os.Stat(filepath.Join(filepath.Dir(archive), "escape.txt")); err == nil {
		t.Error("path-traversal entry escaped the extraction directory")
	}
}

// TestOpenBundleRejectsOversizedEntry confirms an over-cap entry is refused
// rather than expanded onto disk (zip-bomb / disk-fill guard).
func TestOpenBundleRejectsOversizedEntry(t *testing.T) {
	defer func(f, a int64) { maxExtractedFileBytes, maxExtractedArchiveBytes = f, a }(maxExtractedFileBytes, maxExtractedArchiveBytes)
	maxExtractedFileBytes, maxExtractedArchiveBytes = 16, 64

	archive := filepath.Join(t.TempDir(), "big.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	body := []byte("this body is definitely longer than sixteen bytes")
	if err := tw.WriteHeader(&tar.Header{Name: "Policyfile.lock.json", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	for _, c := range []interface{ Close() error }{tw, gz, f} {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}

	if _, cleanup, err := OpenBundle(archive); err == nil {
		cleanup()
		t.Fatal("expected OpenBundle to reject an over-cap archive entry")
	}
}
