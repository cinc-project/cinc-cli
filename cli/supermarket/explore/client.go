// Package explore implements the `cinc supermarket explore` TUI: a
// bubbletea-driven cookbook browser over the anonymous read endpoints of
// Chef Supermarket.
//
// Testing strategy: cinc-zero, the acceptance harness for the rest of
// the CLI, simulates a Chef Infra Server — it does not serve the
// Supermarket API. There is therefore no acceptance test for this
// command. Coverage lives entirely in unit tests:
//
//   - realClient is exercised against httptest servers that mimic
//     /api/v1/cookbooks and /api/v1/search responses.
//   - The model state machine is driven directly against a fakeClient
//     so every cookbooksLoadedMsg / cookbookDetailMsg / debounce path
//     can be tested without standing up a bubbletea program.
//   - The cobra wiring is verified by command-tree tests in
//     apps/cinc/cmd, including the non-TTY refusal path.
package explore

import (
	"context"
	"errors"

	sm "github.com/cinc-project/cinc-supermarket-api"
)

// sortOrder identifies one of the three sort modes the TUI exposes.
type sortOrder int

const (
	sortMostDownloaded sortOrder = iota
	sortRecentlyUpdated
	sortAlphabetical
)

// orderParam maps a sortOrder to the Supermarket API order parameter.
// Alphabetical maps to the empty string because the public Supermarket
// API serves /api/v1/cookbooks alphabetically by name when no `order`
// query parameter is set.
func (s sortOrder) orderParam() string {
	switch s {
	case sortMostDownloaded:
		return "most_downloaded"
	case sortRecentlyUpdated:
		return "recently_updated"
	default:
		return ""
	}
}

// listMode identifies which Supermarket endpoint a page came from. The
// model carries this on each request so out-of-order responses (a search
// that finishes after the user cleared the box, for example) can be
// dropped instead of overwriting the new state.
type listMode int

const (
	modeList listMode = iota
	modeSearch
)

// pageSize is the number of cookbooks we ask Supermarket for at a time.
// 50 is the supermarket UI's own page size and keeps responses snappy.
const pageSize = 50

// apiClient is the small surface area of *sm.Client the explore TUI
// uses. Keeping it as an interface lets tests substitute an in-memory
// fake instead of standing up an httptest server for every model test.
type apiClient interface {
	List(ctx context.Context, opts sm.ListOptions) (sm.Page[sm.CookbookSummary], error)
	Search(ctx context.Context, opts sm.SearchOptions) (sm.Page[sm.CookbookSummary], error)
	Get(ctx context.Context, name string) (*sm.Cookbook, error)
}

// realClient adapts *sm.Client to apiClient. It is the only place in
// the explore package that talks to the supermarket library.
type realClient struct{ c *sm.Client }

func newRealClient(site string) (*realClient, error) {
	c, err := sm.NewClient(sm.Config{BaseURL: site}, sm.WithUserAgent("cinc-cli"))
	if err != nil {
		return nil, err
	}
	return &realClient{c: c}, nil
}

func (r *realClient) List(ctx context.Context, opts sm.ListOptions) (sm.Page[sm.CookbookSummary], error) {
	page, _, err := r.c.Cookbooks.List(ctx, opts)
	return page, err
}

func (r *realClient) Search(ctx context.Context, opts sm.SearchOptions) (sm.Page[sm.CookbookSummary], error) {
	page, _, err := r.c.Search.Cookbooks(ctx, opts)
	return page, err
}

func (r *realClient) Get(ctx context.Context, name string) (*sm.Cookbook, error) {
	cb, _, err := r.c.Cookbooks.Get(ctx, name)
	if err != nil {
		if errors.Is(err, sm.ErrNotFound) {
			return nil, errNotFound
		}
		return nil, err
	}
	return cb, nil
}

// errNotFound is the sentinel the detail and preview flows compare
// against so the UI can show a friendly "no longer on Supermarket"
// message instead of a raw 404.
var errNotFound = errors.New("explore: cookbook not found")
