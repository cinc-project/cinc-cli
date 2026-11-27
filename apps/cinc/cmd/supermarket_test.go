package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cinc-project/cinc-cli/cli/client"
)

func TestSupermarketShareDryRunCommand(t *testing.T) {
	cookbookPath := writeCommandSupermarketCookbook(t, "nginx")
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{
		"supermarket", "share", "nginx", "Other",
		"--dry-run", "--cookbook-path", cookbookPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc supermarket share --dry-run: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "nginx 1.2.0") {
		t.Fatalf("output = %q, want cookbook name and version", got)
	}
	if !strings.Contains(got, "nginx.tgz") {
		t.Fatalf("output = %q, want tarball name", got)
	}
	if !strings.Contains(got, "  nginx/metadata.json\n") {
		t.Fatalf("output = %q, want indented archive file list", got)
	}
}

func TestSupermarketShareDryRunCommandJSON(t *testing.T) {
	cookbookPath := writeCommandSupermarketCookbook(t, "nginx")
	cfgPath := filepath.Join(t.TempDir(), "credentials")
	cfg := fmt.Sprintf(`[default]
cinc_server_url = "https://chef.example.test/organizations/acme"
client_name     = "tim"
client_key      = %q
`, writeTestKey(t))
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{
		"supermarket", "share", "nginx", "Other",
		"--dry-run", "--cookbook-path", cookbookPath,
		"--format", "json", "--config", cfgPath,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc supermarket share --dry-run --format json: %v", err)
	}
	var result struct {
		Cookbook string `json:"cookbook"`
		Version  string `json:"version"`
		Category string `json:"category"`
		Uploaded bool   `json:"uploaded"`
		Status   int    `json:"status"`
		Tarball  string `json:"tarball"`
	}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("decode JSON output: %v\n%s", err, buf.String())
	}
	if result.Cookbook != "nginx" || result.Version != "1.2.0" || result.Category != "Other" || result.Uploaded || result.Status != 0 || result.Tarball != "nginx.tgz" {
		t.Fatalf("result = %+v", result)
	}
}

func TestSupermarketShareDryRunFiltersChefignoreByDefault(t *testing.T) {
	root := writeCommandSupermarketCookbookWithChefignore(t, "nginx")
	cmdRoot := newRootCmd()
	var buf bytes.Buffer
	cmdRoot.SetOut(&buf)
	cmdRoot.SetArgs([]string{
		"supermarket", "share", "nginx", "Other",
		"--dry-run", "--cookbook-path", root,
	})

	if err := cmdRoot.Execute(); err != nil {
		t.Fatalf("cinc supermarket share --dry-run: %v", err)
	}
	if strings.Contains(buf.String(), "default.bak") {
		t.Fatalf("output includes chefignored file:\n%s", buf.String())
	}
}

func TestSupermarketShareDryRunNoChefignoreIncludesIgnoredFiles(t *testing.T) {
	root := writeCommandSupermarketCookbookWithChefignore(t, "nginx")
	cmdRoot := newRootCmd()
	var buf bytes.Buffer
	cmdRoot.SetOut(&buf)
	cmdRoot.SetArgs([]string{
		"supermarket", "share", "nginx", "Other",
		"--dry-run", "--no-chefignore", "--cookbook-path", root,
	})

	if err := cmdRoot.Execute(); err != nil {
		t.Fatalf("cinc supermarket share --dry-run --no-chefignore: %v", err)
	}
	if !strings.Contains(buf.String(), "default.bak") {
		t.Fatalf("--no-chefignore did not include ignored file:\n%s", buf.String())
	}
}

func writeCommandSupermarketCookbookWithChefignore(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(dir, "recipes"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"metadata.json":       `{"name":"` + name + `","version":"1.2.0"}`,
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

func TestSupermarketShareUsesSupermarketOverrideIdentity(t *testing.T) {
	cookbookPath := writeCommandSupermarketCookbook(t, "nginx")
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

	cfgPath := filepath.Join(t.TempDir(), "credentials")
	// The base client_key is bogus on purpose: a successful, signed upload
	// proves the share path loaded supermarket_key, not client_key.
	cfg := fmt.Sprintf(`[default]
cinc_server_url         = "https://chef.example.test/organizations/acme"
client_name             = "tim"
client_key              = "/keys/does-not-exist.pem"
supermarket_client_name = "tim-public"
supermarket_key         = %q
supermarket_site        = %q
`, writeTestKey(t), srv.URL)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{
		"supermarket", "share", "nginx", "Other",
		"--cookbook-path", cookbookPath, "--config", cfgPath,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc supermarket share: %v", err)
	}
	if userID != "tim-public" {
		t.Fatalf("X-Ops-Userid = %q, want supermarket_client_name tim-public", userID)
	}
	got := buf.String()
	if !strings.Contains(got, "Uploaded to "+srv.URL) {
		t.Fatalf("output = %q, want confirmation naming the Supermarket URL", got)
	}
	if !strings.Contains(got, "nginx 1.2.0 is now live: "+srv.URL+"/cookbooks/nginx/versions/1.2.0") {
		t.Fatalf("output = %q, want the published cookbook version URL", got)
	}
}

func TestSupermarketExploreCommandRegistered(t *testing.T) {
	root := newRootCmd()
	sub, _, err := root.Find([]string{"supermarket", "explore"})
	if err != nil {
		t.Fatalf("Find supermarket explore: %v", err)
	}
	if sub.Use != "explore" {
		t.Fatalf("Use = %q, want explore", sub.Use)
	}
	if flag := sub.Flags().Lookup("supermarket-site"); flag == nil {
		t.Fatal("--supermarket-site flag missing")
	}
}

func TestSupermarketExploreRejectsNonTTYOutput(t *testing.T) {
	root := newRootCmd()
	// bytes.Buffer is not an *os.File, so the TTY check returns false
	// and the command should refuse to launch the bubbletea program.
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs([]string{"supermarket", "explore"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected non-TTY error, got nil")
	}
	if !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("error = %q, want mention of interactive terminal", err)
	}
}

func writeCommandSupermarketCookbook(t *testing.T, name string) string {
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

// TestSupermarketShareMissingKeyNamesProfileAndFile checks the missing-key
// message points at the profile share actually picked (the conventional
// [supermarket] section here) and the credentials file to edit.
func TestSupermarketShareMissingKeyNamesProfileAndFile(t *testing.T) {
	cookbookPath := writeCommandSupermarketCookbook(t, "nginx")
	missing := filepath.Join(t.TempDir(), "supermarket.pem")
	cfgPath := filepath.Join(t.TempDir(), "credentials")
	cfg := fmt.Sprintf(`[default]
cinc_server_url = "https://chef.example.test/organizations/acme"
client_name     = "tim"
client_key      = %q

[supermarket]
supermarket_site = "https://supermarket.example.test"
client_name      = "tim"
supermarket_key  = %q
`, writeTestKey(t), missing)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CINC_PROFILE", "")
	t.Setenv("CHEF_PROFILE", "")

	root := newRootCmd()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{
		"supermarket", "share", "nginx", "Other",
		"--cookbook-path", cookbookPath, "--config", cfgPath,
	})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected a missing key error")
	}
	for _, want := range []string{
		"can't find your Supermarket key at " + missing,
		`"supermarket" profile in ` + cfgPath,
		"supermarket_key in " + cfgPath,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got:\n%s", want, err)
		}
	}
}

// TestSupermarketUnreachableSurvivesExplain checks the Supermarket's own
// network message reaches the user: Execute passes every error through
// client.Explain, whose advice is about the Cinc server URL, not
// --supermarket-site.
func TestSupermarketUnreachableSurvivesExplain(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	site := srv.URL
	srv.Close()

	root := newRootCmd()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"supermarket", "search", "nginx", "--supermarket-site", site, "--config", filepath.Join(t.TempDir(), "none")})
	err := client.Explain(root.Execute())
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"we couldn't reach the Supermarket at " + site, "--supermarket-site"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got:\n%s", want, err)
		}
	}
}
