package cookbook

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// TestExtractArchiveRoundTripsBuiltArchive builds a real cookbook tarball
// with BuildArchive, then unpacks it with ExtractArchive and asserts the
// files land on disk under the returned top-level cookbook directory.
func TestExtractArchiveRoundTripsBuiltArchive(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "recipes"), 0o755); err != nil {
		t.Fatal(err)
	}
	metadata := []byte("name 'nginx'\nversion '1.2.0'\n")
	recipe := []byte("package 'nginx'\n")
	if err := os.WriteFile(filepath.Join(src, "metadata.rb"), metadata, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "recipes", "default.rb"), recipe, 0o644); err != nil {
		t.Fatal(err)
	}

	archive := loadArchive(t, src, false, nil)

	dest := t.TempDir()
	cookbookDir, err := ExtractArchive(bytes.NewReader(archive.Bytes), dest)
	if err != nil {
		t.Fatalf("ExtractArchive: %v", err)
	}

	want := filepath.Join(dest, "nginx")
	if cookbookDir != want {
		t.Fatalf("cookbookDir = %q, want %q", cookbookDir, want)
	}
	gotMeta, err := os.ReadFile(filepath.Join(cookbookDir, "metadata.rb"))
	if err != nil {
		t.Fatalf("read extracted metadata.rb: %v", err)
	}
	if string(gotMeta) != string(metadata) {
		t.Errorf("metadata.rb = %q, want %q", gotMeta, metadata)
	}
	gotRecipe, err := os.ReadFile(filepath.Join(cookbookDir, "recipes", "default.rb"))
	if err != nil {
		t.Fatalf("read extracted recipe: %v", err)
	}
	if string(gotRecipe) != string(recipe) {
		t.Errorf("recipes/default.rb = %q, want %q", gotRecipe, recipe)
	}
}

// TestExtractArchiveHandlesDotSlashAndDirEntries covers the layout real
// Supermarket tarballs (produced by Ruby's mixlib-archive) often use:
// explicit directory headers and "./"-prefixed entry names. The returned
// cookbook directory must still be <dest>/nginx, not the temp root, so the
// uploaded cookbook keeps its real name.
func TestExtractArchiveHandlesDotSlashAndDirEntries(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	writeEntry := func(name string, dir bool, body string) {
		hdr := &tar.Header{Name: name, Mode: 0o644, Typeflag: tar.TypeReg, Size: int64(len(body))}
		if dir {
			hdr.Typeflag, hdr.Mode, hdr.Size = tar.TypeDir, 0o755, 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if !dir {
			if _, err := tw.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	writeEntry("./", true, "")
	writeEntry("./nginx/", true, "")
	writeEntry("./nginx/metadata.rb", false, "name 'nginx'\n")
	writeEntry("./nginx/recipes/", true, "")
	writeEntry("./nginx/recipes/default.rb", false, "package 'nginx'\n")
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	cookbookDir, err := ExtractArchive(bytes.NewReader(buf.Bytes()), dest)
	if err != nil {
		t.Fatalf("ExtractArchive: %v", err)
	}
	if want := filepath.Join(dest, "nginx"); cookbookDir != want {
		t.Fatalf("cookbookDir = %q, want %q", cookbookDir, want)
	}
	if _, err := os.Stat(filepath.Join(cookbookDir, "recipes", "default.rb")); err != nil {
		t.Fatalf("expected extracted recipe: %v", err)
	}
}

// TestExtractArchiveWantsOneTopLevelDirectory refuses a tarball that isn't
// a single cookbook directory, since there'd be no telling which to upload.
func TestExtractArchiveWantsOneTopLevelDirectory(t *testing.T) {
	for name, files := range map[string][]string{
		"two directories": {"nginx/metadata.rb", "apache/metadata.rb"},
		"a loose file":    {"metadata.rb"},
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)
			tw := tar.NewWriter(gz)
			for _, f := range files {
				if err := tw.WriteHeader(&tar.Header{Name: f, Mode: 0o644, Typeflag: tar.TypeReg}); err != nil {
					t.Fatal(err)
				}
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := ExtractArchive(bytes.NewReader(buf.Bytes()), t.TempDir()); err == nil {
				t.Error("ExtractArchive accepted an archive without exactly one top-level directory")
			}
		})
	}
}
