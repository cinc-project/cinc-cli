package supermarket

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

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
	base, err := validateSite(site)
	if err != nil {
		return nil, err
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
		return DownloadResult{}, missingCookbookNameError("download")
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
			return DownloadResult{}, fmt.Errorf("%s already exists, so we didn't download over it. Pass --force to replace it, or --file to save the tarball somewhere else.", target)
		}
	}

	body, _, err := c.api.Cookbooks.Download(ctx, opts.Cookbook, version)
	if err != nil {
		return DownloadResult{}, c.readError(ctx, opts.Cookbook, version, "download "+opts.Cookbook+" "+version, err)
	}
	defer body.Close()

	written, err := writeAtomic(target, body)
	if err != nil {
		return DownloadResult{}, c.saveError(opts.Cookbook, version, target, err)
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
		return "", c.readError(ctx, cookbook, version, "look up the latest version of "+cookbook, err)
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

// streamCutError marks a download whose body failed partway, as opposed
// to a local file problem, so saveError can tell the two apart.
type streamCutError struct{ err error }

func (e *streamCutError) Error() string { return "download cut off: " + e.err.Error() }
func (e *streamCutError) Unwrap() error { return e.err }

// saveError explains a failed writeAtomic. A cut-off stream is a network
// problem worth retrying; anything else is about the target path.
func (c *Client) saveError(cookbook, version, target string, err error) error {
	if cut, ok := errors.AsType[*streamCutError](err); ok {
		return explain(err, "the download of %s %s from %s stopped partway through (%v), so we didn't save it. Try again; if it keeps happening, check your network connection.",
			cookbook, version, c.base.String(), cut.err)
	}
	return explain(err, "we couldn't save %s %s to %s: %v. Check that the directory exists and you can write to it, or pass --file to save the tarball somewhere else.",
		cookbook, version, target, err)
}

// writeAtomic streams body into target via a sibling ".partial" file,
// renaming on success. If the stream fails partway, the partial is
// removed so a retry starts from a clean slate. A failed stream is
// wrapped in a streamCutError; file errors are returned as the os package
// reports them.
func writeAtomic(target string, body io.Reader) (int64, error) {
	partial := target + ".partial"
	f, err := os.Create(partial)
	if err != nil {
		return 0, err
	}
	written, copyErr := io.Copy(f, body)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(partial)
		return 0, &streamCutError{err: copyErr}
	}
	if closeErr != nil {
		_ = os.Remove(partial)
		return 0, closeErr
	}
	if err := os.Rename(partial, target); err != nil {
		_ = os.Remove(partial)
		return 0, err
	}
	return written, nil
}
