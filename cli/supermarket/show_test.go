package supermarket

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sm "github.com/cinc-project/cinc-supermarket-api"
)

func TestShowReturnsCookbookWhenNoVersion(t *testing.T) {
	srv := showFixtureServer(t, showFixture{
		Cookbook: `{
			"name": "nginx",
			"maintainer": "sous-chefs",
			"description": "Installs and configures NGINX",
			"category": "Web Servers",
			"latest_version": "https://supermarket.example.test/api/v1/cookbooks/nginx/versions/12.0.4",
			"external_url": "https://github.com/sous-chefs/nginx",
			"versions": [
				"https://supermarket.example.test/api/v1/cookbooks/nginx/versions/12.0.4",
				"https://supermarket.example.test/api/v1/cookbooks/nginx/versions/12.0.3"
			],
			"metrics": {"downloads": {"total": 89341221}}
		}`,
	})
	defer srv.Close()

	client := mustAnonymousClient(t, srv.URL)
	result, err := client.Show(context.Background(), ShowOptions{Cookbook: "nginx"})
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if result.Cookbook == nil {
		t.Fatal("Cookbook field unset")
	}
	if result.Version != nil {
		t.Fatal("Version field should be nil when no version arg")
	}
	if result.Cookbook.Name != "nginx" {
		t.Fatalf("Name = %q", result.Cookbook.Name)
	}
	if result.Cookbook.Maintainer != "sous-chefs" {
		t.Fatalf("Maintainer = %q", result.Cookbook.Maintainer)
	}
}

func TestShowReturnsVersionWhenVersionProvided(t *testing.T) {
	srv := showFixtureServer(t, showFixture{
		Version: `{
			"version": "12.0.4",
			"license": "Apache-2.0",
			"tarball_file_size": 145842,
			"dependencies": {"ohai": ">= 5.2.0"},
			"platforms": {"ubuntu": ">= 0.0.0"}
		}`,
	})
	defer srv.Close()

	client := mustAnonymousClient(t, srv.URL)
	result, err := client.Show(context.Background(), ShowOptions{Cookbook: "nginx", Version: "12.0.4"})
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if result.Version == nil {
		t.Fatal("Version field unset")
	}
	if result.Cookbook != nil {
		t.Fatal("Cookbook field should be nil when version arg given")
	}
	if result.Version.Version != "12.0.4" {
		t.Fatalf("Version = %q", result.Version.Version)
	}
	if result.Version.License != "Apache-2.0" {
		t.Fatalf("License = %q", result.Version.License)
	}
	if got := result.Version.Dependencies["ohai"]; got != ">= 5.2.0" {
		t.Fatalf("ohai dep = %q", got)
	}
}

func TestShowRejectsEmptyCookbook(t *testing.T) {
	client := mustAnonymousClient(t, "https://supermarket.example.test")
	if _, err := client.Show(context.Background(), ShowOptions{}); err == nil {
		t.Fatal("expected error for empty cookbook")
	}
}

func TestShowWrapsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error_code":"NOT_FOUND"}`)
	}))
	defer srv.Close()

	client := mustAnonymousClient(t, srv.URL)
	_, err := client.Show(context.Background(), ShowOptions{Cookbook: "ghost"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, sm.ErrNotFound) {
		t.Fatalf("err = %v, want errors.Is(err, sm.ErrNotFound)", err)
	}
}

type showFixture struct {
	Cookbook string // raw JSON for /api/v1/cookbooks/<name>
	Version  string // raw JSON for /api/v1/cookbooks/<name>/versions/<v>
}

func showFixtureServer(t *testing.T, f showFixture) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/versions/") && f.Version != "":
			_, _ = io.WriteString(w, f.Version)
		case strings.HasPrefix(r.URL.Path, "/api/v1/cookbooks/") && f.Cookbook != "":
			_, _ = io.WriteString(w, f.Cookbook)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	return srv
}
