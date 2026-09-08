package supermarket

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sm "github.com/cinc-project/cinc-supermarket-api"
)

func TestNewAnonymousAcceptsEmptyProfile(t *testing.T) {
	client, err := NewAnonymous("https://supermarket.example.test")
	if err != nil {
		t.Fatalf("NewAnonymous: %v", err)
	}
	if client.base.String() != "https://supermarket.example.test" {
		t.Fatalf("base = %q", client.base)
	}
}

func TestNewAnonymousRejectsInvalidSite(t *testing.T) {
	if _, err := NewAnonymous("not a url"); err == nil {
		t.Fatal("expected invalid site URL error")
	}
}

func TestDownloadWritesTarballForExplicitVersion(t *testing.T) {
	body := []byte("tar-body")
	srv := downloadFixtureServer(t, downloadFixture{
		Cookbook: "nginx",
		Version:  "1.2.0",
		Tarball:  body,
	})
	defer srv.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "nginx.tgz")
	client := mustAnonymousClient(t, srv.URL)
	result, err := client.Download(context.Background(), DownloadOptions{
		Cookbook: "nginx", Version: "1.2.0", File: out,
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if result.Cookbook != "nginx" || result.Version != "1.2.0" {
		t.Fatalf("result = %+v", result)
	}
	if result.File != out {
		t.Fatalf("File = %q, want %q", result.File, out)
	}
	if result.Bytes != int64(len(body)) {
		t.Fatalf("Bytes = %d, want %d", result.Bytes, len(body))
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("file contents = %q, want %q", got, body)
	}
}

func TestDownloadResolvesLatestToConcreteVersion(t *testing.T) {
	body := []byte("latest-body")
	srv := downloadFixtureServer(t, downloadFixture{
		Cookbook:      "nginx",
		LatestVersion: "3.4.5",
		Tarball:       body,
	})
	defer srv.Close()

	dir := t.TempDir()
	client := mustAnonymousClient(t, srv.URL)
	result, err := client.Download(context.Background(), DownloadOptions{
		Cookbook: "nginx", File: dir,
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if result.Version != "3.4.5" {
		t.Fatalf("Version = %q, want resolved 3.4.5", result.Version)
	}
	want := filepath.Join(dir, "nginx-3.4.5.tar.gz")
	if result.File != want {
		t.Fatalf("File = %q, want %q", result.File, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected file at %s: %v", want, err)
	}
}

func TestDownloadDefaultFilenameUsesResolvedVersion(t *testing.T) {
	srv := downloadFixtureServer(t, downloadFixture{
		Cookbook: "nginx",
		Version:  "1.2.0",
		Tarball:  []byte("x"),
	})
	defer srv.Close()

	dir := t.TempDir()
	t.Chdir(dir)

	client := mustAnonymousClient(t, srv.URL)
	result, err := client.Download(context.Background(), DownloadOptions{
		Cookbook: "nginx", Version: "1.2.0",
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	want := filepath.Join(dir, "nginx-1.2.0.tar.gz")
	if result.File != want {
		t.Fatalf("File = %q, want %q (cwd default)", result.File, want)
	}
}

func TestDownloadFileAsDirectoryUsesDefaultName(t *testing.T) {
	srv := downloadFixtureServer(t, downloadFixture{
		Cookbook: "nginx",
		Version:  "1.2.0",
		Tarball:  []byte("x"),
	})
	defer srv.Close()

	dir := t.TempDir()
	client := mustAnonymousClient(t, srv.URL)
	result, err := client.Download(context.Background(), DownloadOptions{
		Cookbook: "nginx", Version: "1.2.0", File: dir,
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	want := filepath.Join(dir, "nginx-1.2.0.tar.gz")
	if result.File != want {
		t.Fatalf("File = %q, want default name inside directory", result.File)
	}
}

func TestDownloadRefusesExistingFileWithoutForce(t *testing.T) {
	srv := downloadFixtureServer(t, downloadFixture{
		Cookbook: "nginx",
		Version:  "1.2.0",
		Tarball:  []byte("new"),
	})
	defer srv.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "nginx.tgz")
	if err := os.WriteFile(out, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := mustAnonymousClient(t, srv.URL)
	_, err := client.Download(context.Background(), DownloadOptions{
		Cookbook: "nginx", Version: "1.2.0", File: out,
	})
	if err == nil {
		t.Fatal("expected refusal when file exists without --force")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %q, want 'already exists'", err)
	}
	got, _ := os.ReadFile(out)
	if string(got) != "old" {
		t.Fatalf("existing file was modified: %q", got)
	}
}

func TestDownloadForceOverwritesExistingFile(t *testing.T) {
	srv := downloadFixtureServer(t, downloadFixture{
		Cookbook: "nginx",
		Version:  "1.2.0",
		Tarball:  []byte("new"),
	})
	defer srv.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "nginx.tgz")
	if err := os.WriteFile(out, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := mustAnonymousClient(t, srv.URL)
	if _, err := client.Download(context.Background(), DownloadOptions{
		Cookbook: "nginx", Version: "1.2.0", File: out, Force: true,
	}); err != nil {
		t.Fatalf("Download with Force: %v", err)
	}
	got, _ := os.ReadFile(out)
	if string(got) != "new" {
		t.Fatalf("file contents = %q, want overwrite", got)
	}
}

func TestDownloadWrapsNotFoundFromSupermarket(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error_code":"NOT_FOUND"}`)
	}))
	defer srv.Close()

	client := mustAnonymousClient(t, srv.URL)
	_, err := client.Download(context.Background(), DownloadOptions{
		Cookbook: "nginx", Version: "1.2.0", File: filepath.Join(t.TempDir(), "n.tgz"),
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, sm.ErrNotFound) {
		t.Fatalf("err = %v, want errors.Is(err, sm.ErrNotFound)", err)
	}
}

func TestDownloadCleansUpPartialOnStreamFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/download") {
			w.Header().Set("Content-Length", "1000")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("partial"))
			// Hijack and break the connection so the body read errors out.
			hj, ok := w.(http.Hijacker)
			if !ok {
				return
			}
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "nginx.tgz")
	client := mustAnonymousClient(t, srv.URL)
	_, err := client.Download(context.Background(), DownloadOptions{
		Cookbook: "nginx", Version: "1.2.0", File: out,
	})
	if err == nil {
		t.Fatal("expected stream error")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".partial") {
			t.Fatalf("partial file left behind: %s", e.Name())
		}
		if e.Name() == filepath.Base(out) {
			t.Fatalf("incomplete final file left behind: %s", e.Name())
		}
	}
}

// downloadFixture describes a minimal Supermarket the tests want to model.
// If Version is set we expect an explicit-version download; if LatestVersion
// is set the test relies on resolving "latest" via GetVersion.
type downloadFixture struct {
	Cookbook      string
	Version       string
	LatestVersion string
	Tarball       []byte
}

func downloadFixtureServer(t *testing.T, f downloadFixture) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "/api/v1/cookbooks/" + f.Cookbook
		switch {
		case r.URL.Path == base+"/versions/latest" && f.LatestVersion != "":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"version":"`+f.LatestVersion+`"}`)
		case strings.HasSuffix(r.URL.Path, "/download"):
			w.Header().Set("Content-Type", "application/gzip")
			_, _ = w.Write(f.Tarball)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	return srv
}

func mustAnonymousClient(t *testing.T, site string) *Client {
	t.Helper()
	client, err := NewAnonymous(site)
	if err != nil {
		t.Fatalf("NewAnonymous: %v", err)
	}
	return client
}

func TestReachableHitsHealthEndpoint(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	}))
	t.Cleanup(srv.Close)

	if err := mustAnonymousClient(t, srv.URL).Reachable(context.Background()); err != nil {
		t.Fatalf("Reachable: %v", err)
	}
	if gotPath != "/api/v1/health" {
		t.Errorf("Reachable hit %q, want /api/v1/health", gotPath)
	}
}

func TestReachableReportsUnreachableSite(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	if err := mustAnonymousClient(t, srv.URL).Reachable(context.Background()); err == nil {
		t.Error("Reachable returned nil for an unhealthy endpoint, want an error")
	}
}
