// Package tarball unpacks untrusted gzipped tarballs (Supermarket cookbooks,
// policy export bundles, the ruby.wasm release) and answers the "does this
// path escape its directory" questions the rest of the CLI asks.
package tarball

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Extraction modes ignore the untrusted tar entry bits and clamp to
// owner-plus-group-readable, never world-writable.
const (
	DirMode  = 0o750
	FileMode = 0o640
)

// Extraction byte caps guard against a zip-bomb / disk-fill DoS (a tiny gzip
// stream can expand into enormous output). They're vars (not consts) only so
// tests can shrink them; production never reassigns them.
var (
	maxFileBytes    int64 = 512 << 20 // 512 MiB per file
	maxArchiveBytes int64 = 2 << 30   // 2 GiB per archive total
)

// Options adjusts Extract.
type Options struct {
	// StripTopLevel drops the first path segment of every entry, as
	// `tar --strip-components=1` does, so a tarball wrapping a cookbook in
	// nginx/ extracts its contents straight into dest. An entry with nothing
	// below its first segment is skipped.
	StripTopLevel bool
}

// Extract unpacks the gzip-compressed tar stream r into dest, which must
// exist. Only directories and regular files are written, with DirMode and
// FileMode; symlinks, hard links and devices are skipped. Each file is capped
// at 512 MiB and the archive at 2 GiB.
//
// An entry whose path would land outside dest is refused with an error
// naming it. That lexical check is for the message: every write goes through
// an os.Root handle on dest, so the kernel refuses an escape the check cannot
// see (a symlink already sitting in dest, say).
func Extract(r io.Reader, dest string, opts Options) error {
	root, err := os.OpenRoot(dest)
	if err != nil {
		return fmt.Errorf("open destination %s: %w", dest, err)
	}
	defer func() { _ = root.Close() }()

	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("open gzip: %w", err)
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
			return fmt.Errorf("read tar: %w", err)
		}
		name := filepath.ToSlash(hdr.Name)
		if opts.StripTopLevel {
			if name = stripTopLevel(name); name == "" {
				continue
			}
		}
		rel := filepath.FromSlash(name)
		if Escapes(rel) {
			return fmt.Errorf("archive entry %q escapes the destination directory", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(rel, DirMode); err != nil {
				return fmt.Errorf("mkdir %s: %w", rel, err)
			}
		case tar.TypeReg:
			if err := writeFile(root, tr, rel, hdr.Name, &total); err != nil {
				return err
			}
		}
	}
}

// stripTopLevel drops the first segment of a slash-separated entry name,
// after any leading "./" or "/", returning "" when nothing is below it.
func stripTopLevel(name string) string {
	name = strings.TrimLeft(strings.TrimPrefix(name, "./"), "/")
	_, rest, ok := strings.Cut(name, "/")
	if !ok {
		return ""
	}
	return strings.Trim(rest, "/")
}

// writeFile creates rel (with parent directories) under root and copies the
// current tar entry into it, capping output so a zip bomb can't fill the
// disk. total accumulates across every entry in one archive.
func writeFile(root *os.Root, r io.Reader, rel, name string, total *int64) error {
	if dir := filepath.Dir(rel); dir != "." {
		if err := root.MkdirAll(dir, DirMode); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	f, err := root.OpenFile(rel, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, FileMode)
	if err != nil {
		return fmt.Errorf("create %s: %w", rel, err)
	}
	if err := boundedCopy(f, r, name, total); err != nil {
		_ = f.Close() // already returning an error
		return fmt.Errorf("write %s: %w", rel, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", rel, err)
	}
	return nil
}

// boundedCopy copies the current tar entry into dst, failing if the entry
// exceeds the per-file cap or pushes the running archive total past the
// whole-archive cap.
func boundedCopy(dst io.Writer, src io.Reader, name string, total *int64) error {
	limit := min(maxFileBytes, max(maxArchiveBytes-*total, 0))
	// Read one byte past the limit so we can tell when an entry overflows it.
	n, err := io.Copy(dst, io.LimitReader(src, limit+1))
	*total += n
	if err != nil {
		return err
	}
	if n > limit {
		return fmt.Errorf("archive entry %q is too large: extraction is capped at %d MiB per file and %d MiB per archive", name, maxFileBytes>>20, maxArchiveBytes>>20)
	}
	return nil
}

// Escapes reports whether rel, a path meant to be relative, would resolve
// outside the directory it is joined to: it is absolute, or it climbs out
// with "..".
func Escapes(rel string) bool {
	if filepath.IsAbs(rel) {
		return true
	}
	clean := filepath.Clean(rel)
	return clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

// Within reports whether target is dir itself or lies beneath it, comparing
// the paths lexically.
func Within(dir, target string) bool {
	rel, err := filepath.Rel(dir, target)
	return err == nil && !Escapes(rel)
}
