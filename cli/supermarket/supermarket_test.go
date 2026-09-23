package supermarket

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cinc-project/cinc-cli/cli/config"
)

func TestShareDryRunPackagesCookbookWithoutNetwork(t *testing.T) {
	cookbookRoot := writeSupermarketCookbook(t, "nginx")
	client := supermarketTestClient(t, "https://supermarket.example.test")

	result, err := client.Share(context.Background(), ShareOptions{
		Cookbook: "nginx", Category: "Other", CookbookPath: cookbookRoot, DryRun: true,
	})
	if err != nil {
		t.Fatalf("Share: %v", err)
	}
	if result.Uploaded {
		t.Fatal("dry-run reported upload")
	}
	if result.Tarball != "nginx.tgz" {
		t.Fatalf("tarball = %q, want nginx.tgz", result.Tarball)
	}
	if len(result.Files) != 2 || result.Files[0] != "nginx/metadata.json" {
		t.Fatalf("files = %v, want metadata and recipe", result.Files)
	}
}

func TestShareDryRunGeneratesMetadataJSONFromMetadataRB(t *testing.T) {
	cookbookRoot := writeSupermarketCookbookFromMetadataRB(t, "nginx")
	client := supermarketTestClient(t, "https://supermarket.example.test")

	result, err := client.Share(context.Background(), ShareOptions{
		Cookbook: "nginx", Category: "Other", CookbookPath: cookbookRoot, DryRun: true,
	})
	if err != nil {
		t.Fatalf("Share: %v", err)
	}
	if len(result.Files) != 3 || result.Files[0] != "nginx/metadata.json" || result.Files[1] != "nginx/metadata.rb" {
		t.Fatalf("files = %v, want generated metadata.json before metadata.rb", result.Files)
	}
}

func TestShareDryRunRespectsChefignore(t *testing.T) {
	root := writeSupermarketCookbookWithChefignore(t, "nginx")
	client := supermarketTestClient(t, "https://supermarket.example.test")

	result, err := client.Share(context.Background(), ShareOptions{
		Cookbook: "nginx", Category: "Other", CookbookPath: root, DryRun: true,
	})
	if err != nil {
		t.Fatalf("Share: %v", err)
	}
	for _, f := range result.Files {
		if strings.HasSuffix(f, ".bak") {
			t.Fatalf("dry-run files included %s, expected chefignore filter", f)
		}
	}
}

func TestShareDryRunSkipChefignoreIncludesEverything(t *testing.T) {
	root := writeSupermarketCookbookWithChefignore(t, "nginx")
	client := supermarketTestClient(t, "https://supermarket.example.test")

	result, err := client.Share(context.Background(), ShareOptions{
		Cookbook: "nginx", Category: "Other", CookbookPath: root, DryRun: true,
		SkipChefignore: true,
	})
	if err != nil {
		t.Fatalf("Share: %v", err)
	}
	var sawBak bool
	for _, f := range result.Files {
		if strings.HasSuffix(f, ".bak") {
			sawBak = true
			break
		}
	}
	if !sawBak {
		t.Fatalf("expected .bak file in archive when SkipChefignore is set, got %v", result.Files)
	}
}

func writeSupermarketCookbookWithChefignore(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(dir, "recipes"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"metadata.json":       `{"name":"` + name + `","version":"1.0.0"}`,
		"chefignore":          "*.bak\n",
		"recipes/default.rb":  "package 'nginx'\n",
		"recipes/default.bak": "old\n",
	}
	for sub, body := range files {
		if err := os.WriteFile(filepath.Join(dir, sub), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestDryRunPackagesCookbookWithoutClient(t *testing.T) {
	cookbookRoot := writeSupermarketCookbook(t, "nginx")

	result, err := DryRun(ShareOptions{
		Cookbook: "nginx", CookbookPath: cookbookRoot,
	})
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if result.Uploaded || result.Status != 0 {
		t.Fatalf("DryRun result reports upload: %+v", result)
	}
	if result.Tarball != "nginx.tgz" {
		t.Fatalf("tarball = %q, want nginx.tgz", result.Tarball)
	}
	if result.Version != "1.2.0" {
		t.Fatalf("version = %q, want 1.2.0 from metadata", result.Version)
	}
	if result.Category != "Other" {
		t.Fatalf("category = %q, want default Other", result.Category)
	}
	if len(result.Files) == 0 {
		t.Fatal("DryRun result has no files listed")
	}
}

func TestShareFallsBackToOtherWhenCookbookUnknownOnServer(t *testing.T) {
	cookbookRoot := writeSupermarketCookbook(t, "nginx")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cookbooks/nginx":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error_code":"NOT_FOUND","error_messages":["Resource not found"]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cookbooks":
			reader, err := r.MultipartReader()
			if err != nil {
				t.Fatalf("MultipartReader: %v", err)
			}
			for {
				part, err := reader.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				data, _ := io.ReadAll(part)
				if part.FormName() == "cookbook" && string(data) != `{"category":"Other"}` {
					t.Errorf("cookbook field = %q, want Other fallback category", data)
				}
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	client := supermarketTestClient(t, srv.URL)
	result, err := client.Share(context.Background(), ShareOptions{
		Cookbook: "nginx", CookbookPath: cookbookRoot,
	})
	if err != nil {
		t.Fatalf("Share: %v", err)
	}
	if result.Category != "Other" {
		t.Fatalf("result.Category = %q, want Other", result.Category)
	}
}

func TestNewRejectsInvalidSupermarketSite(t *testing.T) {
	_, err := New(config.Profile{
		SupermarketSite: "not a url",
		ClientName:      "tim",
		KeyPath:         writeSupermarketTestKey(t),
	}, "")
	if err == nil {
		t.Fatal("expected invalid site URL error")
	}
	if !strings.Contains(err.Error(), "invalid site URL") {
		t.Fatalf("error = %q, want invalid site URL", err)
	}
}

func TestNewRejectsProfileMissingIdentity(t *testing.T) {
	_, err := New(config.Profile{
		SupermarketSite: "https://supermarket.example.test",
		KeyPath:         "/keys/missing.pem",
	}, "")
	if err == nil {
		t.Fatal("expected validate identity error")
	}
	if !strings.Contains(err.Error(), "client_name") {
		t.Fatalf("error = %q, want client_name in message", err)
	}
}

func TestNewRejectsProfileWithoutAnySupermarketIdentity(t *testing.T) {
	// Neither base nor Supermarket-override identity is set, so there's
	// nothing to sign uploads with.
	_, err := New(config.Profile{
		SupermarketSite: "https://supermarket.example.test",
	}, "")
	if err == nil {
		t.Fatal("expected missing Supermarket identity error")
	}
	if !strings.Contains(err.Error(), "Supermarket identity") {
		t.Fatalf("error = %q, want Supermarket identity in message", err)
	}
}

// shareCapturedUserID runs a real (signed) Share against a recording server
// and returns the X-Ops-Userid header the upload was signed with. New
// loading the key file is exercised on the way: a profile whose effective
// key path is invalid never reaches this point.
func shareCapturedUserID(t *testing.T, profile config.Profile) string {
	t.Helper()
	cookbookRoot := writeSupermarketCookbook(t, "nginx")
	var userID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cookbooks/nginx":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error_code":"NOT_FOUND","error_messages":["Resource not found"]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cookbooks":
			userID = r.Header.Get("X-Ops-Userid")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	profile.SupermarketSite = srv.URL
	client, err := New(profile, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := client.Share(context.Background(), ShareOptions{
		Cookbook: "nginx", Category: "Other", CookbookPath: cookbookRoot,
	}); err != nil {
		t.Fatalf("Share: %v", err)
	}
	return userID
}

func TestNewSignsWithClientIdentityWhenNoOverride(t *testing.T) {
	got := shareCapturedUserID(t, config.Profile{
		ClientName: "tim",
		KeyPath:    writeSupermarketTestKey(t),
	})
	if got != "tim" {
		t.Fatalf("X-Ops-Userid = %q, want client_name tim", got)
	}
}

func TestNewSignsWithSupermarketOverrideIdentity(t *testing.T) {
	// The base client_key points at a missing file; if New loaded it
	// instead of the override key, the share would fail. A successful,
	// signed upload proves the override key was the one loaded.
	got := shareCapturedUserID(t, config.Profile{
		ClientName:            "tim",
		KeyPath:               "/keys/does-not-exist.pem",
		SupermarketClientName: "tim-public",
		SupermarketKey:        writeSupermarketTestKey(t),
	})
	if got != "tim-public" {
		t.Fatalf("X-Ops-Userid = %q, want supermarket_client_name tim-public", got)
	}
}

func TestNewSignsWithUsernameOverrideAndClientKey(t *testing.T) {
	// Only the username is overridden; the key falls back to client_key.
	got := shareCapturedUserID(t, config.Profile{
		ClientName:            "tim",
		KeyPath:               writeSupermarketTestKey(t),
		SupermarketClientName: "tim-public",
	})
	if got != "tim-public" {
		t.Fatalf("X-Ops-Userid = %q, want supermarket_client_name tim-public", got)
	}
}

func TestNewSignsWithKeyOverrideAndClientName(t *testing.T) {
	// Only the key is overridden; the username falls back to client_name.
	// The base client_key is invalid, so a successful upload proves the
	// override key was loaded.
	got := shareCapturedUserID(t, config.Profile{
		ClientName:     "tim",
		KeyPath:        "/keys/does-not-exist.pem",
		SupermarketKey: writeSupermarketTestKey(t),
	})
	if got != "tim" {
		t.Fatalf("X-Ops-Userid = %q, want client_name tim", got)
	}
}

func TestShareInfersCategoryAndPostsMultipartUpload(t *testing.T) {
	cookbookRoot := writeSupermarketCookbook(t, "nginx")
	var sawUpload bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cookbooks/nginx":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"category":"Web Servers"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cookbooks":
			sawUpload = true
			if r.Header.Get("X-Ops-Authorization-1") == "" {
				t.Error("upload request missing X-Ops-Authorization-1")
			}
			reader, err := r.MultipartReader()
			if err != nil {
				t.Fatalf("MultipartReader: %v", err)
			}
			fields := map[string]string{}
			for {
				part, err := reader.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				data, _ := io.ReadAll(part)
				if part.FormName() == "tarball" {
					if part.FileName() != "nginx.tgz" {
						t.Errorf("tarball filename = %q, want nginx.tgz", part.FileName())
					}
					if len(data) == 0 {
						t.Error("tarball field is empty")
					}
					continue
				}
				fields[part.FormName()] = string(data)
			}
			if fields["cookbook"] != `{"category":"Web Servers"}` {
				t.Errorf("cookbook field = %q", fields["cookbook"])
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	client := supermarketTestClient(t, srv.URL)
	result, err := client.Share(context.Background(), ShareOptions{
		Cookbook: "nginx", CookbookPath: cookbookRoot,
	})
	if err != nil {
		t.Fatalf("Share: %v", err)
	}
	if !sawUpload {
		t.Fatal("expected upload POST")
	}
	if !result.Uploaded || result.Status != http.StatusCreated || result.Category != "Web Servers" {
		t.Fatalf("result = %+v", result)
	}
}

func TestShareReportsVersionSiteAndCookbookURL(t *testing.T) {
	cookbookRoot := writeSupermarketCookbook(t, "nginx")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/cookbooks/nginx":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error_code":"NOT_FOUND","error_messages":["Resource not found"]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/cookbooks":
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	client := supermarketTestClient(t, srv.URL)
	result, err := client.Share(context.Background(), ShareOptions{
		Cookbook: "nginx", Category: "Other", CookbookPath: cookbookRoot,
	})
	if err != nil {
		t.Fatalf("Share: %v", err)
	}
	if result.Version != "1.2.0" {
		t.Fatalf("version = %q, want 1.2.0 from metadata", result.Version)
	}
	if result.Site != srv.URL {
		t.Fatalf("site = %q, want %q", result.Site, srv.URL)
	}
	want := srv.URL + "/cookbooks/nginx/versions/1.2.0"
	if result.URL != want {
		t.Fatalf("url = %q, want %q", result.URL, want)
	}
}

func TestShareSurfacesSupermarketValidationError(t *testing.T) {
	cookbookRoot := writeSupermarketCookbook(t, "nginx")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"error_code":"VALIDATION_ERROR","error_messages":["Version already exists"]}`)
	}))
	t.Cleanup(srv.Close)

	client := supermarketTestClient(t, srv.URL)
	_, err := client.Share(context.Background(), ShareOptions{
		Cookbook: "nginx", Category: "Other", CookbookPath: cookbookRoot,
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "Version already exists") {
		t.Fatalf("error = %q, want validation message", err)
	}
}

func TestNewUsesSupermarketSiteFromProfileWithoutServerURL(t *testing.T) {
	keyPath := writeSupermarketTestKey(t)
	client, err := New(config.Profile{
		SupermarketSite: "https://supermarket.example.test",
		ClientName:      "tim",
		KeyPath:         keyPath,
	}, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := client.base.String(); got != "https://supermarket.example.test" {
		t.Fatalf("base = %q, want profile SupermarketSite", got)
	}
}

func TestShareDryRunSharesByMetadataNameFromMismatchedDir(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "chef-mondoo")
	writeNamedSupermarketCookbookRB(t, dir, "mondoo")
	supermarketPushdir(t, dir)

	result, err := DryRun(ShareOptions{Cookbook: "mondoo"})
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if result.Cookbook != "mondoo" {
		t.Fatalf("cookbook = %q, want mondoo", result.Cookbook)
	}
	if result.Tarball != "mondoo.tgz" {
		t.Fatalf("tarball = %q, want mondoo.tgz", result.Tarball)
	}
	if len(result.Files) == 0 || result.TarballSize == 0 {
		t.Fatalf("expected a built tarball, got %+v", result)
	}
	for _, f := range result.Files {
		if !strings.HasPrefix(f, "mondoo/") {
			t.Fatalf("archive entry %q is not rooted at mondoo/", f)
		}
	}
}

func TestShareDryRunSharesByMetadataNameViaCookbookPath(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "chef-mondoo")
	writeNamedSupermarketCookbookRB(t, dir, "mondoo")

	result, err := DryRun(ShareOptions{Cookbook: "mondoo", CookbookPath: dir})
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if result.Cookbook != "mondoo" || result.Tarball != "mondoo.tgz" {
		t.Fatalf("result = %+v, want cookbook/tarball named mondoo", result)
	}
	if len(result.Files) == 0 {
		t.Fatal("expected a built tarball")
	}
}

func TestShareRejectsSharingByDirectoryNameWhenMetadataDiffers(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "chef-mondoo")
	writeNamedSupermarketCookbookRB(t, dir, "mondoo")

	_, err := DryRun(ShareOptions{Cookbook: "chef-mondoo", CookbookPath: dir})
	if err == nil {
		t.Fatal("expected a guard error sharing by the directory name")
	}
	want := `metadata name "mondoo" does not match requested cookbook "chef-mondoo"`
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want it to contain %q", err, want)
	}
}

func writeNamedSupermarketCookbookRB(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "recipes"), 0o755); err != nil {
		t.Fatal(err)
	}
	rb := "name '" + name + "'\nversion '1.0.0'\n"
	if err := os.WriteFile(filepath.Join(dir, "metadata.rb"), []byte(rb), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "recipes", "default.rb"), []byte("package 'mondoo'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func supermarketPushdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

func writeSupermarketCookbook(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(dir, "recipes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(`{"name":"`+name+`","version":"1.2.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "recipes", "default.rb"), []byte("package 'nginx'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeSupermarketCookbookFromMetadataRB(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(dir, "recipes"), 0o755); err != nil {
		t.Fatal(err)
	}
	metadata := "name '" + name + "'\nversion '1.2.0'\ndescription 'Generated metadata test'\n"
	if err := os.WriteFile(filepath.Join(dir, "metadata.rb"), []byte(metadata), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "recipes", "default.rb"), []byte("package 'nginx'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func supermarketTestClient(t *testing.T, site string) *Client {
	t.Helper()
	keyPath := writeSupermarketTestKey(t)
	client, err := New(config.Profile{
		SupermarketSite: site,
		ClientName:      "tim",
		KeyPath:         keyPath,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func writeSupermarketTestKey(t *testing.T) string {
	t.Helper()
	keyPath := filepath.Join(t.TempDir(), "key.pem")
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return keyPath
}

// TestNewExpandsTildeInSupermarketKey checks a supermarket_key written as
// ~/... is read from the home directory.
func TestNewExpandsTildeInSupermarketKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	key, err := os.ReadFile(writeSupermarketTestKey(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "supermarket.pem"), key, 0o600); err != nil {
		t.Fatal(err)
	}
	got := shareCapturedUserID(t, config.Profile{
		ClientName:     "tim",
		KeyPath:        "/keys/does-not-exist.pem",
		SupermarketKey: "~/supermarket.pem",
	})
	if got != "tim" {
		t.Fatalf("X-Ops-Userid = %q, want tim", got)
	}
}
