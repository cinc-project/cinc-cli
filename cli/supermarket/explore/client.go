// Package explore implements the `cinc supermarket explore` TUI: a
// bubbletea-driven cookbook browser over the anonymous read endpoints of
// Chef Supermarket.
//
// Testing strategy: the integration suite runs the rest of the CLI
// against Chef Infra Server implementations, none of which serves the
// Supermarket API. There is therefore no integration case for this
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
	"fmt"

	sm "github.com/cinc-project/cinc-supermarket-api"

	"github.com/cinc-project/cinc-cli/cli/supermarket"
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
		return sm.OrderMostDownloaded
	case sortRecentlyUpdated:
		return sm.OrderRecentlyUpdated
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
type realClient struct {
	c    *sm.Client
	site string
}

func newRealClient(site string) (*realClient, error) {
	if err := supermarket.ValidateSite(site); err != nil {
		return nil, err
	}
	c, err := sm.NewClient(sm.Config{BaseURL: site}, sm.WithUserAgent("cinc-cli"))
	if err != nil {
		return nil, err
	}
	return &realClient{c: c, site: site}, nil
}

func (r *realClient) List(ctx context.Context, opts sm.ListOptions) (sm.Page[sm.CookbookSummary], error) {
	page, _, err := r.c.Cookbooks.List(ctx, opts)
	if err != nil {
		return page, supermarket.RequestError(r.site, "list cookbooks", err)
	}
	return page, nil
}

func (r *realClient) Search(ctx context.Context, opts sm.SearchOptions) (sm.Page[sm.CookbookSummary], error) {
	page, _, err := r.c.Search.Cookbooks(ctx, opts)
	if err != nil {
		return page, supermarket.RequestError(r.site, fmt.Sprintf("search for %q", opts.Q), err)
	}
	return page, nil
}

func (r *realClient) Get(ctx context.Context, name string) (*sm.Cookbook, error) {
	cb, _, err := r.c.Cookbooks.Get(ctx, name)
	if err != nil {
		if errors.Is(err, sm.ErrNotFound) {
			return nil, fmt.Errorf("%q isn't on %s anymore; it may have been removed or renamed (%w)", name, r.site, errNotFound)
		}
		return nil, supermarket.RequestError(r.site, "look up "+name, err)
	}
	return cb, nil
}

// errNotFound is the sentinel Get wraps for a cookbook Supermarket doesn't
// have, so callers can tell "gone" apart from "couldn't ask".
var errNotFound = errors.New("not found")
