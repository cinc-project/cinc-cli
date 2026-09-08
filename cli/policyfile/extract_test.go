package policyfile

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// buildTarball gzips a tarball from the given entries (name -> body).
func buildTarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		hdr := &tar.Header{Name: name, Mode: 0o644, Typeflag: tar.TypeReg, Size: int64(len(body))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestExtractCookbookTarballRejectsTraversal pins the plain "../" case.
func TestExtractCookbookTarballRejectsTraversal(t *testing.T) {
	dest := t.TempDir()
	archive := buildTarball(t, map[string]string{"nginx/../../escape.txt": "pwned"})
	if err := extractCookbookTarball(bytes.NewReader(archive), dest); err == nil {
		t.Error("expected a traversal entry to be refused")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "escape.txt")); err == nil {
		t.Fatal("traversal entry escaped dest")
	}
}

// TestExtractCookbookTarballDoesNotFollowSymlinkOutOfDest covers what a
// lexical check cannot see. After the leading cookbook segment is stripped,
// "nginx/cache/evil.rb" resolves to dest/cache/evil.rb, which is inside dest
// by string comparison; if dest already holds a "cache" symlink the write
// still lands outside it.
func TestExtractCookbookTarballDoesNotFollowSymlinkOutOfDest(t *testing.T) {
	dest := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dest, "cache")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	archive := buildTarball(t, map[string]string{"nginx/cache/evil.rb": "pwned"})
	if err := extractCookbookTarball(bytes.NewReader(archive), dest); err == nil {
		t.Error("extractCookbookTarball wrote through a symlink in dest without complaint")
	}
	if _, err := os.Stat(filepath.Join(outside, "evil.rb")); err == nil {
		t.Fatal("archive entry escaped dest through a pre-existing symlink")
	}
}

// TestRelEscapes pins the lexical rules the extractor rejects entries on.
// "a/../b" normalizes back inside the destination and is fine; anything that
// climbs above it, or arrives absolute, is not.
func TestRelEscapes(t *testing.T) {
	cases := []struct {
		rel  string
		want bool
	}{
		{"metadata.rb", false},
		{"recipes/default.rb", false},
		{"a/../b", false},
		{".", false},
		{"..", true},
		{"../escape.txt", true},
		{"a/../../escape.txt", true},
		// os.TempDir is absolute on every platform, unlike a hardcoded "/etc".
		{filepath.Join(os.TempDir(), "escape.txt"), true},
	}
	for _, tc := range cases {
		if got := relEscapes(tc.rel); got != tc.want {
			t.Errorf("relEscapes(%q) = %v, want %v", tc.rel, got, tc.want)
		}
	}
}
