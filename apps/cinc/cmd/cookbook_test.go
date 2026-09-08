package cmd

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// cookbookServer starts an httptest server that serves a cookbook index
// for org "acme" and returns the server. Each cookbook in the index has a
// URL plus a single dummy version, matching the Chef API shape.
func cookbookServer(t *testing.T, names ...string) *httptest.Server {
	t.Helper()
	type entry struct {
		URL      string              `json:"url"`
		Versions []map[string]string `json:"versions"`
	}
	index := make(map[string]entry, len(names))
	for _, n := range names {
		index[n] = entry{
			URL: "https://example.test/cookbooks/" + n,
			Versions: []map[string]string{
				{"url": "https://example.test/cookbooks/" + n + "/1.0.0", "version": "1.0.0"},
			},
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/cookbooks", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(index)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchCookbookNamesReturnsSortedNames(t *testing.T) {
	srv := cookbookServer(t, "nginx", "apache", "mysql")

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	c, err := cinc.NewClient(cinc.Config{
		ServerURL: srv.URL, Org: "acme", ClientName: "tim", Key: key,
	})
	if err != nil {
		t.Fatal(err)
	}

	names, err := fetchCookbookNames(context.Background(), c)
	if err != nil {
		t.Fatalf("fetchCookbookNames: %v", err)
	}
	want := []string{"apache", "mysql", "nginx"}
	if !slices.Equal(names, want) {
		t.Errorf("fetchCookbookNames = %v, want %v", names, want)
	}
}

func TestCookbookDeleteCommandEndToEnd(t *testing.T) {
	var deletedPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/cookbooks/nginx/1.0.0", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		deletedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "nginx"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfgPath := filepath.Join(t.TempDir(), "credentials")
	cfg := fmt.Sprintf(`[default]
cinc_server_url = "%s/organizations/acme"
client_name     = "tim"
client_key      = %q
`, srv.URL, writeTestKey(t))
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"cookbook", "delete", "nginx", "1.0.0", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc cookbook delete: %v", err)
	}
	if deletedPath != "/organizations/acme/cookbooks/nginx/1.0.0" {
		t.Errorf("server saw delete at %q", deletedPath)
	}
	if got := buf.String(); got != "Deleted cookbook \"nginx\" version 1.0.0\n" {
		t.Errorf("cookbook delete output = %q", got)
	}
}

func TestCookbookUploadCommandEndToEnd(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "nginx", "recipes"), 0o755); err != nil {
		t.Fatal(err)
	}
	metadataContent := []byte("name 'nginx'\nversion '1.2.0'\n")
	recipeContent := []byte("package 'nginx'\n")
	if err := os.WriteFile(filepath.Join(dir, "nginx", "metadata.rb"), metadataContent, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nginx", "recipes", "default.rb"), recipeContent, 0o644); err != nil {
		t.Fatal(err)
	}

	metadataChecksum := fmt.Sprintf("%x", md5.Sum(metadataContent))
	var sawManifest bool
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/sandboxes", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("sandbox method = %q, want POST", r.Method)
		}
		uploadURL := "http://" + r.Host + "/upload/" + metadataChecksum
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"sandbox_id":"sb1","checksums":{"`+metadataChecksum+`":{"needs_upload":true,"url":"`+uploadURL+`"}}}`)
	})
	mux.HandleFunc("/upload/"+metadataChecksum, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("upload method = %q, want PUT", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != string(metadataContent) {
			t.Errorf("uploaded body = %q, want metadata.rb", body)
		}
	})
	mux.HandleFunc("/organizations/acme/sandboxes/sb1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("commit method = %q, want PUT", r.Method)
		}
		_, _ = io.WriteString(w, `{}`)
	})
	mux.HandleFunc("/organizations/acme/cookbooks/nginx/1.2.0", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("manifest method = %q, want PUT", r.Method)
		}
		sawManifest = true
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte(`"chef_type":"cookbook_version"`)) {
			t.Errorf("manifest body = %s", body)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfgPath := filepath.Join(t.TempDir(), "credentials")
	cfg := fmt.Sprintf(`[default]
cinc_server_url = "%s/organizations/acme"
client_name     = "tim"
client_key      = %q
`, srv.URL, writeTestKey(t))
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"cookbook", "upload", "nginx", "--cookbook-path", dir, "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc cookbook upload: %v", err)
	}
	if !sawManifest {
		t.Fatal("expected cookbook manifest upload")
	}
	if got := buf.String(); got != "Uploaded cookbook \"nginx\" version 1.2.0\n" {
		t.Errorf("cookbook upload output = %q", got)
	}
}

func TestCookbookShowCommandEndToEnd(t *testing.T) {
	manifest := cinc.Cookbook{
		CookbookName: "nginx",
		Name:         "nginx-1.2.0",
		Version:      "1.2.0",
		Recipes: []cinc.CookbookFileRef{
			{Name: "default.rb", Path: "recipes/default.rb", Checksum: "abc", URL: "https://example.test/recipes/default.rb"},
		},
	}
	var sawPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/cookbooks/nginx/1.2.0", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		sawPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(manifest)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfgPath := filepath.Join(t.TempDir(), "credentials")
	cfg := fmt.Sprintf(`[default]
cinc_server_url = "%s/organizations/acme"
client_name     = "tim"
client_key      = %q
`, srv.URL, writeTestKey(t))
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"cookbook", "show", "nginx", "1.2.0", "--config", cfgPath, "--format", "json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc cookbook show: %v", err)
	}
	if sawPath != "/organizations/acme/cookbooks/nginx/1.2.0" {
		t.Errorf("server saw GET at %q", sawPath)
	}

	var got cinc.Cookbook
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("show output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if got.CookbookName != "nginx" || got.Version != "1.2.0" {
		t.Errorf("show returned %+v, want cookbook_name=nginx version=1.2.0", got)
	}
	if len(got.Recipes) != 1 || got.Recipes[0].Name != "default.rb" {
		t.Errorf("show recipes = %+v, want one default.rb entry", got.Recipes)
	}
}

func TestCookbookShowCommandLatestVersion(t *testing.T) {
	manifest := cinc.Cookbook{CookbookName: "nginx", Name: "nginx-2.0.0", Version: "2.0.0"}
	var sawPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/cookbooks/nginx/_latest", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		sawPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(manifest)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfgPath := filepath.Join(t.TempDir(), "credentials")
	cfg := fmt.Sprintf(`[default]
cinc_server_url = "%s/organizations/acme"
client_name     = "tim"
client_key      = %q
`, srv.URL, writeTestKey(t))
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"cookbook", "show", "nginx", "--config", cfgPath, "--format", "json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc cookbook show: %v", err)
	}
	if sawPath != "/organizations/acme/cookbooks/nginx/_latest" {
		t.Errorf("server saw GET at %q, want /_latest", sawPath)
	}

	var got cinc.Cookbook
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("show output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if got.Version != "2.0.0" {
		t.Errorf("show version = %q, want 2.0.0", got.Version)
	}
}

func TestCookbookListCommandEndToEnd(t *testing.T) {
	srv := cookbookServer(t, "nginx", "apache", "mysql")

	cfgPath := filepath.Join(t.TempDir(), "credentials")
	cfg := fmt.Sprintf(`[default]
cinc_server_url = "%s/organizations/acme"
client_name     = "tim"
client_key      = %q
`, srv.URL, writeTestKey(t))
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"cookbook", "list", "--config", cfgPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc cookbook list: %v", err)
	}
	if got := buf.String(); got != "apache\nmysql\nnginx\n" {
		t.Errorf("cookbook list output = %q, want sorted cookbook names", got)
	}

	// The same list under --format json renders a JSON array of the names.
	root = newRootCmd()
	buf.Reset()
	root.SetOut(&buf)
	root.SetArgs([]string{"cookbook", "list", "--config", cfgPath, "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc cookbook list --format json: %v", err)
	}
	var names []string
	if err := json.Unmarshal(buf.Bytes(), &names); err != nil {
		t.Fatalf("json list output is not a JSON array: %v\noutput: %s", err, buf.String())
	}
	if !slices.Equal(names, []string{"apache", "mysql", "nginx"}) {
		t.Errorf("json list output = %v, want [apache mysql nginx]", names)
	}
}
