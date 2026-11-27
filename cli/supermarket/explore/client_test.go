package explore

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	sm "github.com/cinc-project/cinc-supermarket-api"
)

func TestRealClientListSendsOrderAndPagination(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/cookbooks" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"start":0,"total":2,"items":[{"cookbook_name":"a","cookbook_maintainer":"x"},{"cookbook_name":"b","cookbook_maintainer":"y"}]}`)
	}))
	t.Cleanup(srv.Close)

	c, err := newRealClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	page, err := c.List(context.Background(), sm.ListOptions{Order: "most_downloaded", Start: 50, Items: 50})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := gotQuery.Get("order"); got != "most_downloaded" {
		t.Errorf("order = %q, want most_downloaded", got)
	}
	if got := gotQuery.Get("start"); got != "50" {
		t.Errorf("start = %q, want 50", got)
	}
	if got := gotQuery.Get("items"); got != "50" {
		t.Errorf("items = %q, want 50", got)
	}
	if len(page.Items) != 2 || page.Items[0].Name != "a" {
		t.Fatalf("page items = %+v", page.Items)
	}
}

func TestRealClientGetMapsNotFoundToSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error_messages":["not found"]}`)
	}))
	t.Cleanup(srv.Close)

	c, err := newRealClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Get(context.Background(), "missing")
	if !errors.Is(err, errNotFound) {
		t.Fatalf("error = %v, want errNotFound", err)
	}
}

func TestRealClientSearchCallsSearchEndpoint(t *testing.T) {
	var sawQ string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/search") {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		sawQ = r.URL.Query().Get("q")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"start":0,"total":1,"items":[{"cookbook_name":"nginx","cookbook_maintainer":"sc"}]}`)
	}))
	t.Cleanup(srv.Close)

	c, err := newRealClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	page, err := c.Search(context.Background(), sm.SearchOptions{Q: "nginx"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if sawQ != "nginx" {
		t.Errorf("q = %q, want nginx", sawQ)
	}
	if page.Items[0].Name != "nginx" {
		t.Fatalf("items = %+v", page.Items)
	}
}

func TestSortOrderParam(t *testing.T) {
	cases := map[sortOrder]string{
		sortMostDownloaded:  "most_downloaded",
		sortRecentlyUpdated: "recently_updated",
		sortAlphabetical:    "",
	}
	for s, want := range cases {
		if got := s.orderParam(); got != want {
			t.Errorf("%v.orderParam() = %q, want %q", s, got, want)
		}
	}
}

func TestRealClientNotFoundNamesCookbookAndSite(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error_messages":["Resource does not exist."],"error_code":"NOT_FOUND"}`)
	}))
	t.Cleanup(srv.Close)

	c, err := newRealClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Get(context.Background(), "ghost")
	if err == nil || !strings.Contains(err.Error(), `"ghost" isn't on `+srv.URL) {
		t.Fatalf("error = %v, want it to name the cookbook and site", err)
	}
}

func TestRealClientUnreachableSiteSaysSo(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	site := srv.URL
	srv.Close()

	c, err := newRealClient(site)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.List(context.Background(), sm.ListOptions{})
	if err == nil || !strings.Contains(err.Error(), "we couldn't reach the Supermarket at "+site) {
		t.Fatalf("error = %v, want it to say the site is unreachable", err)
	}
	if strings.Contains(err.Error(), "/api/v1") {
		t.Errorf("error should not leak the API path: %v", err)
	}
}

func TestRunRejectsInvalidSiteBeforeAnythingElse(t *testing.T) {
	err := Run(context.Background(), Options{Site: "supermarket.example.test", Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), `"supermarket.example.test" isn't a Supermarket address`) {
		t.Fatalf("error = %v, want the invalid site explanation", err)
	}
}
