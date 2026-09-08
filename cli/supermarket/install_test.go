package supermarket

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	cinc "github.com/cinc-project/cinc-api"

	localcookbook "github.com/cinc-project/cinc-cli/cli/cookbook"
)

func TestInstallDownloadsLatestAndUploadsToServer(t *testing.T) {
	tarball := buildCookbookTarball(t, "nginx", "3.4.5")
	smSrv := downloadFixtureServer(t, downloadFixture{
		Cookbook:      "nginx",
		LatestVersion: "3.4.5",
		Tarball:       tarball,
	})
	defer smSrv.Close()

	var sawManifest bool
	serverSrv := fakeCincServer(t, "nginx", "3.4.5", &sawManifest)
	defer serverSrv.Close()

	server := mustCincClient(t, serverSrv.URL)
	client := mustAnonymousClient(t, smSrv.URL)

	result, err := client.Install(context.Background(), server, InstallOptions{Cookbook: "nginx"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if result.Cookbook != "nginx" || result.Version != "3.4.5" || !result.Installed {
		t.Fatalf("result = %+v", result)
	}
	if !sawManifest {
		t.Fatal("expected the cookbook manifest to be uploaded to the server")
	}
}

func TestInstallInstallsExplicitVersion(t *testing.T) {
	tarball := buildCookbookTarball(t, "nginx", "1.2.0")
	smSrv := downloadFixtureServer(t, downloadFixture{
		Cookbook: "nginx",
		Version:  "1.2.0",
		Tarball:  tarball,
	})
	defer smSrv.Close()

	var sawManifest bool
	serverSrv := fakeCincServer(t, "nginx", "1.2.0", &sawManifest)
	defer serverSrv.Close()

	result, err := mustAnonymousClient(t, smSrv.URL).Install(
		context.Background(), mustCincClient(t, serverSrv.URL),
		InstallOptions{Cookbook: "nginx", Version: "1.2.0"},
	)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if result.Version != "1.2.0" || !sawManifest {
		t.Fatalf("result = %+v, sawManifest = %v", result, sawManifest)
	}
}

func TestInstallRequiresCookbookName(t *testing.T) {
	if _, err := mustAnonymousClient(t, "https://supermarket.example.test").Install(
		context.Background(), nil, InstallOptions{},
	); err == nil {
		t.Fatal("expected an error when the cookbook name is empty")
	}
}

func TestInstallWrapsDownloadFailure(t *testing.T) {
	smSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error_code":"NOT_FOUND"}`)
	}))
	defer smSrv.Close()

	_, err := mustAnonymousClient(t, smSrv.URL).Install(
		context.Background(), mustCincClient(t, "http://server.invalid"),
		InstallOptions{Cookbook: "nginx", Version: "1.2.0"},
	)
	if err == nil {
		t.Fatal("expected a download error")
	}
}

// buildCookbookTarball produces a real gzipped cookbook tarball (rooted at
// <name>/) the way Supermarket would serve one for download.
func buildCookbookTarball(t *testing.T, name, version string) []byte {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "recipes"), 0o755); err != nil {
		t.Fatal(err)
	}
	metadata := []byte("name '" + name + "'\nversion '" + version + "'\n")
	if err := os.WriteFile(filepath.Join(dir, "metadata.rb"), metadata, 0o644); err != nil {
		t.Fatal(err)
	}
	metaJSON := []byte(`{"name":"` + name + `","version":"` + version + `"}`)
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), metaJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "recipes", "default.rb"), []byte("package '"+name+"'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	archive, err := localcookbook.BuildArchive(dir, name)
	if err != nil {
		t.Fatalf("BuildArchive: %v", err)
	}
	return archive.Bytes
}

// fakeCincServer models the cookbook-upload flow of a Chef Infra Server:
// a sandbox POST (responding that nothing needs upload), a sandbox commit
// PUT, and the version manifest PUT. It flips *sawManifest when the
// manifest lands.
func fakeCincServer(t *testing.T, cookbook, version string, sawManifest *bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/sandboxes", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Checksums map[string]json.RawMessage `json:"checksums"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		out := map[string]any{}
		for sum := range req.Checksums {
			out[sum] = map[string]any{"needs_upload": false}
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"sandbox_id": "sb1", "checksums": out})
	})
	mux.HandleFunc("/organizations/acme/sandboxes/sb1", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	})
	mux.HandleFunc("/organizations/acme/cookbooks/"+cookbook+"/"+version, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			*sawManifest = true
			w.WriteHeader(http.StatusCreated)
		}
		_, _ = io.WriteString(w, `{}`)
	})
	srv := httptest.NewServer(mux)
	return srv
}

func mustCincClient(t *testing.T, serverURL string) *cinc.Client {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	c, err := cinc.NewClient(cinc.Config{
		ServerURL: serverURL, Org: "acme", ClientName: "tim", Key: key,
	})
	if err != nil {
		t.Fatalf("cinc.NewClient: %v", err)
	}
	return c
}
