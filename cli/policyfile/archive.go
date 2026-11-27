package policyfile

import (
	"fmt"
	"os"
	"path/filepath"

	cinc "github.com/cinc-project/cinc-api"

	"github.com/cinc-project/cinc-cli/cli/internal/tarball"
	"github.com/cinc-project/cinc-cli/cli/progname"
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
		dir, err := bundleRoot(path, path)
		return dir, noop, err
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
	dir, err := bundleRoot(tmp, path)
	if err != nil {
		cleanup()
		return "", noop, err
	}
	return dir, cleanup, nil
}

// LoadBundleCookbooks reads each cookbook the lock pins from a bundle's
// cookbooks/<name>-<identifier>/ tree into an uploadable LocalCookbook, keyed by
// cookbook name. The directory layout matches what Export writes. PushRevision
// uploads each one under its lock name, so the identifier suffix on the
// directory never reaches the server.
func LoadBundleCookbooks(dir string, lock *cinc.PolicyRevision) (map[string]*cinc.LocalCookbook, error) {
	cookbooks := make(map[string]*cinc.LocalCookbook, len(lock.CookbookLocks))
	for name, cl := range lock.CookbookLocks {
		ddi := cl.DottedIdentifier()
		// name and ddi come from the (untrusted) lock; keep the lookup from
		// escaping the bundle's cookbooks directory.
		cbDir, err := safeJoin(dir, "cookbooks", name+"-"+ddi)
		if err != nil {
			return nil, fmt.Errorf("the bundle's lock names cookbook %q with identifier %q, which we won't use as a directory name: %w. "+
				"The lock is damaged or hand-edited; export the bundle again with `%s policy export --archive`", name, ddi, err, progname.Get())
		}
		cb, err := cinc.LocalCookbookFromDir(cbDir, cl.Version)
		if err != nil {
			rel := filepath.Join("cookbooks", name+"-"+ddi)
			return nil, fmt.Errorf("we couldn't load cookbook %q from the bundle: the lock says it's in %s, and reading it failed (%w). "+
				"The bundle looks incomplete; export it again with `%s policy export --archive`", name, rel, err, progname.Get())
		}
		cookbooks[name] = cb
	}
	return cookbooks, nil
}

// extractBundleTarball extracts the gzip-compressed tarball at archivePath
// under dest, guarded as tarball.Extract describes.
func extractBundleTarball(archivePath, dest string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }() // read handle
	if err := tarball.Extract(f, dest, tarball.Options{}); err != nil {
		return fmt.Errorf("we couldn't unpack %s: %w", archivePath, err)
	}
	return nil
}

// bundleRoot returns the directory holding the bundle's Policyfile.lock.json. An
// archive written by Export wraps everything in a single top-level directory, so
// the lock is one level down; a bundle extracted some other way may have it at
// the root. given is the path the user named, for the error.
func bundleRoot(dir, given string) (string, error) {
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
	return "", fmt.Errorf("%s isn't a policy bundle: it has no %s, at its top level or one directory down. "+
		"A bundle holds a policy's lock and every cookbook it pins, and `%s policy export --archive` makes one from a lock", given, BundleLockName, progname.Get())
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
