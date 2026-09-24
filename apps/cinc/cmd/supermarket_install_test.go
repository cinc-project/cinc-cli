package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	localcookbook "github.com/cinc-project/cinc-cli/cli/cookbook"
)

func TestSupermarketInstallCommandRegistered(t *testing.T) {
	root := newRootCmd()
	sub, _, err := root.Find([]string{"supermarket", "install"})
	if err != nil {
		t.Fatalf("Find supermarket install: %v", err)
	}
	if !strings.HasPrefix(sub.Use, "install") {
		t.Fatalf("Use = %q, want install", sub.Use)
	}
	if sub.Flags().Lookup("supermarket-site") == nil {
		t.Fatal("--supermarket-site flag missing")
	}
}

func TestSupermarketInstallDownloadsAndUploadsToServer(t *testing.T) {
	smSrv := installSupermarketServer(t, "nginx", "3.4.5")
	defer smSrv.Close()

	var sawManifest bool
	serverSrv := installCincServer(t, "nginx", "3.4.5", &sawManifest)
	defer serverSrv.Close()

	cfgPath := writeInstallConfig(t, serverSrv.URL)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{
		"supermarket", "install", "nginx",
		"--supermarket-site", smSrv.URL,
		"--config", cfgPath,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("supermarket install: %v", err)
	}
	if !sawManifest {
		t.Fatal("expected the cookbook manifest to be uploaded to the server")
	}
	want := "Installed cookbook \"nginx\" version 3.4.5 into the server\n"
	if got := buf.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestSupermarketInstallJSONOutput(t *testing.T) {
	smSrv := installSupermarketServer(t, "nginx", "3.4.5")
	defer smSrv.Close()

	var sawManifest bool
	serverSrv := installCincServer(t, "nginx", "3.4.5", &sawManifest)
	defer serverSrv.Close()

	cfgPath := writeInstallConfig(t, serverSrv.URL)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{
		"supermarket", "install", "nginx",
		"--supermarket-site", smSrv.URL,
		"--config", cfgPath,
		"--format", "json",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("supermarket install --format json: %v", err)
	}
	var result struct {
		Cookbook  string `json:"cookbook"`
		Version   string `json:"version"`
		Installed bool   `json:"installed"`
	}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, buf.String())
	}
	if result.Cookbook != "nginx" || result.Version != "3.4.5" || !result.Installed {
		t.Fatalf("result = %+v", result)
	}
}

func TestSupermarketInstallerInstallsThroughLazyCredentials(t *testing.T) {
	smSrv := installSupermarketServer(t, "nginx", "3.4.5")
	defer smSrv.Close()

	var sawManifest bool
	serverSrv := installCincServer(t, "nginx", "3.4.5", &sawManifest)
	defer serverSrv.Close()

	cmd := &cobra.Command{}
	cmd.Flags().String("config", writeInstallConfig(t, serverSrv.URL), "")
	cmd.Flags().String("profile", "", "")

	if err := supermarketInstaller(cmd, smSrv.URL)(context.Background(), "nginx", ""); err != nil {
		t.Fatalf("installer: %v", err)
	}
	if !sawManifest {
		t.Fatal("expected the installer to upload the cookbook manifest")
	}
}

func TestSupermarketInstallerSurfacesMissingCredentials(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("config", filepath.Join(t.TempDir(), "nope.toml"), "")
	cmd.Flags().String("profile", "", "")

	err := supermarketInstaller(cmd, "https://supermarket.example.test")(context.Background(), "nginx", "")
	if err == nil {
		t.Fatal("expected a credentials error when the config file is missing")
	}
}

func writeInstallConfig(t *testing.T, serverURL string) string {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "credentials")
	cfg := fmt.Sprintf(`[default]
cinc_server_url = "%s/organizations/acme"
client_name     = "tim"
client_key      = %q
`, serverURL, writeTestKey(t))
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

// installSupermarketServer serves the anonymous download half: latest
// version resolution plus a real gzipped tarball at the download path.
func installSupermarketServer(t *testing.T, cookbook, version string) *httptest.Server {
	t.Helper()
	tarball := buildInstallTarball(t, cookbook, version)
	downloadPath := "/api/v1/cookbooks/" + cookbook + "/versions/" + strings.ReplaceAll(version, ".", "_") + "/download"
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/cookbooks/" + cookbook + "/versions/latest":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"version":"`+version+`"}`)
		case downloadPath:
			w.Header().Set("Content-Type", "application/gzip")
			_, _ = w.Write(tarball)
		default:
			t.Errorf("unexpected supermarket request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

// installCincServer models the server-side cookbook upload flow: sandbox
// POST (nothing needs upload), sandbox commit, and version manifest PUT.
func installCincServer(t *testing.T, cookbook, version string, sawManifest *bool) *httptest.Server {
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
	return httptest.NewServer(mux)
}

func buildInstallTarball(t *testing.T, name, version string) []byte {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "recipes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata.rb"), []byte("name '"+name+"'\nversion '"+version+"'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(`{"name":"`+name+`","version":"`+version+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "recipes", "default.rb"), []byte("package '"+name+"'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cb, err := localcookbook.Load(dir, false)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	archive, err := localcookbook.BuildArchive(cb, nil)
	if err != nil {
		t.Fatalf("BuildArchive: %v", err)
	}
	return archive.Bytes
}
