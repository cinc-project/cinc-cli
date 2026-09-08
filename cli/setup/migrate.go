// Package setup handles first-run credential migration for cinc. It
// detects an existing ~/.chef/credentials file and writes the
// equivalent ~/.cinc/credentials file by delegating to cli/config.
// Guided setup of a fresh profile is handled separately by the
// `cinc config create` command.
package setup

import (
	"fmt"
	"sort"

	"github.com/BurntSushi/toml"

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
}

// MigrateChef reads chefPath and writes the equivalent credentials
// file to cincPath. Each profile is built via config.NewProfile (which
// validates client name, key path, and either a Chef server URL or a
// Supermarket site) and then persisted via config.WriteProfile, so the
// resulting file is byte-identical to what `cinc config create` would
// produce for the same inputs. Returns the number of profiles
// migrated.
//
// Migration is all-or-nothing: every profile is resolved and validated
// before any of them is written. A half-migrated file would be worse than
// none at all, because the first-run flow only offers to migrate while
// ~/.cinc/credentials is still absent, so a partial write would strand the
// user with some profiles missing and no prompt to finish.
func MigrateChef(chefPath, cincPath string) (int, error) {
	var raw map[string]chefRawProfile
	if _, err := toml.DecodeFile(chefPath, &raw); err != nil {
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
		profile, err := config.NewProfile(serverURL, rp.ClientName, rp.ClientKey, rp.SSLVerifyMode, rp.SupermarketSite)
		if err != nil {
			return 0, fmt.Errorf("setup: profile %q: %w", name, err)
		}
		// NewProfile only takes the core connection fields, so carry the
		// remaining keys chefRawProfile models across by hand.
		profile.SecretFile = rp.SecretFile
		profile.SupermarketClientName = rp.SupermarketClientName
		profile.SupermarketKey = rp.SupermarketKey
		resolved = append(resolved, profile)
	}

	// Everything validated, so the writes below can only fail on I/O.
	for i, name := range names {
		if err := config.WriteProfile(cincPath, name, resolved[i]); err != nil {
			return 0, err
		}
	}
	return len(names), nil
}
