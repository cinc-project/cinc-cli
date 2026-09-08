// Package policyfile deploys an existing Policyfile.lock.json to a Cinc/Chef
// Server. It fetches and caches the cookbooks the lock pins (from the sources
// the lock records), then drives the server-side push via cinc-api, and
// assembles a standalone export bundle. It owns the local, multi-source
// concerns; every server call goes through cinc-api or cinc-supermarket.
package policyfile

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Extraction modes deliberately ignore the tar entry's own bits (which we
// don't trust) and clamp to owner-plus-group-readable, never world-writable.
const (
	extractDirMode  = 0o750
	extractFileMode = 0o640
)

// Extraction byte caps guard against a zip-bomb / disk-fill DoS: a tiny gzip
// stream can otherwise expand into enormous output. They're vars (not consts)
// only so tests can shrink them; production never reassigns them.
var (
	maxExtractedFileBytes    int64 = 512 << 20 // 512 MiB per file
	maxExtractedArchiveBytes int64 = 2 << 30   // 2 GiB per archive total
)

// boundedCopy copies the current tar entry into dst, failing if the entry
// exceeds the per-file cap or pushes the running archive total past the
// whole-archive cap. total accumulates across every entry in one archive.
func boundedCopy(dst io.Writer, src io.Reader, name string, total *int64) error {
	// Apply the tighter of the per-file cap and the archive's remaining budget.
	limit := maxExtractedFileBytes
	if remaining := maxExtractedArchiveBytes - *total; remaining < limit {
		limit = remaining
	}
	if limit < 0 {
		limit = 0
	}
	// Read one byte past the limit so we can tell when an entry overflows it.
	n, err := io.Copy(dst, io.LimitReader(src, limit+1))
	*total += n
	if err != nil {
		return err
	}
	if n > limit {
		return fmt.Errorf("archive entry %q is too large: extraction is capped at %d MiB per file and %d MiB per archive", name, maxExtractedFileBytes>>20, maxExtractedArchiveBytes>>20)
	}
	return nil
}

// extractCookbookTarball reads a gzip-compressed tar stream and writes its
// entries under dest, stripping the single leading path segment that
// Supermarket tarballs wrap a cookbook in (e.g. "nginx/metadata.rb" lands at
// dest/metadata.rb), so dest ends up holding the cookbook root directly. Paths
// that would escape dest are rejected, and every write goes through an os.Root
// handle so the kernel refuses an escape the lexical check cannot see (a
// symlink already sitting in dest, say).
func extractCookbookTarball(r io.Reader, dest string) error {
	root, err := os.OpenRoot(dest)
	if err != nil {
		return fmt.Errorf("supermarket: open destination %s: %w", dest, err)
	}
	defer func() { _ = root.Close() }()

	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("supermarket: open gzip: %w", err)
	}
	defer func() { _ = gz.Close() }() // read handle

	tr := tar.NewReader(gz)
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("supermarket: read tar: %w", err)
		}
		rel := stripLeadingSegment(hdr.Name)
		if rel == "" {
			continue
		}
		rel = filepath.FromSlash(rel)
		if !withinDir(dest, filepath.Join(dest, rel)) {
			return fmt.Errorf("supermarket: unsafe path in tarball: %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(rel, extractDirMode); err != nil {
				return err
			}
		case tar.TypeReg:
			if dir := filepath.Dir(rel); dir != "." {
				if err := root.MkdirAll(dir, extractDirMode); err != nil {
					return err
				}
			}
			f, err := root.OpenFile(rel, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, extractFileMode)
			if err != nil {
				return err
			}
			if err := boundedCopy(f, tr, hdr.Name, &total); err != nil {
				_ = f.Close() // already returning an error
				return fmt.Errorf("supermarket: %w", err)
			}
			if err := f.Close(); err != nil {
				return err
			}
		}
	}
}

// stripLeadingSegment drops the first path element of a slash-separated tar
// entry name, returning "" when there is nothing below it.
func stripLeadingSegment(name string) string {
	name = strings.TrimPrefix(filepath.ToSlash(name), "./")
	idx := strings.IndexByte(name, '/')
	if idx < 0 {
		return ""
	}
	return strings.Trim(name[idx+1:], "/")
}

// withinDir reports whether target stays inside dir (no "../" escape).
func withinDir(dir, target string) bool {
	rel, err := filepath.Rel(dir, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
