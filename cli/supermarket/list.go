package supermarket

import (
	"context"
	"fmt"

	sm "github.com/cinc-project/cinc-supermarket-api"
)

// ListOptions controls a cookbook list operation.
type ListOptions struct {
	// Order, when set, is one of: recently_updated, recently_added,
	// most_downloaded, most_followed.
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

// ListEntry is one row in a list result.
type ListEntry struct {
	Name          string `json:"name"`
	Maintainer    string `json:"maintainer"`
	Description   string `json:"description,omitempty"`
	LatestVersion string `json:"latest_version,omitempty"`
}

// ListResult is the full set of entries the server (or limit) yielded.
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
	var (
		entries []ListEntry
		total   int
		start   int
	)
	for {
		page, _, err := c.api.Cookbooks.List(ctx, sm.ListOptions{
			Start: start,
			Items: listPageSize,
			Order: opts.Order,
			User:  opts.User,
		})
		if err != nil {
			return nil, 0, fmt.Errorf("supermarket: list cookbooks: %w", err)
		}
		total = page.Total
		for _, item := range page.Items {
			entries = append(entries, ListEntry{
				Name:        item.Name,
				Maintainer:  item.Maintainer,
				Description: item.Description,
			})
			if opts.Limit > 0 && len(entries) >= opts.Limit {
				return entries, total, nil
			}
		}
		if !page.HasMore() {
			return entries, total, nil
		}
		start = page.NextStart()
	}
}

func (c *Client) attachLatestVersions(ctx context.Context, entries []ListEntry) error {
	if len(entries) == 0 {
		return nil
	}
	universe, _, err := c.api.Universe.Get(ctx)
	if err != nil {
		return fmt.Errorf("supermarket: fetch universe: %w", err)
	}
	for i := range entries {
		versions := universe[entries[i].Name]
		if len(versions) == 0 {
			continue
		}
		keys := make([]string, 0, len(versions))
		for v := range versions {
			keys = append(keys, v)
		}
		entries[i].LatestVersion = sm.LatestVersion(keys)
	}
	return nil
}
