// Package supermarket orchestrates Chef Supermarket cookbook uploads.
//
// It owns the local concerns — locating the cookbook on disk, building
// the upload tarball, and rendering dry-run results — and delegates
// every network call (signed share, category lookup) to
// github.com/cinc-project/cinc-supermarket-api.
package supermarket

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	sm "github.com/cinc-project/cinc-supermarket-api"

	"github.com/cinc-project/cinc-cli/cli/config"
	localcookbook "github.com/cinc-project/cinc-cli/cli/cookbook"
)

// DefaultSite is the public Supermarket instance.
const DefaultSite = sm.DefaultBaseURL

// Client uploads cookbooks to one Chef Supermarket endpoint.
type Client struct {
	base *url.URL
	api  *sm.Client
}

// ShareOptions controls a cookbook share operation.
type ShareOptions struct {
	Cookbook     string
	Category     string
	CookbookPath string
	DryRun       bool
	// SkipChefignore disables filtering the tarball through the
	// cookbook's chefignore file.
	SkipChefignore bool
}

// ShareResult describes the work done by Share.
type ShareResult struct {
	Cookbook    string   `json:"cookbook"`
	Version     string   `json:"version"`
	Category    string   `json:"category"`
	Uploaded    bool     `json:"uploaded"`
	Status      int      `json:"status"`
	Tarball     string   `json:"tarball"`
	TarballSize int      `json:"tarball_size"`
	Files       []string `json:"files,omitempty"`
	// Site is the Supermarket base URL an upload was sent to, and URL is the
	// public page for the uploaded version. Both are empty on a dry run.
	Site string `json:"site,omitempty"`
	URL  string `json:"url,omitempty"`
}

// New builds a Supermarket client using the profile's Supermarket identity.
//
// The signing identity is resolved through Profile.SupermarketIdentity, so a
// profile can override the username and/or key used for uploads via
// supermarket_client_name/supermarket_key while falling back to
// client_name/client_key for whichever override is unset.
func New(profile config.Profile, site string) (*Client, error) {
	if err := profile.ValidateSupermarketIdentity(); err != nil {
		return nil, err
	}
	username, keyPath := profile.SupermarketIdentity()
	if site == "" {
		site = profile.SupermarketSite
	}
	if site == "" {
		site = DefaultSite
	}
	base, err := url.Parse(strings.TrimRight(site, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("supermarket: invalid site URL %q", site)
	}
	key, err := sm.LoadKeyFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("supermarket: load key: %w", err)
	}
	api, err := sm.NewClient(sm.Config{
		BaseURL:  base.String(),
		Username: username,
		Key:      key,
	}, sm.WithUserAgent("cinc-cli"))
	if err != nil {
		return nil, err
	}
	return &Client{base: base, api: api}, nil
}

// Share packages and optionally uploads a cookbook to Supermarket.
func (c *Client) Share(ctx context.Context, opts ShareOptions) (ShareResult, error) {
	category := opts.Category
	if category == "" && !opts.DryRun {
		resolved, err := c.lookupCategory(ctx, opts.Cookbook)
		if err != nil {
			return ShareResult{}, err
		}
		category = resolved
	}
	result, archive, err := packageCookbook(opts, category, opts.DryRun)
	if err != nil {
		return ShareResult{}, err
	}
	if opts.DryRun {
		return result, nil
	}
	status, err := c.upload(ctx, opts.Cookbook, result.Category, archive)
	if err != nil {
		return ShareResult{}, err
	}
	result.Uploaded = true
	result.Status = status
	result.Site = c.base.String()
	result.URL = fmt.Sprintf("%s/cookbooks/%s/versions/%s", c.base.String(), result.Cookbook, result.Version)
	return result, nil
}

// DryRun packages a cookbook for Supermarket without loading credentials or
// contacting the configured Supermarket.
func DryRun(opts ShareOptions) (ShareResult, error) {
	result, _, err := packageCookbook(opts, opts.Category, true)
	return result, err
}

func packageCookbook(opts ShareOptions, category string, includeFiles bool) (ShareResult, localcookbook.Archive, error) {
	dir, err := localcookbook.Locate(opts.Cookbook, opts.CookbookPath)
	if err != nil {
		return ShareResult{}, localcookbook.Archive{}, err
	}
	metadata, err := localcookbook.LoadMetadata(dir)
	if err != nil {
		return ShareResult{}, localcookbook.Archive{}, err
	}
	md := metadata.Metadata
	if md.Name != opts.Cookbook {
		return ShareResult{}, localcookbook.Archive{}, fmt.Errorf("metadata name %q does not match requested cookbook %q", md.Name, opts.Cookbook)
	}
	if category == "" {
		category = "Other"
	}

	archive, err := localcookbook.BuildArchiveWithOptions(dir, opts.Cookbook, localcookbook.ArchiveOptions{
		MetadataJSON:   metadata.JSON,
		IncludeFiles:   includeFiles,
		SkipChefignore: opts.SkipChefignore,
	})
	if err != nil {
		return ShareResult{}, localcookbook.Archive{}, err
	}
	return ShareResult{
		Cookbook: opts.Cookbook, Version: md.Version, Category: category,
		Uploaded: false, Status: 0, Tarball: archive.Name,
		TarballSize: len(archive.Bytes), Files: archive.Files,
	}, archive, nil
}

func (c *Client) lookupCategory(ctx context.Context, cookbook string) (string, error) {
	cb, _, err := c.api.Cookbooks.Get(ctx, cookbook)
	if err != nil {
		if errors.Is(err, sm.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	return cb.Category, nil
}

func (c *Client) upload(ctx context.Context, cookbook, category string, archive localcookbook.Archive) (int, error) {
	_, resp, err := c.api.Cookbooks.Share(ctx, cookbook, category, bytes.NewReader(archive.Bytes))
	if err != nil {
		return 0, fmt.Errorf("supermarket: upload %s: %w", cookbook, err)
	}
	return resp.StatusCode, nil
}
