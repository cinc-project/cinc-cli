package supermarket

import (
	"context"
	"fmt"
	"os"

	cinc "github.com/cinc-project/cinc-api"

	localcookbook "github.com/cinc-project/cinc-cli/cli/cookbook"
	"github.com/cinc-project/cinc-cli/cli/progname"
)

// InstallOptions controls installing a Supermarket cookbook into a Cinc
// Server.
type InstallOptions struct {
	Cookbook string
	// Version is "latest" (or empty, treated as "latest") or any concrete
	// Semver the Supermarket API accepts. The resolved version goes into
	// InstallResult.Version.
	Version string
}

// InstallResult describes the work done by Install.
type InstallResult struct {
	Cookbook  string `json:"cookbook"`
	Version   string `json:"version"`
	Installed bool   `json:"installed"`
}

// Install downloads a cookbook from Supermarket and uploads it to the
// Cinc Server reached through server. The Supermarket download is
// anonymous; the upload is signed by the caller-supplied server client.
//
// Only the named cookbook is installed — dependencies are not resolved.
func (c *Client) Install(ctx context.Context, server *cinc.Client, opts InstallOptions) (InstallResult, error) {
	if opts.Cookbook == "" {
		return InstallResult{}, missingCookbookNameError("install")
	}
	requested := opts.Version
	if requested == "" {
		requested = "latest"
	}

	version, err := c.resolveDownloadVersion(ctx, opts.Cookbook, requested)
	if err != nil {
		return InstallResult{}, err
	}

	body, _, err := c.api.Cookbooks.Download(ctx, opts.Cookbook, version)
	if err != nil {
		return InstallResult{}, c.readError(ctx, opts.Cookbook, version, "download "+opts.Cookbook+" "+version, err)
	}
	defer body.Close()

	tmp, err := os.MkdirTemp("", "cinc-install-")
	if err != nil {
		return InstallResult{}, fmt.Errorf("we couldn't make a temporary directory to unpack %s in: %w. Check that your temp directory exists and has free space.", opts.Cookbook, err)
	}
	defer os.RemoveAll(tmp)

	cookbookDir, err := localcookbook.ExtractArchive(body, tmp)
	if err != nil {
		return InstallResult{}, c.badTarballError(opts.Cookbook, version, err)
	}

	cb, err := cinc.LocalCookbookFromDir(cookbookDir, version)
	if err != nil {
		return InstallResult{}, c.badTarballError(opts.Cookbook, version, err)
	}
	if err := server.Cookbooks.Upload(ctx, cb); err != nil {
		return InstallResult{}, fmt.Errorf("we downloaded %s %s from %s, but couldn't upload it to your Cinc server: %w", opts.Cookbook, version, c.base.String(), err)
	}

	return InstallResult{Cookbook: opts.Cookbook, Version: version, Installed: true}, nil
}

// badTarballError explains a download that isn't a cookbook we can read.
// The fault is with what the site served, so it names the site and points
// at the download command for a closer look.
func (c *Client) badTarballError(cookbook, version string, err error) error {
	return fmt.Errorf("we downloaded %s %s from %s, but couldn't unpack it as a cookbook: %w. If that address isn't a Supermarket, fix --supermarket-site or supermarket_site; otherwise run `%s supermarket download %s %s` to look at the tarball yourself.",
		cookbook, version, c.base.String(), err, progname.Get(), cookbook, version)
}
