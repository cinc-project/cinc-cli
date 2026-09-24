package tarball

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// entry is one tar entry for build: a regular file unless typ says otherwise.
type entry struct {
	name, body string
	typ        byte
	link       string
}

// build gzips a tarball of entries, in order. Files advertise mode 0777 so
// tests can see it clamped.
func build(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0o777, Typeflag: e.typ, Linkname: e.link}
		if e.typ == 0 {
			hdr.Typeflag, hdr.Size = tar.TypeReg, int64(len(e.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
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

func shrinkCaps(t *testing.T, file, archive int64) {
	t.Helper()
	f, a := maxFileBytes, maxArchiveBytes
	maxFileBytes, maxArchiveBytes = file, archive
	t.Cleanup(func() { maxFileBytes, maxArchiveBytes = f, a })
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestExtractWritesFilesAndDirectories(t *testing.T) {
	dest := t.TempDir()
	archive := build(t,
		entry{name: "./", typ: tar.TypeDir},
		entry{name: "./nginx/", typ: tar.TypeDir},
		entry{name: "./nginx/metadata.rb", body: "name 'nginx'\n"},
		entry{name: "nginx/recipes/default.rb", body: "package 'nginx'\n"},
		entry{name: "nginx/empty/", typ: tar.TypeDir},
	)
	if err := Extract(bytes.NewReader(archive), dest, Options{}); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if got := readFile(t, filepath.Join(dest, "nginx", "metadata.rb")); got != "name 'nginx'\n" {
		t.Errorf("metadata.rb = %q", got)
	}
	if got := readFile(t, filepath.Join(dest, "nginx", "recipes", "default.rb")); got != "package 'nginx'\n" {
		t.Errorf("default.rb = %q", got)
	}
	if info, err := os.Stat(filepath.Join(dest, "nginx", "empty")); err != nil || !info.IsDir() {
		t.Errorf("empty directory not created: %v", err)
	}
}

func TestExtractStripTopLevel(t *testing.T) {
	dest := t.TempDir()
	archive := build(t,
		entry{name: "./nginx/", typ: tar.TypeDir},
		entry{name: "./nginx/metadata.rb", body: "name 'nginx'\n"},
		entry{name: "nginx/recipes/default.rb", body: "package 'nginx'\n"},
		entry{name: "README", body: "no segment below the top"},
	)
	if err := Extract(bytes.NewReader(archive), dest, Options{StripTopLevel: true}); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if got := readFile(t, filepath.Join(dest, "metadata.rb")); got != "name 'nginx'\n" {
		t.Errorf("metadata.rb = %q", got)
	}
	if got := readFile(t, filepath.Join(dest, "recipes", "default.rb")); got != "package 'nginx'\n" {
		t.Errorf("recipes/default.rb = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dest, "README")); err == nil {
		t.Error("an entry with nothing below its top segment was written")
	}
}

func TestExtractClampsModes(t *testing.T) {
	dest := t.TempDir()
	archive := build(t, entry{name: "nginx/", typ: tar.TypeDir}, entry{name: "nginx/metadata.rb", body: "x"})
	if err := Extract(bytes.NewReader(archive), dest, Options{}); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	for path, want := range map[string]os.FileMode{
		filepath.Join(dest, "nginx"):                DirMode,
		filepath.Join(dest, "nginx", "metadata.rb"): FileMode,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", path, got, want)
		}
	}
}

func TestExtractSkipsLinks(t *testing.T) {
	dest := t.TempDir()
	archive := build(t,
		entry{name: "nginx/key", typ: tar.TypeSymlink, link: "/etc/passwd"},
		entry{name: "nginx/hard", typ: tar.TypeLink, link: "nginx/key"},
		entry{name: "nginx/metadata.rb", body: "x"},
	)
	if err := Extract(bytes.NewReader(archive), dest, Options{}); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	for _, name := range []string{"key", "hard"} {
		if _, err := os.Lstat(filepath.Join(dest, "nginx", name)); err == nil {
			t.Errorf("link entry %s was written", name)
		}
	}
}

func TestExtractRefusesTraversal(t *testing.T) {
	for _, tc := range []struct {
		name  string
		opts  Options
		entry string
	}{
		{"plain", Options{}, "../escape.txt"},
		{"nested", Options{}, "nginx/../../escape.txt"},
		{"after strip", Options{StripTopLevel: true}, "nginx/../../escape.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "dest")
			if err := os.Mkdir(dest, 0o755); err != nil {
				t.Fatal(err)
			}
			err := Extract(bytes.NewReader(build(t, entry{name: tc.entry, body: "pwned"})), dest, tc.opts)
			if err == nil {
				t.Error("Extract accepted an entry that escapes dest")
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "escape.txt")); err == nil {
				t.Fatal("traversal entry escaped dest")
			}
		})
	}
}

// TestExtractDoesNotFollowSymlinkOutOfDest covers the hole a lexical check
// leaves open: "nginx/metadata.rb" is inside dest by every string comparison,
// but if dest already holds a "nginx" symlink pointing elsewhere, the create
// lands on the far side of it. os.Root refuses it at the syscall.
func TestExtractDoesNotFollowSymlinkOutOfDest(t *testing.T) {
	for _, opts := range []Options{{}, {StripTopLevel: true}} {
		dest, outside := t.TempDir(), t.TempDir()
		if err := os.Symlink(outside, filepath.Join(dest, "nginx")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		name := "nginx/metadata.rb"
		if opts.StripTopLevel {
			name = "cookbook/nginx/metadata.rb"
		}
		if err := Extract(bytes.NewReader(build(t, entry{name: name, body: "pwned"})), dest, opts); err == nil {
			t.Errorf("%+v: Extract wrote through a symlink in dest without complaint", opts)
		}
		if _, err := os.Stat(filepath.Join(outside, "metadata.rb")); err == nil {
			t.Fatalf("%+v: entry escaped dest through a pre-existing symlink", opts)
		}
	}
}

func TestExtractCapsFileSize(t *testing.T) {
	shrinkCaps(t, 16, 64)
	archive := build(t, entry{name: "big", body: "this body is definitely longer than sixteen bytes"})
	if err := Extract(bytes.NewReader(archive), t.TempDir(), Options{}); err == nil {
		t.Fatal("Extract accepted an entry over the per-file cap")
	}
}

func TestExtractCapsArchiveSize(t *testing.T) {
	shrinkCaps(t, 16, 40)
	archive := build(t,
		entry{name: "a", body: "0123456789abcdef"},
		entry{name: "b", body: "0123456789abcdef"},
		entry{name: "c", body: "0123456789abcdef"},
	)
	if err := Extract(bytes.NewReader(archive), t.TempDir(), Options{}); err == nil {
		t.Fatal("Extract accepted an archive over the whole-archive cap")
	}
}

// TestEscapes pins the lexical rules: "a/../b" normalizes back inside and is
// fine; anything that climbs above, or arrives absolute, is not.
func TestEscapes(t *testing.T) {
	for rel, want := range map[string]bool{
		"metadata.rb":        false,
		"recipes/default.rb": false,
		"a/../b":             false,
		".":                  false,
		"..":                 true,
		"../escape.txt":      true,
		"a/../../escape.txt": true,
		"..foo":              false,
		// os.TempDir is absolute on every platform, unlike a hardcoded "/etc".
		filepath.Join(os.TempDir(), "escape.txt"): true,
	} {
		if got := Escapes(rel); got != want {
			t.Errorf("Escapes(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestWithin(t *testing.T) {
	base := filepath.Join(os.TempDir(), "base")
	for target, want := range map[string]bool{
		base:                                   true,
		filepath.Join(base, "a", "b"):          true,
		filepath.Join(base, "..", "base2"):     false,
		filepath.Join(base, "..", "base", "x"): true,
		filepath.Dir(base):                     false,
	} {
		if got := Within(base, target); got != want {
			t.Errorf("Within(%q, %q) = %v, want %v", base, target, got, want)
		}
	}
}
