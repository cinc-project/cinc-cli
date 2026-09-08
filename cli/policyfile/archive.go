package policyfile

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"

	cinc "github.com/tas50/cinc-api"
)

// BundleLockName is the Policyfile lock file Export writes at the root of a
// bundle. push-archive reads it back to discover the policy and its cookbooks.
const BundleLockName = "Policyfile.lock.json"

// OpenBundle resolves a push-archive input — either a .tar.gz archive (as
// written by Export with --archive) or an already-extracted bundle directory —
// to an on-disk bundle directory holding the lock and a cookbooks/ tree. A
// tarball is extracted into a fresh temp directory; the returned cleanup removes
// it. cleanup is always non-nil and safe to defer (a no-op for a directory
// input).
func OpenBundle(path string) (string, func(), error) {
	noop := func() {}
	info, err := os.Stat(path)
	if err != nil {
		return "", noop, err
	}
	if info.IsDir() {
		return path, noop, nil
	}

	tmp, err := os.MkdirTemp("", "cinc-push-archive-")
	if err != nil {
		return "", noop, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	if err := extractBundleTarball(path, tmp); err != nil {
		cleanup()
		return "", noop, err
	}
	dir, err := bundleRoot(tmp)
	if err != nil {
		cleanup()
		return "", noop, err
	}
	return dir, cleanup, nil
}

// LoadBundleCookbooks reads each cookbook the lock pins from a bundle's
// cookbooks/<name>-<identifier>/ tree into an uploadable LocalCookbook, keyed by
// cookbook name. The directory layout matches what Export writes; the cookbook's
// upload name is taken from the lock (not the on-disk directory, which carries
// the identifier suffix).
func LoadBundleCookbooks(dir string, lock *cinc.PolicyRevision) (map[string]*cinc.LocalCookbook, error) {
	cookbooks := make(map[string]*cinc.LocalCookbook, len(lock.CookbookLocks))
	for name, cl := range lock.CookbookLocks {
		ddi := cl.DottedDecimalIdentifier
		if ddi == "" {
			ddi = cl.Identifier
		}
		// name and ddi come from the (untrusted) lock; keep the lookup from
		// escaping the bundle's cookbooks directory.
		cbDir, err := safeJoin(dir, "cookbooks", name+"-"+ddi)
		if err != nil {
			return nil, fmt.Errorf("policyfile: cookbook %q: %w", name, err)
		}
		cb, err := cinc.LocalCookbookFromDir(cbDir, cl.Version)
		if err != nil {
			return nil, fmt.Errorf("policyfile: load cookbook %q from %s: %w", name, cbDir, err)
		}
		// The on-disk directory is "<name>-<identifier>"; the upload name is
		// the bare cookbook name the lock records.
		cb.Name = name
		cookbooks[name] = cb
	}
	return cookbooks, nil
}

// extractBundleTarball reads a gzip-compressed tar stream at archivePath and
// writes its entries under dest, preserving the directory structure. Paths that
// would escape dest are rejected.
func extractBundleTarball(archivePath, dest string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }() // read handle
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("policyfile: open archive %s: %w", archivePath, err)
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
			return fmt.Errorf("policyfile: read archive %s: %w", archivePath, err)
		}
		rel := filepath.Clean(filepath.FromSlash(hdr.Name))
		target := filepath.Join(dest, rel)
		if !withinDir(dest, target) {
			return fmt.Errorf("policyfile: unsafe path in archive: %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, extractDirMode); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), extractDirMode); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, extractFileMode)
			if err != nil {
				return err
			}
			if err := boundedCopy(out, tr, hdr.Name, &total); err != nil {
				_ = out.Close() // already returning an error
				return fmt.Errorf("policyfile: %w", err)
			}
			if err := out.Close(); err != nil {
				return err
			}
		}
	}
}

// bundleRoot returns the directory holding the bundle's Policyfile.lock.json. An
// archive written by Export wraps everything in a single top-level directory, so
// the lock is one level down; a bundle extracted some other way may have it at
// the root.
func bundleRoot(dir string) (string, error) {
	if fileExists(filepath.Join(dir, BundleLockName)) {
		return dir, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() && fileExists(filepath.Join(dir, e.Name(), BundleLockName)) {
			return filepath.Join(dir, e.Name()), nil
		}
	}
	return "", fmt.Errorf("policyfile: the archive doesn't contain a %s — is it a `cinc policy export` bundle?", BundleLockName)
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
