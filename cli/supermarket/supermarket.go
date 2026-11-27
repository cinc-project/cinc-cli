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
	"os"
	"slices"

	cinc "github.com/cinc-project/cinc-api"

	sm "github.com/cinc-project/cinc-supermarket-api"

	"github.com/cinc-project/cinc-cli/cli/config"
	localcookbook "github.com/cinc-project/cinc-cli/cli/cookbook"
	"github.com/cinc-project/cinc-cli/cli/progname"
)

// DefaultSite is the public Supermarket instance.
const DefaultSite = sm.DefaultBaseURL

// Client uploads cookbooks to one Chef Supermarket endpoint.
type Client struct {
	base *url.URL
	api  *sm.Client
	// identity records who signs uploads and where that was configured,
	// for error messages. It is zero for an anonymous client.
	identity identity
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
func New(profile config.Profile, site string, opts ...Option) (*Client, error) {
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
	base, err := validateSite(site)
	if err != nil {
		return nil, err
	}
	c := &Client{base: base, identity: identity{
		user:    username,
		userSet: "supermarket_client_name",
		keyPath: keyPath,
		keySet:  "supermarket_key",
		site:    base.String(),
	}}
	if profile.SupermarketClientName == "" {
		c.identity.userSet = "client_name"
	}
	if profile.SupermarketKey == "" {
		c.identity.keySet = "client_key"
	}
	for _, opt := range opts {
		opt(c)
	}
	if expanded, err := config.ExpandHome(keyPath); err == nil {
		c.identity.keyPath = expanded
	}
	// Read and parse separately so the message can tell a missing or
	// unreadable file from one that isn't a private key.
	pemBytes, err := os.ReadFile(c.identity.keyPath)
	if err != nil {
		return nil, keyLoadError(c.identity, err, nil)
	}
	key, err := sm.ParseKey(pemBytes)
	if err != nil {
		return nil, keyLoadError(c.identity, nil, err)
	}
	c.api, err = sm.NewClient(sm.Config{
		BaseURL:  base.String(),
		Username: username,
		Key:      key,
	}, sm.WithUserAgent("cinc-cli"))
	if err != nil {
		return nil, err
	}
	return c, nil
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
	status, err := c.upload(ctx, opts.Cookbook, result.Version, result.Category, archive)
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
	cb, err := localcookbook.Load(dir, opts.SkipChefignore)
	if err != nil {
		return ShareResult{}, localcookbook.Archive{}, err
	}
	if cb.Name != opts.Cookbook {
		prog := progname.Get()
		return ShareResult{}, localcookbook.Archive{}, fmt.Errorf(`the cookbook at %s is named %q in its metadata, not %q.

Supermarket publishes a cookbook under the name in its metadata, so that's the name to share it by:
  %s supermarket share %s
If %q is the name you want, change the name in the cookbook's metadata.rb first.`, dir, cb.Name, opts.Cookbook, prog, cb.Name, opts.Cookbook)
	}
	if category == "" {
		category = "Other"
	}
	// Supermarket reads a cookbook's metadata.json, so the tarball needs one:
	// the cookbook's own when it ships one, otherwise compiled from
	// metadata.rb the way knife does.
	var overlay map[string][]byte
	if !slices.ContainsFunc(cb.Files(), func(f cinc.LocalCookbookFile) bool { return f.Path == "metadata.json" }) {
		compiled, err := cb.Metadata.CompiledJSON()
		if err != nil {
			return ShareResult{}, localcookbook.Archive{}, err
		}
		overlay = map[string][]byte{"metadata.json": compiled}
	}
	archive, err := localcookbook.BuildArchive(cb, overlay)
	if err != nil {
		return ShareResult{}, localcookbook.Archive{}, err
	}
	result := ShareResult{
		Cookbook: cb.Name, Version: cb.Version, Category: category,
		Tarball: archive.Name, TarballSize: len(archive.Bytes),
	}
	if includeFiles {
		result.Files = archive.Files
	}
	return result, archive, nil
}

func (c *Client) lookupCategory(ctx context.Context, cookbook string) (string, error) {
	cb, _, err := c.api.Cookbooks.Get(ctx, cookbook)
	if err != nil {
		if errors.Is(err, sm.ErrNotFound) {
			return "", nil
		}
		return "", requestError(c.base.String(), "look up "+cookbook+"'s category", err)
	}
	return cb.Category, nil
}

func (c *Client) upload(ctx context.Context, cookbook, version, category string, archive localcookbook.Archive) (int, error) {
	_, resp, err := c.api.Cookbooks.Share(ctx, cookbook, category, bytes.NewReader(archive.Bytes))
	if err != nil {
		return 0, c.shareError(cookbook, version, err)
	}
	return resp.StatusCode, nil
}
