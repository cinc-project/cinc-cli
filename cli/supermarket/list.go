package supermarket

import (
	"context"
	"fmt"
	"slices"
	"strings"

	sm "github.com/cinc-project/cinc-supermarket-api"
)

// ListOptions controls a cookbook list operation.
type ListOptions struct {
	// Order, when set, is one of ListOrders.
	Order string
	// User, when set, restricts results to cookbooks owned by that
	// Supermarket username.
	User string
	// Limit caps the total number of entries returned. Zero means
	// "every cookbook on the server".
	Limit int
	// Verbose enriches each entry with its latest version, fetched
	// from /universe in a single request.
	Verbose bool
}

// ListOrders are the sort orders Supermarket's cookbook list accepts.
var ListOrders = []string{
	sm.OrderByName,
	sm.OrderRecentlyUpdated,
	sm.OrderRecentlyAdded,
	sm.OrderMostDownloaded,
	sm.OrderMostFollowed,
}

// ValidateListOrder accepts "" (the server's default order) or one of
// ListOrders.
func ValidateListOrder(order string) error {
	if order == "" || slices.Contains(ListOrders, order) {
		return nil
	}
	return fmt.Errorf("we don't know the sort order %q. Use one of: %s", order, strings.Join(ListOrders, ", "))
}

// ListEntry is one row in a list result.
type ListEntry struct {
	Name          string `json:"name"`
	Maintainer    string `json:"maintainer"`
	Description   string `json:"description,omitempty"`
	LatestVersion string `json:"latest_version,omitempty"`
}

// ListResult is the full set of entries the server (or limit) yielded.
// Total is the server's count for an unfiltered list. Filtered by User,
// it is the number of entries found, since Supermarket reports the whole
// catalog there; it is then a lower bound when Limit cut the walk short.
type ListResult struct {
	Entries []ListEntry `json:"entries"`
	Total   int         `json:"total"`
}

// listPageSize is the Supermarket page size we request. Anything up to
// 100 is allowed; 100 keeps the round-trip count down.
const listPageSize = 100

// List walks every page of /api/v1/cookbooks (or stops at Limit) and,
// when Verbose is set, enriches each row with the latest version
// number from /universe.
func (c *Client) List(ctx context.Context, opts ListOptions) (ListResult, error) {
	entries, total, err := c.paginateCookbooks(ctx, opts)
	if err != nil {
		return ListResult{}, err
	}
	if opts.Verbose {
		if err := c.attachLatestVersions(ctx, entries); err != nil {
			return ListResult{}, err
		}
	}
	return ListResult{Entries: entries, Total: total}, nil
}

func (c *Client) paginateCookbooks(ctx context.Context, opts ListOptions) ([]ListEntry, int, error) {
	entries, total, err := walkPages(func(start int) (sm.Page[sm.CookbookSummary], error) {
		page, _, err := c.api.Cookbooks.List(ctx, sm.ListOptions{
			Start: start,
			Items: listPageSize,
			Order: opts.Order,
			User:  opts.User,
		})
		return page, err
	}, opts.Limit, opts.User == "")
	if err != nil {
		return nil, 0, fmt.Errorf("supermarket: list cookbooks: %w", err)
	}
	return entries, total, nil
}

// walkPages fetches listPageSize pages until a short or empty page (or
// limit) ends the walk. Supermarket's "total" is only the true count for
// an unfiltered cookbook list; search and owner-filtered lists report the
// size of the whole catalog. So unless trustTotal is set, the total is the
// number of entries the walk found, which is exact when the walk reached
// the end and a lower bound when limit stopped it.
func walkPages(fetch func(start int) (sm.Page[sm.CookbookSummary], error), limit int, trustTotal bool) ([]ListEntry, int, error) {
	var entries []ListEntry
	total := func(page sm.Page[sm.CookbookSummary]) int {
		if trustTotal {
			return page.Total
		}
		return len(entries)
	}
	start := 0
	for {
		page, err := fetch(start)
		if err != nil {
			return nil, 0, err
		}
		for _, item := range page.Items {
			entries = append(entries, ListEntry{
				Name:        item.Name,
				Maintainer:  item.Maintainer,
				Description: item.Description,
			})
			if limit > 0 && len(entries) >= limit {
				return entries, total(page), nil
			}
		}
		if len(page.Items) < listPageSize || !page.HasMore() {
			return entries, total(page), nil
		}
		start = page.NextStart()
	}
}

// attachLatestVersions streams /universe, which runs to tens of MB on the
// public Supermarket, keeping only the versions of the listed cookbooks.
func (c *Client) attachLatestVersions(ctx context.Context, entries []ListEntry) error {
	if len(entries) == 0 {
		return nil
	}
	versions := make(map[string][]string, len(entries))
	for _, e := range entries {
		versions[e.Name] = nil
	}
	for entry, err := range c.api.Universe.Entries(ctx) {
		if err != nil {
			return fmt.Errorf("supermarket: fetch universe: %w", err)
		}
		if known, ok := versions[entry.Cookbook]; ok {
			versions[entry.Cookbook] = append(known, entry.Version)
		}
	}
	for i := range entries {
		if v := versions[entries[i].Name]; len(v) > 0 {
			entries[i].LatestVersion = sm.LatestVersion(v)
		}
	}
	return nil
}
