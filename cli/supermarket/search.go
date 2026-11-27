package supermarket

import (
	"context"
	"errors"
	"fmt"

	sm "github.com/cinc-project/cinc-supermarket-api"
)

// SearchOptions controls a cookbook search.
type SearchOptions struct {
	// Query is the fuzzy search string sent to /api/v1/search. Required.
	Query string
	// Limit caps the total number of entries returned. Zero means
	// "every cookbook that matches".
	Limit int
	// Verbose enriches each entry with its latest version from
	// /universe in a single follow-up request.
	Verbose bool
}

// SearchResult is the full set of entries the server (or limit) yielded.
// Total is the number of matches found: Supermarket's own search total is
// the size of the whole catalog. When Limit cut the walk short it is a
// lower bound.
type SearchResult struct {
	Entries []ListEntry `json:"entries"`
	Total   int         `json:"total"`
}

// Search walks every page of /api/v1/search?q=… (or stops at Limit)
// and, when Verbose is set, enriches each row with the latest version
// number from /universe.
func (c *Client) Search(ctx context.Context, opts SearchOptions) (SearchResult, error) {
	if opts.Query == "" {
		return SearchResult{}, errors.New("supermarket: search query is required")
	}
	entries, total, err := c.paginateSearch(ctx, opts)
	if err != nil {
		return SearchResult{}, err
	}
	if opts.Verbose {
		if err := c.attachLatestVersions(ctx, entries); err != nil {
			return SearchResult{}, err
		}
	}
	return SearchResult{Entries: entries, Total: total}, nil
}

func (c *Client) paginateSearch(ctx context.Context, opts SearchOptions) ([]ListEntry, int, error) {
	entries, total, err := walkPages(func(start int) (sm.Page[sm.CookbookSummary], error) {
		page, _, err := c.api.Search.Cookbooks(ctx, sm.SearchOptions{
			Q:     opts.Query,
			Start: start,
			Items: listPageSize,
		})
		return page, err
	}, opts.Limit, false)
	if err != nil {
		return nil, 0, fmt.Errorf("supermarket: search %q: %w", opts.Query, err)
	}
	return entries, total, nil
}
