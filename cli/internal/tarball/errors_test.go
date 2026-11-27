package tarball

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"strings"
	"testing"
)

func extractErr(t *testing.T, data []byte) error {
	t.Helper()
	err := Extract(bytes.NewReader(data), t.TempDir(), Options{})
	if err == nil {
		t.Fatal("Extract succeeded, want an error")
	}
	return err
}

func wantMention(t *testing.T, err error, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q:\n%s", want, err)
		}
	}
}

func TestExtractExplainsANonGzipFile(t *testing.T) {
	err := extractErr(t, []byte("PK\x03\x04 this is a zip, not a tarball"))
	wantMention(t, err, "isn't a gzip-compressed tar archive", ".tar.gz")
}

func TestExtractExplainsAnEmptyFile(t *testing.T) {
	err := extractErr(t, nil)
	wantMention(t, err, "empty")
}

func TestExtractExplainsATruncatedArchive(t *testing.T) {
	full := build(t, entry{name: "cb/metadata.rb", body: strings.Repeat("name 'cb'\n", 2000)})
	err := extractErr(t, full[:len(full)/2])
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("errors.Is(err, io.ErrUnexpectedEOF) = false for %v", err)
	}
	wantMention(t, err, "cut off", "fresh copy")
}

func TestExtractExplainsGzipThatIsNoTar(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte(strings.Repeat("not a tar header ", 64)))
	_ = gz.Close()
	err := extractErr(t, buf.Bytes())
	wantMention(t, err, "tar")
	if strings.HasPrefix(err.Error(), "read tar:") {
		t.Errorf("error is the bare library message: %s", err)
	}
}

func TestExtractExplainsTraversal(t *testing.T) {
	err := extractErr(t, build(t, entry{name: "../evil", body: "x"}))
	wantMention(t, err, `"../evil"`, "outside", "didn't extract")
}
