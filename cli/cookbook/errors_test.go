package cookbook

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func wantMention(t *testing.T, err error, wants ...string) {
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

func TestLocateExplainsAMissingCookbookInTheCurrentDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	_, err = Locate("nginx", "")
	wantMention(t, err, `cookbook "nginx"`, cwd, filepath.Join(cwd, "nginx"), "metadata.rb", "--cookbook-path")
}

func TestLocateExplainsAMissingCookbookOnTheCookbookPath(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	_, err := Locate("nginx", a+string(filepath.ListSeparator)+b)
	wantMention(t, err, `cookbook "nginx"`, filepath.Join(a, "nginx"), filepath.Join(b, "nginx"), "--cookbook-path", "metadata.rb")
}

func TestExtractArchiveExplainsAnArchiveWithSeveralTopLevelEntries(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range []string{"metadata.rb", "README.md"} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	_, err := ExtractArchive(&buf, t.TempDir())
	wantMention(t, err, "2 entries", "README.md", "metadata.rb", "one directory")
}
