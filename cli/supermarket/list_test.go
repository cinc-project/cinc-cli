package supermarket

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestListReturnsAllEntriesFromSinglePage(t *testing.T) {
	srv := listFixtureServer(t, listFixture{
		Cookbooks: []sumEntry{
			{Name: "apache2", Maintainer: "sous-chefs", Description: "Web server"},
			{Name: "nginx", Maintainer: "sous-chefs", Description: "Web server"},
		},
	})
	defer srv.Close()

	client := mustAnonymousClient(t, srv.URL)
	result, err := client.List(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result.Entries) != 2 {
		t.Fatalf("len = %d, want 2", len(result.Entries))
	}
	if result.Entries[0].Name != "apache2" || result.Entries[1].Name != "nginx" {
		t.Fatalf("entries = %+v", result.Entries)
	}
	if result.Total != 2 {
		t.Fatalf("Total = %d, want 2", result.Total)
	}
}

func TestListAutoPaginatesUntilExhausted(t *testing.T) {
	all := make([]sumEntry, 0, 250)
	for i := range 250 {
		all = append(all, sumEntry{Name: fmt.Sprintf("cookbook-%03d", i), Maintainer: "m"})
	}
	srv := listFixtureServer(t, listFixture{Cookbooks: all})
	defer srv.Close()

	client := mustAnonymousClient(t, srv.URL)
	result, err := client.List(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result.Entries) != 250 {
		t.Fatalf("len = %d, want 250", len(result.Entries))
	}
	if result.Entries[0].Name != "cookbook-000" || result.Entries[249].Name != "cookbook-249" {
		t.Fatalf("first/last = %q/%q", result.Entries[0].Name, result.Entries[249].Name)
	}
}

func TestListHonorsLimit(t *testing.T) {
	all := make([]sumEntry, 0, 250)
	for i := range 250 {
		all = append(all, sumEntry{Name: fmt.Sprintf("cookbook-%03d", i)})
	}
	srv := listFixtureServer(t, listFixture{Cookbooks: all})
	defer srv.Close()

	client := mustAnonymousClient(t, srv.URL)
	result, err := client.List(context.Background(), ListOptions{Limit: 50})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result.Entries) != 50 {
		t.Fatalf("len = %d, want 50", len(result.Entries))
	}
}

func TestListPassesOrderAndUserQueryParams(t *testing.T) {
	var captured url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/cookbooks" {
			captured = r.URL.Query()
			writeJSON(w, map[string]any{"start": 0, "total": 0, "items": []any{}})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	client := mustAnonymousClient(t, srv.URL)
	if _, err := client.List(context.Background(), ListOptions{
		Order: "most_downloaded",
		User:  "sous-chefs",
	}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := captured.Get("order"); got != "most_downloaded" {
		t.Fatalf("order = %q, want most_downloaded", got)
	}
	if got := captured.Get("user"); got != "sous-chefs" {
		t.Fatalf("user = %q, want sous-chefs", got)
	}
}

func TestListVerboseAttachesLatestVersionFromUniverse(t *testing.T) {
	srv := listFixtureServer(t, listFixture{
		Cookbooks: []sumEntry{
			{Name: "apache2", Maintainer: "sous-chefs"},
			{Name: "nginx", Maintainer: "sous-chefs"},
		},
		Universe: map[string][]string{
			"apache2": {"9.0.3", "9.0.4", "9.0.4-beta", "10.0.0"},
			"nginx":   {"12.0.4", "12.0.3"},
		},
	})
	defer srv.Close()

	client := mustAnonymousClient(t, srv.URL)
	result, err := client.List(context.Background(), ListOptions{Verbose: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := map[string]string{"apache2": "10.0.0", "nginx": "12.0.4"}
	for _, e := range result.Entries {
		if got := want[e.Name]; e.LatestVersion != got {
			t.Errorf("%s LatestVersion = %q, want %q", e.Name, e.LatestVersion, got)
		}
	}
}

func TestListVerboseLeavesVersionEmptyForUnknownCookbook(t *testing.T) {
	srv := listFixtureServer(t, listFixture{
		Cookbooks: []sumEntry{
			{Name: "ghost", Maintainer: "m"},
		},
		Universe: map[string][]string{},
	})
	defer srv.Close()

	client := mustAnonymousClient(t, srv.URL)
	result, err := client.List(context.Background(), ListOptions{Verbose: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if result.Entries[0].LatestVersion != "" {
		t.Fatalf("LatestVersion = %q, want empty when universe lacks the cookbook", result.Entries[0].LatestVersion)
	}
}

func TestListByOwnerTotalCountsTheirCookbooksNotTheCatalog(t *testing.T) {
	calls := 0
	srv := listFixtureServer(t, listFixture{
		Cookbooks:    []sumEntry{{Name: "apache2"}, {Name: "nginx"}, {Name: "mysql"}},
		CatalogTotal: 4000,
		Calls:        &calls,
	})
	defer srv.Close()

	client := mustAnonymousClient(t, srv.URL)
	result, err := client.List(context.Background(), ListOptions{User: "sous-chefs"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if result.Total != 3 {
		t.Fatalf("Total = %d, want 3 (the owner's cookbooks, not the catalog size)", result.Total)
	}
	if calls != 1 {
		t.Fatalf("list requests = %d, want 1: a short page is the last page", calls)
	}
}

func TestListUnfilteredKeepsServerTotalWhenLimited(t *testing.T) {
	all := make([]sumEntry, 0, 175)
	for i := range 175 {
		all = append(all, sumEntry{Name: fmt.Sprintf("cb-%03d", i)})
	}
	srv := listFixtureServer(t, listFixture{Cookbooks: all})
	defer srv.Close()

	client := mustAnonymousClient(t, srv.URL)
	result, err := client.List(context.Background(), ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result.Entries) != 10 || result.Total != 175 {
		t.Fatalf("len = %d, Total = %d, want 10 and 175", len(result.Entries), result.Total)
	}
}

// sumEntry mirrors the CookbookSummary payload Supermarket returns.
type sumEntry struct {
	Name        string
	Maintainer  string
	Description string
}

type listFixture struct {
	Cookbooks []sumEntry
	Universe  map[string][]string
	// CatalogTotal, when set, is reported as "total" instead of the number
	// of cookbooks served. Production Supermarket does this for a list
	// filtered by owner.
	CatalogTotal int
	// Calls, when set, counts the /api/v1/cookbooks requests served.
	Calls *int
}

func listFixtureServer(t *testing.T, f listFixture) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/cookbooks":
			if f.Calls != nil {
				*f.Calls++
			}
			q := r.URL.Query()
			start, _ := strconv.Atoi(q.Get("start"))
			items, _ := strconv.Atoi(q.Get("items"))
			if items <= 0 {
				items = 100
			}
			end := min(start+items, len(f.Cookbooks))
			start = min(start, len(f.Cookbooks))
			page := f.Cookbooks[start:end]
			items_ := make([]map[string]string, 0, len(page))
			for _, c := range page {
				items_ = append(items_, map[string]string{
					"cookbook_name":        c.Name,
					"cookbook_maintainer":  c.Maintainer,
					"cookbook_description": c.Description,
					"cookbook":             "/api/v1/cookbooks/" + c.Name,
				})
			}
			total := len(f.Cookbooks)
			if f.CatalogTotal > 0 {
				total = f.CatalogTotal
			}
			writeJSON(w, map[string]any{
				"start": start,
				"total": total,
				"items": items_,
			})
		case "/universe":
			out := map[string]map[string]map[string]any{}
			for name, versions := range f.Universe {
				out[name] = map[string]map[string]any{}
				for _, v := range versions {
					out[name][v] = map[string]any{
						"location_type": "opscode",
						"location_path": "https://example.test",
						"dependencies":  map[string]string{},
					}
				}
			}
			writeJSON(w, out)
		default:
			if strings.HasPrefix(r.URL.Path, "/api/v1/cookbooks/") {
				// Some tests may exercise Get; default to 404 unless overridden.
				http.NotFound(w, r)
				return
			}
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	return srv
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	body, _ := json.Marshal(v)
	_, _ = io.Writer(w).Write(body)
}
