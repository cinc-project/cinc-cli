package supermarket

import (
	"context"
	"errors"
	"fmt"
	"os"

	cinc "github.com/cinc-project/cinc-api"

	localcookbook "github.com/cinc-project/cinc-cli/cli/cookbook"
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
		return InstallResult{}, errors.New("supermarket: cookbook name is required")
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
		return InstallResult{}, fmt.Errorf("supermarket: download %s %s: %w", opts.Cookbook, version, err)
	}
	defer body.Close()

	tmp, err := os.MkdirTemp("", "cinc-install-")
	if err != nil {
		return InstallResult{}, fmt.Errorf("supermarket: create temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	cookbookDir, err := localcookbook.ExtractArchive(body, tmp)
	if err != nil {
		return InstallResult{}, fmt.Errorf("supermarket: unpack %s: %w", opts.Cookbook, err)
	}

	cb, err := localcookbook.UploadableFromDir(cookbookDir, version)
	if err != nil {
		return InstallResult{}, fmt.Errorf("supermarket: read %s: %w", opts.Cookbook, err)
	}
	if err := server.Cookbooks.Upload(ctx, cb); err != nil {
		return InstallResult{}, fmt.Errorf("supermarket: upload %s %s to server: %w", opts.Cookbook, version, err)
	}

	return InstallResult{Cookbook: opts.Cookbook, Version: version, Installed: true}, nil
}
