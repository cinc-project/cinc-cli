package explore

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	sm "github.com/cinc-project/cinc-supermarket-api"
)

// fakeClient is an in-memory apiClient used by model tests. It also
// records every call so tests can assert on what the model asked for.
type fakeClient struct {
	mu sync.Mutex

	listFn   func(opts sm.ListOptions) (sm.Page[sm.CookbookSummary], error)
	searchFn func(opts sm.SearchOptions) (sm.Page[sm.CookbookSummary], error)
	getFn    func(name string) (*sm.Cookbook, error)

	listCalls   []sm.ListOptions
	searchCalls []sm.SearchOptions
	getCalls    []string
}

func (f *fakeClient) List(ctx context.Context, opts sm.ListOptions) (sm.Page[sm.CookbookSummary], error) {
	f.mu.Lock()
	f.listCalls = append(f.listCalls, opts)
	fn := f.listFn
	f.mu.Unlock()
	if fn == nil {
		return sm.Page[sm.CookbookSummary]{}, nil
	}
	return fn(opts)
}

func (f *fakeClient) Search(ctx context.Context, opts sm.SearchOptions) (sm.Page[sm.CookbookSummary], error) {
	f.mu.Lock()
	f.searchCalls = append(f.searchCalls, opts)
	fn := f.searchFn
	f.mu.Unlock()
	if fn == nil {
		return sm.Page[sm.CookbookSummary]{}, nil
	}
	return fn(opts)
}

func (f *fakeClient) Get(ctx context.Context, name string) (*sm.Cookbook, error) {
	f.mu.Lock()
	f.getCalls = append(f.getCalls, name)
	fn := f.getFn
	f.mu.Unlock()
	if fn == nil {
		return &sm.Cookbook{Name: name}, nil
	}
	return fn(name)
}

// runCmd executes a tea.Cmd synchronously and returns the message it
// produced. tea.Cmd is just `func() tea.Msg`; running it directly skips
// the bubbletea program loop, which is exactly what unit tests want.
func runCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a tea.Cmd, got nil")
	}
	return cmd()
}

// drainBatch runs every command inside a tea.Batch produced by `cmd`.
// If `cmd` isn't a batch, it just runs it. Returns every message
// produced, in order, for assertions.
func drainBatch(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var msgs []tea.Msg
		for _, c := range batch {
			if c == nil {
				continue
			}
			msgs = append(msgs, c())
		}
		return msgs
	}
	return []tea.Msg{msg}
}

func newTestModel(t *testing.T, c apiClient) model {
	t.Helper()
	return initialModel(context.Background(), c, "https://supermarket.example.test", func(string) error { return nil }, nil)
}

func cookbookSummary(name, maintainer string) sm.CookbookSummary {
	return sm.CookbookSummary{Name: name, Maintainer: maintainer}
}

func TestApplyCookbooksLoadedPopulatesItems(t *testing.T) {
	m := newTestModel(t, &fakeClient{})
	msg := cookbooksLoadedMsg{
		mode: modeList, sort: sortMostDownloaded,
		page: sm.Page[sm.CookbookSummary]{
			Start: 0, Total: 2,
			Items: []sm.CookbookSummary{cookbookSummary("a", "x"), cookbookSummary("b", "y")},
		},
	}
	next, _ := applyCookbooksLoaded(m, msg)
	got := next.(model).browse
	if len(got.items) != 2 || got.items[0].Name != "a" {
		t.Fatalf("items = %+v", got.items)
	}
	if got.total != 2 {
		t.Errorf("total = %d, want 2", got.total)
	}
	if got.loading {
		t.Error("loading should be false after load")
	}
}

func TestApplyCookbooksLoadedDropsStaleSortResponses(t *testing.T) {
	m := newTestModel(t, &fakeClient{})
	m.browse.sort = sortRecentlyUpdated // user moved on
	msg := cookbooksLoadedMsg{
		mode: modeList, sort: sortMostDownloaded, // stale
		page: sm.Page[sm.CookbookSummary]{Items: []sm.CookbookSummary{cookbookSummary("a", "x")}},
	}
	next, _ := applyCookbooksLoaded(m, msg)
	if len(next.(model).browse.items) != 0 {
		t.Fatal("stale sort response should have been dropped")
	}
}

func TestApplyCookbooksLoadedDropsStaleSearchQuery(t *testing.T) {
	m := newTestModel(t, &fakeClient{})
	m.browse.mode = modeSearch
	m.browse.query = "nginx"
	msg := cookbooksLoadedMsg{
		mode: modeSearch, query: "apache", // stale
		page: sm.Page[sm.CookbookSummary]{Items: []sm.CookbookSummary{cookbookSummary("apache", "x")}},
	}
	next, _ := applyCookbooksLoaded(m, msg)
	if len(next.(model).browse.items) != 0 {
		t.Fatal("stale search response should have been dropped")
	}
}

func TestApplyCookbooksLoadedAppendsMoreItems(t *testing.T) {
	m := newTestModel(t, &fakeClient{})
	m.browse.items = []sm.CookbookSummary{cookbookSummary("a", "x")}
	m.browse.cursor = 0
	msg := cookbooksLoadedMsg{
		mode: modeList, sort: sortMostDownloaded, append: true,
		page: sm.Page[sm.CookbookSummary]{
			Start: 1, Total: 2,
			Items: []sm.CookbookSummary{cookbookSummary("b", "y")},
		},
	}
	next, _ := applyCookbooksLoaded(m, msg)
	if items := next.(model).browse.items; len(items) != 2 || items[1].Name != "b" {
		t.Fatalf("items = %+v", items)
	}
}

func TestApplyCookbooksLoadedSurfacesError(t *testing.T) {
	m := newTestModel(t, &fakeClient{})
	m.browse.loading = true
	msg := cookbooksLoadedMsg{mode: modeList, sort: sortMostDownloaded, err: errors.New("network down")}
	next, _ := applyCookbooksLoaded(m, msg)
	got := next.(model).browse
	if got.lastErr != "network down" {
		t.Errorf("lastErr = %q, want network down", got.lastErr)
	}
	if got.loading {
		t.Error("loading should be cleared on error")
	}
}

func TestMoveCursorClampsAtBounds(t *testing.T) {
	m := newTestModel(t, &fakeClient{})
	m.browse.items = []sm.CookbookSummary{cookbookSummary("a", ""), cookbookSummary("b", "")}
	m.browse.cursor = 0

	up, _ := moveCursor(m, -5)
	if up.(model).browse.cursor != 0 {
		t.Errorf("cursor under floor = %d, want 0", up.(model).browse.cursor)
	}

	down, _ := moveCursor(m, 99)
	if down.(model).browse.cursor != 1 {
		t.Errorf("cursor over ceiling = %d, want 1", down.(model).browse.cursor)
	}
}

func TestMoveCursorTriggersNextPageNearBottom(t *testing.T) {
	c := &fakeClient{}
	m := newTestModel(t, c)
	m.browse.items = make([]sm.CookbookSummary, 12)
	for i := range m.browse.items {
		m.browse.items[i] = cookbookSummary("cb", "")
	}
	m.browse.hasMore = true
	m.browse.cursor = 1
	m.browse.start = 0

	next, cmd := moveCursor(m, 1) // now at 2; that's len-10, the prefetch trigger
	if cmd == nil {
		t.Fatal("expected prefetch command")
	}
	if !next.(model).browse.loadingMore {
		t.Fatal("loadingMore should be true")
	}
	drainBatch(t, cmd) // runs each cmd inside the batched preview+prefetch
	if len(c.listCalls) != 1 {
		t.Fatalf("listCalls = %d, want 1", len(c.listCalls))
	}
	if c.listCalls[0].Start != 12 {
		t.Errorf("prefetch start = %d, want 12", c.listCalls[0].Start)
	}
}

func TestSetSortTriggersReload(t *testing.T) {
	c := &fakeClient{}
	m := newTestModel(t, c)
	m.browse.sort = sortMostDownloaded

	next, cmd := setSort(m, sortRecentlyUpdated)
	got := next.(model).browse
	if got.sort != sortRecentlyUpdated {
		t.Errorf("sort = %v, want sortRecentlyUpdated", got.sort)
	}
	if len(got.items) != 0 || got.cursor != 0 {
		t.Error("items and cursor should reset")
	}
	if !got.loading {
		t.Error("loading should be true after sort change")
	}
	_ = cmd()
	if len(c.listCalls) != 1 || c.listCalls[0].Order != "recently_updated" {
		t.Fatalf("listCalls = %+v", c.listCalls)
	}
}

func TestSetSortIgnoredInSearchMode(t *testing.T) {
	c := &fakeClient{}
	m := newTestModel(t, c)
	m.browse.mode = modeSearch
	m.browse.sort = sortMostDownloaded
	m.browse.query = "nginx"

	next, cmd := setSort(m, sortAlphabetical)
	if next.(model).browse.sort == sortAlphabetical {
		t.Error("sort change should be ignored while searching")
	}
	if cmd != nil {
		t.Error("no command should fire when sort is ignored")
	}
	if len(c.listCalls) != 0 {
		t.Error("no API call should happen")
	}
}

func TestClearSearchReturnsToListMode(t *testing.T) {
	c := &fakeClient{}
	m := newTestModel(t, c)
	m.browse.mode = modeSearch
	m.browse.query = "nginx"
	m.browse.searchInput.SetValue("nginx")
	m.browse.items = []sm.CookbookSummary{cookbookSummary("nginx", "x")}

	next, cmd := clearSearch(m)
	got := next.(model).browse
	if got.mode != modeList {
		t.Errorf("mode = %v, want modeList", got.mode)
	}
	if got.query != "" || got.searchInput.Value() != "" {
		t.Error("query and input should be cleared")
	}
	_ = cmd()
	if len(c.listCalls) != 1 {
		t.Fatalf("listCalls = %d, want 1 reload", len(c.listCalls))
	}
}

func TestSearchKeyTypingFiresDebouncedSearch(t *testing.T) {
	c := &fakeClient{}
	m := newTestModel(t, c)
	m.browse.searchInput.Focus()
	m.browse.searchFocused = true

	keyN := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}
	next, cmd := handleSearchKey(m, keyN)
	got := next.(model).browse
	if got.query != "n" || got.mode != modeSearch {
		t.Fatalf("after type 'n', query=%q mode=%v", got.query, got.mode)
	}
	if !got.loading {
		t.Error("loading should be true while debounce is pending")
	}
	// The batched cmd contains a debounceTickMsg producer; the search
	// call itself fires once the tick is processed by runSearchOrList.
	// We exercise that path directly:
	tickModel, searchCmd := runSearchOrList(next.(model))
	_ = tickModel
	_ = runCmd(t, searchCmd) // invokes c.Search
	if len(c.searchCalls) != 1 || c.searchCalls[0].Q != "n" {
		t.Fatalf("searchCalls = %+v", c.searchCalls)
	}
	_ = cmd
}

func TestEscFromSearchModeKeepsQuery(t *testing.T) {
	m := newTestModel(t, &fakeClient{})
	m.browse.searchInput.SetValue("nginx")
	m.browse.searchInput.Focus()
	m.browse.searchFocused = true
	m.browse.query = "nginx"

	next, _ := handleSearchKey(m, tea.KeyMsg{Type: tea.KeyEsc})
	got := next.(model).browse
	if got.searchFocused {
		t.Error("esc should blur the search input")
	}
	if got.query != "nginx" {
		t.Errorf("query = %q, want nginx (esc keeps query in search mode)", got.query)
	}
}

func TestOpenDetailUsesCacheWhenAvailable(t *testing.T) {
	c := &fakeClient{}
	m := newTestModel(t, c)
	m.browse.items = []sm.CookbookSummary{cookbookSummary("nginx", "x")}
	cached := &sm.Cookbook{Name: "nginx", Maintainer: "Sous Chefs"}
	m.browse.previewCache["nginx"] = cached

	next, cmd := openDetail(m)
	got := next.(model)
	if got.view != viewDetail {
		t.Error("view should be viewDetail")
	}
	if got.detail.cookbook == nil || got.detail.cookbook.Maintainer != "Sous Chefs" {
		t.Errorf("cookbook = %+v, want cached", got.detail.cookbook)
	}
	if cmd != nil {
		t.Error("no command needed when cache is warm")
	}
	if len(c.getCalls) != 0 {
		t.Error("no API call expected for cached entry")
	}
}

func TestOpenDetailFetchesWhenCacheMiss(t *testing.T) {
	c := &fakeClient{
		getFn: func(name string) (*sm.Cookbook, error) {
			return &sm.Cookbook{Name: name, Maintainer: "Sous Chefs"}, nil
		},
	}
	m := newTestModel(t, c)
	m.browse.items = []sm.CookbookSummary{cookbookSummary("nginx", "x")}

	next, cmd := openDetail(m)
	if !next.(model).detail.loading {
		t.Error("detail.loading should be true while fetching")
	}
	msg := runCmd(t, cmd).(cookbookDetailMsg)
	if msg.target != targetDetail || msg.name != "nginx" || msg.cookbook.Maintainer != "Sous Chefs" {
		t.Fatalf("detail msg = %+v", msg)
	}
}

func TestApplyCookbookDetailRendersViewport(t *testing.T) {
	m := newTestModel(t, &fakeClient{})
	// Initialize the viewport the same way WindowSizeMsg would.
	nextM, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = nextM.(model)
	m.detail.name = "nginx"
	m.detail.loading = true

	cb := &sm.Cookbook{
		Name: "nginx", Maintainer: "Sous Chefs", Category: "Web Servers",
		LatestVersion: "12.0.4", Description: "Installs and configures NGINX.",
		UpdatedAt: time.Now(),
		Versions:  []string{"12.0.4", "12.0.3"},
	}
	next, _ := applyCookbookDetail(m, cookbookDetailMsg{name: "nginx", cookbook: cb, target: targetDetail})
	got := next.(model)
	if got.detail.loading {
		t.Error("detail.loading should be cleared")
	}
	if got.detail.cookbook != cb {
		t.Error("cookbook not stored")
	}
	if !strings.Contains(got.detail.viewport.View(), "Sous Chefs") {
		t.Errorf("viewport missing maintainer:\n%s", got.detail.viewport.View())
	}
}

func TestCookbookURLPrefersExternalURL(t *testing.T) {
	m := newTestModel(t, &fakeClient{})
	m.browse.previewCache["nginx"] = &sm.Cookbook{Name: "nginx", ExternalURL: "https://github.com/x/nginx"}
	if got := cookbookURL(m, "nginx"); got != "https://github.com/x/nginx" {
		t.Errorf("url = %q, want external", got)
	}
}

func TestCookbookURLFallsBackToSiteSlashCookbooks(t *testing.T) {
	m := newTestModel(t, &fakeClient{})
	if got := cookbookURL(m, "nginx"); got != "https://supermarket.example.test/cookbooks/nginx" {
		t.Errorf("url = %q", got)
	}
}

func TestOpenInBrowserSurfacesOpenErrors(t *testing.T) {
	m := newTestModel(t, &fakeClient{})
	m.browse.items = []sm.CookbookSummary{cookbookSummary("nginx", "x")}
	m.browse.cursor = 0
	m.browse.previewName = "nginx"
	m.openInBrowser = func(string) error { return errors.New("no opener") }

	next, _ := openInBrowser(m, m.browse.previewName)
	if got := next.(model).openErr; got != "no opener" {
		t.Errorf("openErr = %q, want \"no opener\"", got)
	}
}

func TestPreviewTickCacheHitSkipsRequest(t *testing.T) {
	c := &fakeClient{}
	m := newTestModel(t, c)
	cached := &sm.Cookbook{Name: "nginx", Maintainer: "Sous Chefs"}
	m.browse.previewCache["nginx"] = cached
	m.browse.previewID = 7

	next, cmd := updateBrowse(m, previewTickMsg{reqID: 7, name: "nginx"})
	if cmd != nil {
		t.Error("cached preview shouldn't fire a command")
	}
	got := next.(model).browse
	if got.preview != cached || got.previewName != "nginx" {
		t.Error("cached preview wasn't installed")
	}
}

func TestDetailEscReturnsToBrowse(t *testing.T) {
	m := newTestModel(t, &fakeClient{})
	m.view = viewDetail
	m.detail.ready = true

	next, _ := updateDetail(m, tea.KeyMsg{Type: tea.KeyEsc})
	if next.(model).view != viewBrowse {
		t.Errorf("view = %v, want viewBrowse", next.(model).view)
	}
}

func TestPreviewCacheUpdatedFromDetailFetch(t *testing.T) {
	m := newTestModel(t, &fakeClient{})
	m.detail.name = "nginx"
	cb := &sm.Cookbook{Name: "nginx", Maintainer: "Sous Chefs"}
	next, _ := applyCookbookDetail(m, cookbookDetailMsg{name: "nginx", cookbook: cb, target: targetDetail})
	if next.(model).browse.previewCache["nginx"] != cb {
		t.Fatal("detail fetch should populate preview cache for fast back-nav")
	}
}
