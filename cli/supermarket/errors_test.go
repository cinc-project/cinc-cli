package supermarket

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	sm "github.com/cinc-project/cinc-supermarket-api"

	"github.com/cinc-project/cinc-cli/cli/config"
)

// assertMessage fails unless msg mentions every want, and never uses an em
// dash: these are full-sentence messages for people new to Cinc.
func assertMessage(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	for _, want := range wants {
		if !strings.Contains(msg, want) {
			t.Errorf("error should mention %q, got:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "—") {
		t.Errorf("error should not use an em dash, got:\n%s", msg)
	}
}

func TestNewExplainsMissingSupermarketKey(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "supermarket.pem")
	_, err := New(config.Profile{
		ClientName:     "tim",
		KeyPath:        writeSupermarketTestKey(t),
		SupermarketKey: missing,
	}, "https://supermarket.example.test", WithProfileSource("supermarket", "/home/tim/.cinc/credentials"))
	assertMessage(t, err,
		"can't find your Supermarket key at "+missing,
		"supermarket_key",
		`"supermarket" profile in /home/tim/.cinc/credentials`,
		"https://supermarket.example.test",
		`"tim"`,
		"--dry-run",
	)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("errors.Is(err, fs.ErrNotExist) = false for %v", err)
	}
}

func TestNewExplainsMissingKeyFromClientKeyFallback(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "tim.pem")
	_, err := New(config.Profile{ClientName: "tim", KeyPath: missing}, "https://supermarket.example.test")
	assertMessage(t, err,
		"can't find your Supermarket key at "+missing,
		"client_key",
		// The fallback is invisible unless we say so, and the override is
		// the usual fix when the Cinc server key isn't the Supermarket one.
		"supermarket_key isn't set",
		"supermarket_client_name",
	)
}

func TestNewExplainsUnreadableSupermarketKey(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file mode permissions don't apply")
	}
	key := writeSupermarketTestKey(t)
	if err := os.Chmod(key, 0o000); err != nil {
		t.Fatal(err)
	}
	_, err := New(config.Profile{ClientName: "tim", SupermarketKey: key}, "https://supermarket.example.test")
	assertMessage(t, err,
		"can't read your Supermarket key at "+key,
		"permission",
		"chmod 600 "+key,
		"supermarket_key",
	)
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("errors.Is(err, fs.ErrPermission) = false for %v", err)
	}
}

func TestNewExplainsKeyThatIsNotAPrivateKey(t *testing.T) {
	key := filepath.Join(t.TempDir(), "tim.pub")
	if err := os.WriteFile(key, []byte("ssh-rsa AAAAB3Nza tim@laptop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New(config.Profile{ClientName: "tim", SupermarketKey: key}, "https://supermarket.example.test")
	assertMessage(t, err,
		"Supermarket key at "+key+" isn't a private key",
		"-----BEGIN RSA PRIVATE KEY-----",
		"supermarket_key",
		`"tim"`,
	)
}

func TestNewAnonymousExplainsInvalidSite(t *testing.T) {
	for _, site := range []string{"not a url", "ftp://supermarket.example.test", "supermarket.example.test"} {
		_, err := NewAnonymous(site)
		assertMessage(t, err,
			`"`+site+`" isn't a Supermarket address we can use`,
			"https://supermarket.chef.io",
			"--supermarket-site",
			"supermarket_site",
		)
	}
}

func TestEmptyCookbookNameGivesAnExample(t *testing.T) {
	client := mustAnonymousClient(t, "https://supermarket.example.test")
	_, err := client.Show(context.Background(), ShowOptions{})
	assertMessage(t, err, "which cookbook", "supermarket show nginx")
	_, err = client.Download(context.Background(), DownloadOptions{})
	assertMessage(t, err, "which cookbook", "supermarket download nginx")
	_, err = client.Install(context.Background(), nil, InstallOptions{})
	assertMessage(t, err, "which cookbook", "supermarket install nginx")
}

func TestEmptySearchQueryGivesAnExample(t *testing.T) {
	_, err := mustAnonymousClient(t, "https://supermarket.example.test").Search(context.Background(), SearchOptions{})
	assertMessage(t, err, "what to search for", "supermarket search nginx")
}

// notFoundBody is what Supermarket's Api::V1Controller#render_404 sends for
// a cookbook or version that doesn't exist.
const notFoundBody = `{"error_messages":["Resource does not exist."],"error_code":"NOT_FOUND"}`

// nginxCookbookBody is a trimmed GET /api/v1/cookbooks/nginx response, in
// the shape the public Supermarket returns it.
const nginxCookbookBody = `{
  "name": "nginx",
  "maintainer": "sous-chefs",
  "category": "Web Servers",
  "latest_version": "https://supermarket.chef.io/api/v1/cookbooks/nginx/versions/12.3.1",
  "versions": [
    "https://supermarket.chef.io/api/v1/cookbooks/nginx/versions/12.3.1",
    "https://supermarket.chef.io/api/v1/cookbooks/nginx/versions/12.3.0"
  ]
}`

// catalogServer serves nginx (and nothing else) the way Supermarket does:
// the cookbook record, 404 for any version, 404 for any other cookbook.
func catalogServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/cookbooks/nginx" {
			_, _ = io.WriteString(w, nginxCookbookBody)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, notFoundBody)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestShowExplainsUnknownCookbook(t *testing.T) {
	srv := catalogServer(t)
	_, err := mustAnonymousClient(t, srv.URL).Show(context.Background(), ShowOptions{Cookbook: "ghost"})
	assertMessage(t, err,
		`we couldn't find a cookbook named "ghost" on `+srv.URL,
		"supermarket search ghost",
	)
	if strings.Contains(err.Error(), "/api/v1") {
		t.Errorf("error should not leak the API path, got:\n%s", err)
	}
	if !errors.Is(err, sm.ErrNotFound) {
		t.Errorf("errors.Is(err, sm.ErrNotFound) = false for %v", err)
	}
}

func TestShowExplainsUnknownVersionOfKnownCookbook(t *testing.T) {
	srv := catalogServer(t)
	_, err := mustAnonymousClient(t, srv.URL).Show(context.Background(), ShowOptions{Cookbook: "nginx", Version: "9.9.9"})
	assertMessage(t, err,
		`nginx is on `+srv.URL+`, but it has no version 9.9.9`,
		"12.3.1",
		"supermarket show nginx",
	)
	if !errors.Is(err, sm.ErrNotFound) {
		t.Errorf("errors.Is(err, sm.ErrNotFound) = false for %v", err)
	}
}

func TestShowVersionOfUnknownCookbookNamesTheCookbook(t *testing.T) {
	srv := catalogServer(t)
	_, err := mustAnonymousClient(t, srv.URL).Show(context.Background(), ShowOptions{Cookbook: "ghost", Version: "1.0.0"})
	assertMessage(t, err, `we couldn't find a cookbook named "ghost"`, "supermarket search ghost")
}

func TestDownloadExplainsUnknownVersion(t *testing.T) {
	srv := catalogServer(t)
	_, err := mustAnonymousClient(t, srv.URL).Download(context.Background(), DownloadOptions{
		Cookbook: "nginx", Version: "9.9.9", File: filepath.Join(t.TempDir(), "n.tgz"),
	})
	assertMessage(t, err, "it has no version 9.9.9", "12.3.1")
}

func TestDownloadLatestExplainsUnknownCookbook(t *testing.T) {
	srv := catalogServer(t)
	_, err := mustAnonymousClient(t, srv.URL).Download(context.Background(), DownloadOptions{
		Cookbook: "ghost", File: filepath.Join(t.TempDir(), "g.tgz"),
	})
	assertMessage(t, err, `we couldn't find a cookbook named "ghost"`, "supermarket search ghost")
}

func TestUnreachableSupermarketNamesTheSite(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	site := srv.URL
	srv.Close()

	_, err := mustAnonymousClient(t, site).Search(context.Background(), SearchOptions{Query: "nginx"})
	assertMessage(t, err,
		"we couldn't reach the Supermarket at "+site,
		"--supermarket-site",
	)
}

func TestSupermarketServerErrorSaysItIsTemporary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "<html>502 Bad Gateway</html>")
	}))
	t.Cleanup(srv.Close)

	_, err := mustAnonymousClient(t, srv.URL).List(context.Background(), ListOptions{})
	assertMessage(t, err, srv.URL, "HTTP 502", "try again")
}

func TestDownloadRefusalNamesBothWaysOut(t *testing.T) {
	out := filepath.Join(t.TempDir(), "nginx-1.2.0.tar.gz")
	if err := os.WriteFile(out, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := downloadFixtureServer(t, downloadFixture{Cookbook: "nginx"})
	t.Cleanup(srv.Close)
	_, err := mustAnonymousClient(t, srv.URL).Download(context.Background(), DownloadOptions{
		Cookbook: "nginx", Version: "1.2.0", File: out,
	})
	assertMessage(t, err, out+" already exists", "--force", "--file")
}

func TestDownloadIntoMissingDirectoryExplains(t *testing.T) {
	srv := downloadFixtureServer(t, downloadFixture{Cookbook: "nginx", Tarball: []byte("tgz")})
	t.Cleanup(srv.Close)
	out := filepath.Join(t.TempDir(), "nope", "nginx.tgz")
	_, err := mustAnonymousClient(t, srv.URL).Download(context.Background(), DownloadOptions{
		Cookbook: "nginx", Version: "1.2.0", File: out,
	})
	assertMessage(t, err, "couldn't save nginx 1.2.0 to "+out, "--file")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("errors.Is(err, fs.ErrNotExist) = false for %v", err)
	}
}

func TestInstallExplainsTarballThatIsNotACookbook(t *testing.T) {
	srv := downloadFixtureServer(t, downloadFixture{Cookbook: "nginx", Tarball: []byte("this is not a tarball")})
	t.Cleanup(srv.Close)
	_, err := mustAnonymousClient(t, srv.URL).Install(context.Background(), mustCincClient(t, "http://server.invalid"),
		InstallOptions{Cookbook: "nginx", Version: "1.2.0"})
	assertMessage(t, err, "nginx 1.2.0", "from "+srv.URL, "couldn't unpack")
}

func TestInstallExplainsServerUploadFailure(t *testing.T) {
	smSrv := downloadFixtureServer(t, downloadFixture{
		Cookbook: "nginx", Tarball: buildCookbookTarball(t, "nginx", "1.2.0"),
	})
	t.Cleanup(smSrv.Close)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":["missing create permission"]}`)
	}))
	t.Cleanup(server.Close)

	_, err := mustAnonymousClient(t, smSrv.URL).Install(context.Background(), mustCincClient(t, server.URL),
		InstallOptions{Cookbook: "nginx", Version: "1.2.0"})
	assertMessage(t, err,
		"downloaded nginx 1.2.0 from "+smSrv.URL,
		"couldn't upload it to your Cinc server",
		"missing create permission",
	)
}

func TestShareRejectsMismatchedNameWithTheCommandThatWorks(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "chef-mondoo")
	writeNamedSupermarketCookbookRB(t, dir, "mondoo")

	_, err := DryRun(ShareOptions{Cookbook: "chef-mondoo", CookbookPath: dir})
	assertMessage(t, err,
		`is named "mondoo" in its metadata, not "chef-mondoo"`,
		dir,
		"supermarket share mondoo",
	)
}

// shareAgainst runs a real signed Share of nginx 1.2.0 against a
// Supermarket that answers the upload with status and body.
func shareAgainst(t *testing.T, status int, body string) error {
	t.Helper()
	cookbookRoot := writeSupermarketCookbook(t, "nginx")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, notFoundBody)
			return
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	client := supermarketTestClient(t, srv.URL)
	_, err := client.Share(context.Background(), ShareOptions{
		Cookbook: "nginx", Category: "Other", CookbookPath: cookbookRoot,
	})
	return err
}

// The bodies below are the ones upstream Supermarket's
// CookbookUploadsController sends, from its en.yml locale strings.
func TestShareExplainsSupermarketRejections(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		sentinel error
		wants    []string
	}{
		{
			name:     "unknown user",
			status:   http.StatusUnauthorized,
			body:     `{"error_code":"AUTHENTICATION_FAILED","error_messages":["Could not find user 'tim'."]}`,
			sentinel: sm.ErrUnauthorized,
			wants:    []string{`doesn't have a user named "tim"`, "supermarket_client_name", "sign in"},
		},
		{
			name:     "never signed in",
			status:   http.StatusUnauthorized,
			body:     `{"error_code":"AUTHENTICATION_FAILED","error_messages":["Before you can perform knife activities that require authentication, you must log into https://supermarket.example.test and grant it permission to access your Chef account information."]}`,
			sentinel: sm.ErrUnauthorized,
			wants:    []string{`doesn't have a key on file for "tim"`, "Sign in to"},
		},
		{
			name:     "wrong key",
			status:   http.StatusUnauthorized,
			body:     `{"error_code":"AUTHENTICATION_FAILED","error_messages":["Authentication failed due to an invalid public/private key pair. If you have changed your keys recently try logging out and logging back in to Supermarket."]}`,
			sentinel: sm.ErrUnauthorized,
			wants:    []string{`didn't accept the key`, "client_key", "supermarket_key", "sign out"},
		},
		{
			name:     "bad signature",
			status:   http.StatusBadRequest,
			body:     `{"error_code":"AUTHENTICATION_FAILED","error_messages":["Authentication failed due to an invalid signed request."]}`,
			sentinel: sm.ErrUnauthorized,
			wants:    []string{"couldn't verify the signature", "clock"},
		},
		{
			name:     "not a collaborator",
			status:   http.StatusUnauthorized,
			body:     `{"error_code":"UNAUTHORIZED","error_messages":["You are not authorized to upload this cookbook."]}`,
			sentinel: sm.ErrUnauthorized,
			wants:    []string{`"tim" isn't allowed to publish nginx`, "collaborator", "supermarket show nginx"},
		},
		{
			name:     "version already published",
			status:   http.StatusBadRequest,
			body:     `{"error_code":"INVALID_DATA","error_messages":["nginx (1.2.0) already exists. A cookbook's version number must be unique."]}`,
			sentinel: sm.ErrInvalidData,
			wants:    []string{"nginx 1.2.0 is already published", "metadata.rb"},
		},
		{
			name:     "unknown category",
			status:   http.StatusBadRequest,
			body:     `{"error_code":"INVALID_DATA","error_messages":["Category 'Other' does not exist."]}`,
			sentinel: sm.ErrInvalidData,
			wants:    []string{"turned down nginx 1.2.0", "Category 'Other' does not exist.", "supermarket share nginx 'Web Servers'"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := shareAgainst(t, tc.status, tc.body)
			assertMessage(t, err, tc.wants...)
			if !errors.Is(err, tc.sentinel) {
				t.Errorf("errors.Is(err, %v) = false for %v", tc.sentinel, err)
			}
		})
	}
}

func TestUntrustedSupermarketCertificateSaysSo(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)

	_, err := mustAnonymousClient(t, srv.URL).Show(context.Background(), ShowOptions{Cookbook: "nginx"})
	assertMessage(t, err, "couldn't verify the TLS certificate of the Supermarket at "+srv.URL, "supermarket_site")
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		t.Errorf("error should not expose *url.Error, or the top-level Cinc server explainer rewrites it: %v", err)
	}
}
