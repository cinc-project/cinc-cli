package cookbook

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Extraction modes ignore the untrusted tar entry bits and clamp to
// non-world-writable, owner-plus-group-readable.
const (
	extractDirMode  = 0o750
	extractFileMode = 0o640
)

// Extraction byte caps guard against a zip-bomb / disk-fill DoS (a tiny gzip
// stream can expand into enormous output). They're vars (not consts) only so
// tests can shrink them; production never reassigns them.
var (
	maxExtractedFileBytes    int64 = 512 << 20 // 512 MiB per file
	maxExtractedArchiveBytes int64 = 2 << 30   // 2 GiB per archive total
)

// ExtractArchive unpacks a gzipped cookbook tarball read from r into
// destDir and returns the path of the single top-level directory the
// archive creates (Supermarket tarballs are rooted at <cookbook>/...).
//
// Entries whose paths escape destDir are refused, so a hostile tarball
// can't write outside the destination.
func ExtractArchive(r io.Reader, destDir string) (string, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return "", fmt.Errorf("open gzip: %w", err)
	}
	defer func() { _ = gz.Close() }() // read handle

	tr := tar.NewReader(gz)
	roots := map[string]struct{}{}
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read tar: %w", err)
		}

		target, err := safeJoin(destDir, hdr.Name)
		if err != nil {
			return "", err
		}
		if root := topLevel(hdr.Name); root != "" {
			roots[root] = struct{}{}
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, extractDirMode); err != nil {
				return "", fmt.Errorf("mkdir %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := writeFile(tr, target, hdr.Name, &total); err != nil {
				return "", err
			}
		default:
			// Skip symlinks, devices, and other non-regular entries.
		}
	}

	if len(roots) != 1 {
		return "", fmt.Errorf("cookbook archive has %d top-level entries, want exactly 1", len(roots))
	}
	var root string
	for r := range roots {
		root = r
	}
	return filepath.Join(destDir, root), nil
}

// safeJoin joins name onto destDir and verifies the result stays within
// destDir, guarding against path-traversal ("zip slip") entries.
func safeJoin(destDir, name string) (string, error) {
	target := filepath.Join(destDir, filepath.FromSlash(name))
	rel, err := filepath.Rel(destDir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry %q escapes the destination directory", name)
	}
	return target, nil
}

// topLevel returns the first real path segment of a tar entry name, or ""
// if the entry has none. It normalizes away leading "./" and "/" so a
// "./nginx/metadata.rb" entry (the layout real Supermarket tarballs use)
// reports "nginx" rather than ".", and bare "." / ".." entries report "".
func topLevel(name string) string {
	clean := strings.TrimPrefix(path.Clean(filepath.ToSlash(name)), "/")
	if clean == "" || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return ""
	}
	root, _, _ := strings.Cut(clean, "/")
	return root
}

// writeFile creates target (with parent directories) and copies the
// current tar entry into it, capping output so a zip bomb can't fill the
// disk. total accumulates across every entry in one archive.
func writeFile(r io.Reader, target, name string, total *int64) error {
	if err := os.MkdirAll(filepath.Dir(target), extractDirMode); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(target), err)
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, extractFileMode)
	if err != nil {
		return fmt.Errorf("create %s: %w", target, err)
	}
	if err := boundedCopy(f, r, name, total); err != nil {
		_ = f.Close() // already returning an error
		return fmt.Errorf("write %s: %w", target, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", target, err)
	}
	return nil
}

// boundedCopy copies the current tar entry into dst, failing if the entry
// exceeds the per-file cap or pushes the running archive total past the
// whole-archive cap.
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
