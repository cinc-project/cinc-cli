package supermarket

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	sm "github.com/cinc-project/cinc-supermarket-api"
)

// DownloadOptions controls a cookbook download.
type DownloadOptions struct {
	Cookbook string
	// Version is "latest" (or empty, treated as "latest") or any Semver
	// the Supermarket API accepts. The resolved version goes into
	// DownloadResult.Version.
	Version string
	// File is the on-disk target. It may be empty (default
	// "<cookbook>-<version>.tar.gz" in the current directory), a
	// directory (default name dropped inside it), or a full file path.
	File string
	// Force overwrites an existing target file.
	Force bool
}

// DownloadResult describes the work done by Download.
type DownloadResult struct {
	Cookbook string `json:"cookbook"`
	Version  string `json:"version"`
	File     string `json:"file"`
	Bytes    int64  `json:"bytes"`
}

// NewAnonymous builds a Supermarket client that performs only anonymous
// reads. No profile, key, or identity is required — use this for
// commands like `download` and `explore` that never hit signed
// endpoints.
func NewAnonymous(site string) (*Client, error) {
	if site == "" {
		site = DefaultSite
	}
	base, err := url.Parse(strings.TrimRight(site, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("supermarket: invalid site URL %q", site)
	}
	api, err := sm.NewClient(sm.Config{BaseURL: base.String()}, sm.WithUserAgent("cinc-cli"))
	if err != nil {
		return nil, err
	}
	return &Client{base: base, api: api}, nil
}

// Reachable verifies the Supermarket endpoint is responding by hitting its
// health endpoint through the cinc-supermarket client. It is the
// unauthenticated preflight used by `cinc config validate`; network access
// stays delegated to the library rather than being hand-rolled in the command
// layer.
func (c *Client) Reachable(ctx context.Context) error {
	_, _, err := c.api.Health.Status(ctx)
	return err
}

// Download fetches a cookbook tarball from Supermarket and writes it
// to opts.File (or the resolved default location).
func (c *Client) Download(ctx context.Context, opts DownloadOptions) (DownloadResult, error) {
	if opts.Cookbook == "" {
		return DownloadResult{}, errors.New("supermarket: cookbook name is required")
	}
	requested := opts.Version
	if requested == "" {
		requested = "latest"
	}

	version, err := c.resolveDownloadVersion(ctx, opts.Cookbook, requested)
	if err != nil {
		return DownloadResult{}, err
	}

	target, err := resolveDownloadPath(opts.File, opts.Cookbook, version)
	if err != nil {
		return DownloadResult{}, err
	}
	if !opts.Force {
		if _, err := os.Stat(target); err == nil {
			return DownloadResult{}, fmt.Errorf("%s already exists. Pass --force to overwrite.", target)
		}
	}

	body, _, err := c.api.Cookbooks.Download(ctx, opts.Cookbook, version)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("supermarket: download %s %s: %w", opts.Cookbook, version, err)
	}
	defer body.Close()

	written, err := writeAtomic(target, body)
	if err != nil {
		return DownloadResult{}, err
	}
	return DownloadResult{
		Cookbook: opts.Cookbook,
		Version:  version,
		File:     target,
		Bytes:    written,
	}, nil
}

// resolveDownloadVersion turns "latest" (or empty) into the concrete
// version Supermarket would serve, so the final on-disk filename and
// JSON result reflect what was actually downloaded.
func (c *Client) resolveDownloadVersion(ctx context.Context, cookbook, version string) (string, error) {
	if version != "latest" {
		return version, nil
	}
	v, _, err := c.api.Cookbooks.GetVersion(ctx, cookbook, "latest")
	if err != nil {
		return "", fmt.Errorf("supermarket: resolve latest %s: %w", cookbook, err)
	}
	return v.Version, nil
}

// resolveDownloadPath turns the user-supplied --file value (which may
// be empty, a directory, or a full path) into the absolute target the
// tarball should land at.
func resolveDownloadPath(file, cookbook, version string) (string, error) {
	name := cookbook + "-" + version + ".tar.gz"
	if file == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return filepath.Join(cwd, name), nil
	}
	if info, err := os.Stat(file); err == nil && info.IsDir() {
		return filepath.Join(file, name), nil
	}
	return file, nil
}

// writeAtomic streams body into target via a sibling ".partial" file,
// renaming on success. If the stream fails partway, the partial is
// removed so a retry starts from a clean slate.
func writeAtomic(target string, body io.Reader) (int64, error) {
	partial := target + ".partial"
	f, err := os.Create(partial)
	if err != nil {
		return 0, fmt.Errorf("supermarket: create %s: %w", partial, err)
	}
	written, copyErr := io.Copy(f, body)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(partial)
		return 0, fmt.Errorf("supermarket: stream tarball: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(partial)
		return 0, fmt.Errorf("supermarket: close %s: %w", partial, closeErr)
	}
	if err := os.Rename(partial, target); err != nil {
		_ = os.Remove(partial)
		return 0, fmt.Errorf("supermarket: rename %s -> %s: %w", partial, target, err)
	}
	return written, nil
}
