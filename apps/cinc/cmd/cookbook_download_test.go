package cmd

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cookbookDownloadServer serves a single nginx cookbook version manifest plus
// the two files it references. The checksums are the real MD5s of the file
// bodies, since cinc-api verifies every download. The manifest's file URLs point back at this
// same server, mirroring how the real server hands out bookshelf URLs.
// requestedVersions records, in order, the version segments the manifest was
// fetched under, so tests can assert "_latest" resolution and that the
// manifest is fetched only once.
func cookbookDownloadServer(t *testing.T, requestedVersions *[]string) *httptest.Server {
	t.Helper()
	var base string
	mux := http.NewServeMux()
	manifest := func(w http.ResponseWriter, r *http.Request, version string) {
		*requestedVersions = append(*requestedVersions, version)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"cookbook_name":"nginx","name":"nginx-1.2.0","version":"1.2.0",
			"recipes":[{"name":"default.rb","path":"recipes/default.rb","specificity":"default","checksum":"c2a96135902fd58f8bcc6cc89c8ab3e1","url":"%s/files/recipes/default.rb"}],
			"root_files":[{"name":"metadata.rb","path":"metadata.rb","specificity":"default","checksum":"63c0c54f160051c1c367ed722ccb8421","url":"%s/files/metadata.rb"}]
		}`, base, base)
	}
	mux.HandleFunc("/organizations/acme/cookbooks/nginx/_latest", func(w http.ResponseWriter, r *http.Request) {
		manifest(w, r, "_latest")
	})
	mux.HandleFunc("/organizations/acme/cookbooks/nginx/1.2.0", func(w http.ResponseWriter, r *http.Request) {
		manifest(w, r, "1.2.0")
	})
	mux.HandleFunc("/files/recipes/default.rb", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("package 'nginx'\n"))
	})
	mux.HandleFunc("/files/metadata.rb", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("name 'nginx'\n"))
	})
	srv := httptest.NewServer(mux)
	base = srv.URL
	t.Cleanup(srv.Close)
	return srv
}

func TestCookbookDownloadWritesFilesUnderNameVersionDir(t *testing.T) {
	var requested []string
	srv := cookbookDownloadServer(t, &requested)

	destParent := t.TempDir()
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"cookbook", "download", "nginx", "--dir", destParent, "--config", writeCreateConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc cookbook download: %v", err)
	}

	// No explicit version => one fetch resolves the "_latest" sentinel
	// server-side, the files come from that same manifest, and "_latest"
	// never appears in the destination dir name.
	if len(requested) != 1 || requested[0] != "_latest" {
		t.Errorf("manifest fetched under %v, want exactly one fetch, of _latest", requested)
	}

	cbDir := filepath.Join(destParent, "nginx-1.2.0")
	recipe, err := os.ReadFile(filepath.Join(cbDir, "recipes", "default.rb"))
	if err != nil {
		t.Fatalf("recipe not written: %v", err)
	}
	if string(recipe) != "package 'nginx'\n" {
		t.Errorf("recipe content = %q", recipe)
	}
	if _, err := os.ReadFile(filepath.Join(cbDir, "metadata.rb")); err != nil {
		t.Errorf("metadata.rb not written: %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "nginx") || !strings.Contains(out, "1.2.0") || !strings.Contains(out, cbDir) {
		t.Errorf("output = %q, want confirmation with name, version, and dir", out)
	}
}

func TestCookbookDownloadAcceptsExplicitVersion(t *testing.T) {
	var requested []string
	srv := cookbookDownloadServer(t, &requested)

	destParent := t.TempDir()
	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"cookbook", "download", "nginx", "1.2.0", "--dir", destParent, "--config", writeCreateConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc cookbook download nginx 1.2.0: %v", err)
	}
	if len(requested) != 1 || requested[0] != "1.2.0" {
		t.Errorf("manifest fetched under %v, want exactly one fetch, of 1.2.0", requested)
	}
	if _, err := os.Stat(filepath.Join(destParent, "nginx-1.2.0", "metadata.rb")); err != nil {
		t.Errorf("expected cookbook downloaded to nginx-1.2.0/: %v", err)
	}
}
