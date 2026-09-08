package supermarket

import (
	"context"
	"errors"
	"fmt"

	sm "github.com/cinc-project/cinc-supermarket-api"
)

// ShowOptions controls a show operation. When Version is empty the
// result describes the cookbook as a whole; otherwise it describes
// that specific version.
type ShowOptions struct {
	Cookbook string
	Version  string
}

// Cookbook and CookbookVersion are re-exported from the SDK so the
// cmd layer can render Show results without importing
// github.com/cinc-project/cinc-supermarket-api directly.
type (
	Cookbook        = sm.Cookbook
	CookbookVersion = sm.CookbookVersion
)

// ShowResult carries exactly one of Cookbook or Version, depending on
// whether ShowOptions.Version was set. JSON marshalling produces just
// the populated field.
type ShowResult struct {
	Cookbook *Cookbook        `json:"cookbook,omitempty"`
	Version  *CookbookVersion `json:"version,omitempty"`
}

// Show fetches a single cookbook record, or a single version record
// when opts.Version is set.
func (c *Client) Show(ctx context.Context, opts ShowOptions) (ShowResult, error) {
	if opts.Cookbook == "" {
		return ShowResult{}, errors.New("supermarket: cookbook name is required")
	}
	if opts.Version != "" {
		v, _, err := c.api.Cookbooks.GetVersion(ctx, opts.Cookbook, opts.Version)
		if err != nil {
			return ShowResult{}, fmt.Errorf("supermarket: show %s %s: %w", opts.Cookbook, opts.Version, err)
		}
		return ShowResult{Version: v}, nil
	}
	cb, _, err := c.api.Cookbooks.Get(ctx, opts.Cookbook)
	if err != nil {
		return ShowResult{}, fmt.Errorf("supermarket: show %s: %w", opts.Cookbook, err)
	}
	return ShowResult{Cookbook: cb}, nil
}
