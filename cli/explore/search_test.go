package explore

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// searchMux serves the node/role indexes plus their /search endpoints so a
// search can narrow a loaded list.
func searchMux(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	jsonHandler(mux, "/organizations/acme/nodes", `{"web01":"u","web02":"u","db01":"u"}`)
	jsonHandler(mux, "/organizations/acme/roles", `{"web":"u","db":"u"}`)
	jsonHandler(mux, "/organizations/acme/search/node",
		`{"total":2,"start":0,"rows":[{"name":"web01"},{"name":"web02"}]}`)
	jsonHandler(mux, "/organizations/acme/search/role",
		`{"total":1,"start":0,"rows":[{"name":"web"}]}`)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestSearchIndexCapability(t *testing.T) {
	want := map[string]string{
		"Nodes":        "node",
		"Roles":        "role",
		"Environments": "environment",
		"Clients":      "client",
	}
	for _, k := range registry() {
		idx, ok := searchIndexOf(k)
		if expect, searchable := want[k.Title()]; searchable {
			if !ok || idx != expect {
				t.Errorf("%s: searchIndexOf = (%q,%v), want (%q,true)", k.Title(), idx, ok, expect)
			}
		} else if ok {
			t.Errorf("%s: unexpectedly searchable (index %q)", k.Title(), idx)
		}
	}
}

func TestDataBagItemsAreSearchableByBagName(t *testing.T) {
	k := dataBagItemsKind{bag: "creds"}
	if idx, ok := searchIndexOf(k); !ok || idx != "creds" {
		t.Errorf("data bag items searchIndexOf = (%q,%v), want (\"creds\",true)", idx, ok)
	}
}

func TestSearchNarrowsListToServerMatches(t *testing.T) {
	m, _ := openNodes(t, searchMux(t))

	// Open the search modal; it defaults to the current list's index.
	m, _ = pressRune(t, m, 's')
	if m.screen != screenSearch {
		t.Fatalf("screen = %v, want search", m.screen)
	}
	if idx, _ := searchIndexOf(m.searchKind); idx != "node" {
		t.Fatalf("default search index = %q, want node", idx)
	}

	// Type a query and run it.
	m, _ = step(t, m, keyRunes("role:web"))
	m, cmd := pressKey(t, m, 13) // enter
	msg := drain(cmd)
	sl, ok := msg.(searchLoadedMsg)
	if !ok {
		t.Fatalf("enter produced %T, want searchLoadedMsg", msg)
	}
	if sl.err != nil {
		t.Fatalf("search errored: %v", sl.err)
	}

	// Applying the result reloads the list; drain that too.
	m, cmd = step(t, m, sl)
	m, _ = step(t, m, drain(cmd))

	if !m.searchActive {
		t.Fatal("searchActive = false after a search")
	}
	if m.searchIndex != "node" || m.searchQuery != "role:web" {
		t.Errorf("banner state = (%q,%q), want (node, role:web)", m.searchIndex, m.searchQuery)
	}
	if got := names(m.filteredRows()); !equal(got, []string{"web01", "web02"}) {
		t.Errorf("rows = %v, want web01,web02", got)
	}
}

func TestEscClearsActiveSearch(t *testing.T) {
	m, _ := openNodes(t, searchMux(t))
	m, _ = pressRune(t, m, 's')
	m, _ = step(t, m, keyRunes("role:web"))
	m, cmd := pressKey(t, m, 13)
	m, cmd = step(t, m, drain(cmd))
	m, _ = step(t, m, drain(cmd))
	if got := names(m.filteredRows()); len(got) != 2 {
		t.Fatalf("precondition: rows = %v, want 2 matches", got)
	}

	m, _ = pressKey(t, m, 27) // esc clears the search, stays on the list
	if m.searchActive {
		t.Error("searchActive still true after esc")
	}
	if m.screen != screenList {
		t.Errorf("screen = %v, want list", m.screen)
	}
	if got := names(m.filteredRows()); !equal(got, []string{"db01", "web01", "web02"}) {
		t.Errorf("rows = %v, want the full list back", got)
	}
}

func TestSearchIndexPickerSwitchesIndex(t *testing.T) {
	m, _ := openNodes(t, searchMux(t))
	m, _ = pressRune(t, m, 's')

	// Tab opens the index picker, listing the searchable nouns.
	m, _ = pressKey(t, m, tea.KeyTab)
	if m.screen != screenSearchIndex {
		t.Fatalf("screen = %v, want search-index picker", m.screen)
	}
	if got := kindTitles(m.searchKinds); !equal(got, []string{"Nodes", "Roles", "Environments", "Clients"}) {
		t.Fatalf("picker choices = %v", got)
	}

	// Move to Roles and select it.
	m, _ = pressRune(t, m, 'j') // down to Roles
	m, _ = pressKey(t, m, 13)   // enter
	if m.screen != screenSearch {
		t.Fatalf("screen = %v, want search modal", m.screen)
	}
	if idx, _ := searchIndexOf(m.searchKind); idx != "role" {
		t.Fatalf("search index after picking Roles = %q, want role", idx)
	}

	// Running the search now queries the role index and switches the view.
	m, _ = step(t, m, keyRunes("name:web"))
	m, cmd := pressKey(t, m, 13)
	m, cmd = step(t, m, drain(cmd))
	m, _ = step(t, m, drain(cmd))
	if m.cur.Title() != "Roles" {
		t.Errorf("current kind = %q, want Roles", m.cur.Title())
	}
	if got := names(m.filteredRows()); !equal(got, []string{"web"}) {
		t.Errorf("rows = %v, want web", got)
	}
}

func TestSearchFromKindMenuScopesToHighlightedKind(t *testing.T) {
	m := modelOnKinds(t, testClient(t, searchMux(t)))
	// Nodes is highlighted by default on the kind menu.
	if m.screen != screenKinds {
		t.Fatalf("precondition: screen = %v, want kinds", m.screen)
	}

	// Pressing s on the menu opens the search modal scoped to the
	// highlighted kind, without first opening its list.
	m, _ = pressRune(t, m, 's')
	if m.screen != screenSearch {
		t.Fatalf("screen = %v, want search", m.screen)
	}
	if idx, _ := searchIndexOf(m.searchKind); idx != "node" {
		t.Fatalf("default search index = %q, want node", idx)
	}

	// Running a query lands on the node list with the matched rows.
	m, _ = step(t, m, keyRunes("role:web"))
	m, cmd := pressKey(t, m, 13) // enter
	m, cmd = step(t, m, drain(cmd))
	m, _ = step(t, m, drain(cmd))
	if m.cur.Title() != "Nodes" {
		t.Errorf("current kind = %q, want Nodes", m.cur.Title())
	}
	if got := names(m.filteredRows()); !equal(got, []string{"web01", "web02"}) {
		t.Errorf("rows = %v, want web01,web02", got)
	}
}

func TestSearchCancelFromKindMenuReturnsToMenu(t *testing.T) {
	m := modelOnKinds(t, testClient(t, searchMux(t)))
	m, _ = pressRune(t, m, 's')
	if m.screen != screenSearch {
		t.Fatalf("screen = %v, want search", m.screen)
	}
	m, _ = pressKey(t, m, 27) // esc cancels back to the menu it came from
	if m.screen != screenKinds {
		t.Errorf("screen = %v, want kinds after cancel", m.screen)
	}
}

func TestSearchKeyInertOnUnsearchableKindMenu(t *testing.T) {
	mux := http.NewServeMux()
	jsonHandler(mux, "/organizations/acme/users", `{"alice":"u"}`)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	m := modelOnKinds(t, testClient(t, srv))
	m.kindCursor = 5 // Users, an unsearchable kind
	m, _ = pressRune(t, m, 's')
	if m.screen != screenKinds {
		t.Errorf("screen = %v, want kinds (search inert on Users)", m.screen)
	}
}

func TestSearchModalLabelsTypeWithPluralTitle(t *testing.T) {
	m := modelOnKinds(t, testClient(t, searchMux(t)))
	m, _ = pressRune(t, m, 's') // search the highlighted Nodes kind

	view := m.View()
	if !strings.Contains(view, "Search Nodes") {
		t.Errorf("search modal header missing plural type; view = %q", view)
	}
	if strings.Contains(view, "Search node\n") || strings.Contains(view, "Search node ") {
		t.Errorf("search modal still shows singular index name; view = %q", view)
	}
}

func TestSearchKeyInertOnUnsearchableKind(t *testing.T) {
	mux := http.NewServeMux()
	jsonHandler(mux, "/organizations/acme/users", `{"alice":"u"}`)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	m := modelOnKinds(t, testClient(t, srv))
	m.kindCursor = 5 // Users
	m, cmd := pressKey(t, m, 13)
	m, _ = step(t, m, drain(cmd))

	m, _ = pressRune(t, m, 's')
	if m.screen != screenList {
		t.Errorf("screen = %v, want list (search inert on Users)", m.screen)
	}
}

func kindTitles(ks []Kind) []string {
	out := make([]string, len(ks))
	for i, k := range ks {
		out[i] = k.Title()
	}
	return out
}
