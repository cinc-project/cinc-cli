package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// defaultTrustedCertsDirs are the directories, relative to the home directory,
// searched for extra CA certificates when a profile does not set
// trusted_certs_dir. The cinc location comes first; ~/.chef/trusted_certs is
// where knife keeps them (`knife ssl fetch` writes there), so an existing
// chef setup keeps working unchanged.
var defaultTrustedCertsDirs = []string{
	filepath.Join(".cinc", "trusted_certs"),
	filepath.Join(".chef", "trusted_certs"),
}

// ResolveTrustedCertsDir returns the directory of extra CA certificates this
// profile trusts, and whether the profile configured it explicitly.
//
// An explicit trusted_certs_dir has a leading ~ expanded to the home
// directory; any other path is returned as written, so a relative path is
// relative to the working directory, exactly as client_key is. The directory
// is not checked here: a missing explicit directory is the caller's error to
// report.
//
// With nothing configured, the first of ~/.cinc/trusted_certs and
// ~/.chef/trusted_certs that exists as a directory is returned, with explicit
// false. When neither exists the result is "", meaning trust the system pool
// only.
func (p Profile) ResolveTrustedCertsDir() (dir string, explicit bool, err error) {
	if p.TrustedCertsDir != "" {
		dir, err := ExpandHome(p.TrustedCertsDir)
		if err != nil {
			return "", true, err
		}
		return dir, true, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// No home directory means no default locations; that's not an error.
		return "", false, nil
	}
	for _, rel := range defaultTrustedCertsDirs {
		candidate := filepath.Join(home, rel)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, false, nil
		}
	}
	return "", false, nil
}

// ExpandHome expands a leading "~" or "~/" to the user's home directory and
// returns any other path unchanged. Every path the credentials file holds
// (client_key, supermarket_key, secret_file, trusted_certs_dir) goes through
// it when it is read, never when the file is written, so a rewritten profile
// keeps the portable ~ form rather than an absolute home path.
func ExpandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: we couldn't find your home directory to expand %s: %w", path, err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
}
