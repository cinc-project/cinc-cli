// Package setup handles first-run credential migration for cinc. It
// detects an existing ~/.chef/credentials file and writes the
// equivalent ~/.cinc/credentials file by delegating to cli/config.
// Guided setup of a fresh profile is handled separately by the
// `cinc config create` command.
package setup

import (
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/BurntSushi/toml"
	cinc "github.com/cinc-project/cinc-api"

	"github.com/cinc-project/cinc-cli/cli/config"
)

// chefRawProfile is the on-disk shape of one credentials section in
// either ~/.chef/credentials or ~/.cinc/credentials. Cinc- and
// chef-prefixed server URLs are both accepted; when both are set the
// cinc-prefixed value wins, matching cli/config's precedence rule.
type chefRawProfile struct {
	CincServerURL         string `toml:"cinc_server_url"`
	ChefServerURL         string `toml:"chef_server_url"`
	SupermarketSite       string `toml:"supermarket_site"`
	ClientName            string `toml:"client_name"`
	ClientKey             string `toml:"client_key"`
	SSLVerifyMode         string `toml:"ssl_verify_mode"`
	SecretFile            string `toml:"secret_file"`
	SupermarketClientName string `toml:"supermarket_client_name"`
	SupermarketKey        string `toml:"supermarket_key"`
	TrustedCertsDir       string `toml:"trusted_certs_dir"`
}

// CheckChef reports why the knife credentials file at chefPath cannot be
// migrated, or nil if MigrateChef can migrate it. First-run setup asks
// this before offering the migration, so it never offers one that can only
// fail or migrate nothing. The reason is phrased to follow "we can't
// migrate it: ".
func CheckChef(chefPath string) error {
	info, err := os.Stat(chefPath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return errors.New("it's a directory, not a file")
	}
	var raw map[string]chefRawProfile
	if _, err := toml.DecodeFile(chefPath, &raw); err != nil {
		return fmt.Errorf("we couldn't read it (%w)", err)
	}
	if len(raw) == 0 {
		return errNoProfiles
	}
	return nil
}

// errNoProfiles is CheckChef's and MigrateChef's error for a knife file
// with no profiles in it.
var errNoProfiles = errors.New("it has no profiles")

// MigrateChef reads chefPath and writes the equivalent credentials
// file to cincPath. Each profile is built via config.NewProfile (which
// validates client name, key path, and either a Chef server URL or a
// Supermarket site) and then persisted via config.WriteProfileWithExtras,
// so the keys cinc manages come out exactly as `cinc config create` would
// write them, chef_server_url renamed to cinc_server_url. Every other key
// in the profile is copied verbatim. Returns the number of profiles
// migrated.
//
// Migration is all-or-nothing: every profile is resolved and validated
// before any of them is written. A half-migrated file would be worse than
// none at all, because the first-run flow only offers to migrate while
// ~/.cinc/credentials is still absent, so a partial write would strand the
// user with some profiles missing and no prompt to finish. For the same
// reason a file with no profiles is an error, not a migration of nothing.
func MigrateChef(chefPath, cincPath string) (int, error) {
	var raw map[string]chefRawProfile
	if _, err := toml.DecodeFile(chefPath, &raw); err != nil {
		return 0, fmt.Errorf("setup: parse %s: %w", chefPath, err)
	}
	if len(raw) == 0 {
		return 0, fmt.Errorf("setup: %s: %w", chefPath, errNoProfiles)
	}
	// Decode again without a schema, so the keys chefRawProfile does not model
	// (knife's node_name, validation_key, a knife table, ...) are copied across
	// verbatim instead of silently dropped. The first decode succeeded, so
	// this one cannot fail on syntax.
	var everything map[string]map[string]any
	if _, err := toml.DecodeFile(chefPath, &everything); err != nil {
		return 0, fmt.Errorf("setup: parse %s: %w", chefPath, err)
	}

	// Resolve every profile first. Sorted so a file with more than one
	// unmigratable profile always reports the same one.
	names := make([]string, 0, len(raw))
	for name := range raw {
		names = append(names, name)
	}
	sort.Strings(names)

	resolved := make([]config.Profile, 0, len(names))
	for _, name := range names {
		rp := raw[name]
		serverURL := rp.CincServerURL
		if serverURL == "" {
			serverURL = rp.ChefServerURL
		}
		// A knife server URL names a server even when it has no
		// /organizations/<org> (chef-zero's docs use a bare host), so it
		// never goes through NewProfile's fallback that files such a URL
		// away as a Supermarket site. It is kept as written instead, and
		// config validate and the server commands say what it lacks.
		orgless := ""
		if _, _, err := cinc.ParseServerURL(serverURL); serverURL != "" && err != nil {
			orgless, serverURL = serverURL, ""
		}
		profile, err := config.NewProfile(serverURL, rp.ClientName, rp.ClientKey, rp.SSLVerifyMode, rp.SupermarketSite)
		if err != nil {
			return 0, fmt.Errorf("setup: profile %q: %w", name, err)
		}
		profile.RawServerURL = orgless
		// NewProfile only takes the core connection fields, so carry the
		// remaining keys chefRawProfile models across by hand.
		profile.SecretFile = rp.SecretFile
		profile.SupermarketClientName = rp.SupermarketClientName
		profile.SupermarketKey = rp.SupermarketKey
		profile.TrustedCertsDir = rp.TrustedCertsDir
		resolved = append(resolved, profile)
	}

	// Everything validated, so the writes below can only fail on I/O.
	for i, name := range names {
		if err := config.WriteProfileWithExtras(cincPath, name, resolved[i], everything[name]); err != nil {
			return 0, err
		}
	}
	return len(names), nil
}
