// Package cookbook finds local cookbooks and packs them into tarballs.
//
// What a cookbook is (which files belong to it, what its metadata says) is
// decided by cinc-api, the same way for every command: see Load.
package cookbook

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	cinc "github.com/cinc-project/cinc-api"
)

// Archive is an in-memory gzip-compressed tarball and its manifest.
type Archive struct {
	Name  string
	Bytes []byte
	// Files lists every entry, rooted at the cookbook name
	// (nginx/recipes/default.rb), in the order they were written.
	Files []string
}

// Locate finds a cookbook by name. cookbookPath may be empty, a single path,
// or a filepath-list of parent directories.
func Locate(name, cookbookPath string) (string, error) {
	for _, base := range candidateBases(cookbookPath) {
		if dir, ok := cookbookDir(base, name); ok {
			return dir, nil
		}
	}
	if cookbookPath == "" {
		return "", fmt.Errorf("cookbook %q not found in current directory or ./<name>", name)
	}
	return "", fmt.Errorf("cookbook %q not found in cookbook path %q", name, cookbookPath)
}

// Load reads the cookbook in dir the way an upload sees it: the files Chef's
// loader would pick (chefignore applied unless skipChefignore) and its
// metadata, with the name falling back to the directory's. A metadata.rb that
// computes its version is refused with a message saying how to fix it, since
// cinc can't run the Ruby and would otherwise publish the cookbook as 0.0.0.
func Load(dir string, skipChefignore bool) (*cinc.LocalCookbook, error) {
	var opts []cinc.LocalCookbookOption
	if skipChefignore {
		opts = append(opts, cinc.SkipChefignore())
	}
	// Absolute, so a cookbook without a declared name is named after the
	// directory it's in rather than ".".
	dir = absDir(dir)
	cb, err := cinc.LocalCookbookFromDir(dir, "", opts...)
	if errors.Is(err, cinc.ErrMetadataVersionNotLiteral) {
		return nil, fmt.Errorf("we can't tell which version the cookbook in %s is: its metadata.rb works the version out in Ruby, which cinc doesn't run. "+
			"Write the version as a plain string (version '1.2.3'), or add a metadata.json next to it, and try again (%w)", dir, err)
	}
	return cb, err
}

// BuildArchive packs cb's files into a deterministic gzipped tarball rooted at
// cb.Name/. overlay adds files, or replaces cb's own, by cookbook-relative
// path (metadata.json, say), without touching the cookbook on disk.
func BuildArchive(cb *cinc.LocalCookbook, overlay map[string][]byte) (Archive, error) {
	type entry struct {
		path, disk string
		data       []byte // overlay content; nil means read disk
	}
	var entries []entry
	for _, f := range cb.Files() {
		if _, ok := overlay[f.Path]; !ok {
			entries = append(entries, entry{path: f.Path, disk: f.DiskPath})
		}
	}
	for path, data := range overlay {
		entries = append(entries, entry{path: path, data: data})
	}
	slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(a.path, b.path) })

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.Name = cb.Name + ".tgz"
	gz.ModTime = time.Unix(0, 0)
	tw := tar.NewWriter(gz)
	files := make([]string, len(entries))
	for i, e := range entries {
		files[i] = cb.Name + "/" + e.path
		if err := writeTarEntry(tw, files[i], e.disk, e.data); err != nil {
			return Archive{}, fmt.Errorf("write tar entry %s: %w", e.path, err)
		}
	}
	if err := tw.Close(); err != nil {
		return Archive{}, fmt.Errorf("close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return Archive{}, fmt.Errorf("close gzip: %w", err)
	}
	return Archive{Name: cb.Name + ".tgz", Bytes: buf.Bytes(), Files: files}, nil
}

// writeTarEntry writes one file to tw under name: data, or when data is nil
// the file at disk (followed if it is a symlink).
func writeTarEntry(tw *tar.Writer, name, disk string, data []byte) error {
	var r io.Reader = bytes.NewReader(data)
	size := int64(len(data))
	if data == nil {
		f, err := os.Open(disk)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }() // read handle
		info, err := f.Stat()
		if err != nil {
			return err
		}
		r, size = f, info.Size()
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: size, ModTime: time.Unix(0, 0)}); err != nil {
		return err
	}
	_, err := io.Copy(tw, r)
	return err
}

func candidateBases(cookbookPath string) []string {
	if cookbookPath == "" {
		return []string{"."}
	}
	parts := filepath.SplitList(cookbookPath)
	if len(parts) == 0 {
		return []string{cookbookPath}
	}
	return parts
}

func cookbookDir(base, name string) (string, bool) {
	info, err := os.Stat(base)
	if err == nil && info.IsDir() && hasMetadata(base) && baseIsCookbook(base, name) {
		return base, true
	}
	dir := filepath.Join(base, name)
	info, err = os.Stat(dir)
	if err == nil && info.IsDir() && hasMetadata(dir) {
		return dir, true
	}
	return "", false
}

// baseIsCookbook reports whether base, already known to hold metadata, is the
// cookbook the caller asked for by name. It matches on either the directory's
// (absolute) basename — so "." resolves to the real folder name — or the
// cookbook's declared metadata name, which lets `share mondoo` succeed from a
// directory literally named chef-mondoo. Metadata that can't be read just
// doesn't match by name.
func baseIsCookbook(base, name string) bool {
	if filepath.Base(absDir(base)) == name {
		return true
	}
	md, _ := cinc.LoadCookbookMetadata(base)
	return md != nil && md.Name == name
}

// absDir resolves dir to an absolute path so a base of "." reflects the real
// working-directory name; it falls back to dir unchanged when resolution fails.
func absDir(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

func hasMetadata(dir string) bool {
	for _, name := range []string{"metadata.json", "metadata.rb"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}
