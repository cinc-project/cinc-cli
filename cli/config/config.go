// Package config loads and resolves the cinc CLI credentials file: a
// TOML document holding a set of named connection profiles, in the same
// shape as Chef's `~/.chef/credentials` file.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
	cinc "github.com/cinc-project/cinc-api"
)

// Profile describes how to connect to a single Cinc Server. It is the
// resolved form of an on-disk credentials entry: the configured server URL
// has already been split into a bare server URL and an organization name.
type Profile struct {
	ServerURL       string
	Org             string
	SupermarketSite string
	ClientName      string
	KeyPath         string
	SSLVerifyMode   string

	// SupermarketClientName and SupermarketKey optionally override the
	// identity used to sign Chef Supermarket uploads, so a user can
	// publish to the public Supermarket under a different username or key
	// than the one they use against their Cinc Server. Each field falls
	// back independently: the effective Supermarket username is
	// SupermarketClientName or, when empty, ClientName; the effective key
	// is SupermarketKey or, when empty, KeyPath. These are cinc-only keys
	// (knife has no equivalent). See SupermarketIdentity.
	SupermarketClientName string
	SupermarketKey        string

	// SecretFile is the path to the encrypted data bag secret used to
	// encrypt and decrypt `cinc databag secret` items. The on-disk key
	// is `secret_file`, matching knife's `knife[:secret_file]`, so the
	// same key serves cinc and chef users.
	SecretFile string

	// TrustedCertsDir is the trusted_certs_dir key exactly as written in
	// the credentials file: a directory of extra CA certificates to trust
	// on top of the system pool. The key name matches knife's, so the same
	// file serves cinc and chef users. It is kept unexpanded here so a
	// rewrite of the profile never bakes an absolute home path into the
	// file; ResolveTrustedCertsDir expands it and applies the defaults.
	TrustedCertsDir string

	// RawServerURL is the server URL exactly as written in the config, before
	// it is split into ServerURL + Org. It is preserved even when the URL is
	// malformed (ServerURL/Org are then empty) so validation can report the
	// precise problem instead of the whole load failing.
	RawServerURL string
}

// rawProfile is the on-disk shape of one credentials section. Both the
// chef-prefixed key names knife writes and their cinc-prefixed equivalents
// are accepted; when both appear in the same profile the cinc_-prefixed
// value wins.
type rawProfile struct {
	CincServerURL         string `toml:"cinc_server_url,omitempty"`
	ChefServerURL         string `toml:"chef_server_url,omitempty"`
	SupermarketSite       string `toml:"supermarket_site,omitempty"`
	ClientName            string `toml:"client_name,omitempty"`
	ClientKey             string `toml:"client_key,omitempty"`
	SupermarketClientName string `toml:"supermarket_client_name,omitempty"`
	SupermarketKey        string `toml:"supermarket_key,omitempty"`
	SSLVerifyMode         string `toml:"ssl_verify_mode,omitempty"`
	SecretFile            string `toml:"secret_file,omitempty"`
	TrustedCertsDir       string `toml:"trusted_certs_dir,omitempty"`
}

// serverURL returns the configured server URL, preferring the
// cinc-prefixed key over the chef-prefixed key when both are set.
func (rp rawProfile) serverURL() string {
	if rp.CincServerURL != "" {
		return rp.CincServerURL
	}
	return rp.ChefServerURL
}

// ErrMissingServerURL is Validate's error for a profile that names no
// server, such as a Supermarket-only profile or the one first-run setup
// writes when every prompt is left at its default.
var ErrMissingServerURL = errors.New("config: profile is missing cinc_server_url (or chef_server_url)")

// Validate reports whether the profile has every field required to open a
// server connection.
func (p Profile) Validate() error {
	if err := p.ValidateIdentity(); err != nil {
		return err
	}
	switch {
	case p.ServerURL == "" && p.RawServerURL != "":
		// A server URL was written but didn't parse; report exactly why.
		if _, _, err := cinc.ParseServerURL(p.RawServerURL); err != nil {
			return fmt.Errorf("config: %w", err)
		}
		return ErrMissingServerURL
	case p.ServerURL == "":
		return ErrMissingServerURL
	case p.Org == "":
		return fmt.Errorf("config: profile is missing the /organizations/<org> segment in its server URL")
	}
	return nil
}

// ValidateIdentity reports whether the profile has the fields needed to sign
// requests that target a Cinc Server organization.
func (p Profile) ValidateIdentity() error {
	switch {
	case p.ClientName == "":
		return fmt.Errorf("config: profile is missing client_name")
	case p.KeyPath == "":
		return fmt.Errorf("config: profile is missing client_key")
	}
	return nil
}

// SupermarketIdentity returns the username and key path used to sign Chef
// Supermarket uploads. Each field falls back independently: the username is
// supermarket_client_name or, when unset, client_name; the key is
// supermarket_key or, when unset, client_key. This lets a user publish to
// the public Supermarket under a different identity than their Cinc Server
// client while overriding only the field that differs.
func (p Profile) SupermarketIdentity() (name, keyPath string) {
	name = p.SupermarketClientName
	if name == "" {
		name = p.ClientName
	}
	keyPath = p.SupermarketKey
	if keyPath == "" {
		keyPath = p.KeyPath
	}
	return name, keyPath
}

// ValidateSupermarketIdentity reports whether the profile resolves to an
// identity that can sign Supermarket uploads, accounting for the
// supermarket_client_name/supermarket_key overrides and their fallback to
// client_name/client_key.
func (p Profile) ValidateSupermarketIdentity() error {
	name, keyPath := p.SupermarketIdentity()
	if name == "" || keyPath == "" {
		return fmt.Errorf("config: we need a Supermarket identity to share — set client_name/client_key, or supermarket_client_name/supermarket_key, in your profile")
	}
	return nil
}

// Config is the parsed contents of a cinc credentials file.
type Config struct {
	Profiles map[string]Profile
}

// ValidationIssue describes one profile-level configuration problem.
type ValidationIssue struct {
	Profile string `json:"profile"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Validate reports configuration problems across every loaded profile. It is
// intentionally local-only: it validates TOML shape and URL/profile fields but
// does not perform network calls or authenticate.
func (c *Config) Validate() []ValidationIssue {
	if len(c.Profiles) == 0 {
		return []ValidationIssue{{
			Field:   "profiles",
			Message: "config has no profiles",
		}}
	}
	var issues []ValidationIssue
	for name, profile := range c.Profiles {
		issues = append(issues, validateProfile(name, profile)...)
	}
	return issues
}

// DefaultPath returns the default credentials file location,
// ~/.cinc/credentials.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: locate home directory: %w", err)
	}
	return filepath.Join(home, ".cinc", "credentials"), nil
}

// Load reads and parses the credentials file at path. Each top-level TOML
// section becomes a named profile.
func Load(path string) (*Config, error) {
	var raw map[string]rawProfile
	if _, err := toml.DecodeFile(path, &raw); err != nil {
		// An open or read failure already names the file. A parse or type
		// error only names a line, and the user may keep several files.
		var pathErr *fs.PathError
		if errors.As(err, &pathErr) {
			return nil, fmt.Errorf("config: %w", err)
		}
		return nil, fmt.Errorf("config: we couldn't read %s: %w", path, err)
	}
	cfg := &Config{Profiles: make(map[string]Profile, len(raw))}
	for name, rp := range raw {
		p, err := resolveProfile(rp)
		if err != nil {
			return nil, fmt.Errorf("config: profile %q: %w", name, err)
		}
		cfg.Profiles[name] = p
	}
	return cfg, nil
}

// managedKeys are the credentials keys cinc itself owns. WriteProfile sets or
// clears exactly these on the profile it writes and leaves every other key in
// the file alone, so a file shared with knife keeps the settings knife needs
// (validation_client_name, validation_key, node_name, ...) that cinc has no
// model for.
var managedKeys = []string{
	"cinc_server_url",
	"chef_server_url",
	"supermarket_site",
	"client_name",
	"client_key",
	"supermarket_client_name",
	"supermarket_key",
	"ssl_verify_mode",
	"secret_file",
	"trusted_certs_dir",
}

// UpdateProfile applies mutate to the profile named name in the credentials
// file at path and writes the result back.
//
// This is the entry point callers should reach for when they are changing an
// existing profile. WriteProfile replaces every key cinc manages, so a caller
// that assembles a Profile from only the values it collected clears the ones
// it did not: `config create` never prompts for secret_file,
// supermarket_client_name or supermarket_key, so writing a Profile built from
// its answers alone silently deletes them. UpdateProfile starts from what is
// already on disk, so an unmentioned key keeps its value by construction
// rather than by every caller remembering to carry it.
//
// A profile that does not exist yet starts empty, so this also serves
// create-or-update callers. Keys cinc does not model are preserved by
// WriteProfile regardless.
func UpdateProfile(path, name string, mutate func(*Profile) error) error {
	if name == "" {
		return fmt.Errorf("config: profile name is required")
	}
	existing := Profile{}
	if cfg, err := Load(path); err == nil {
		if p, ok := cfg.Profiles[name]; ok {
			existing = p
		}
	} else if !os.IsNotExist(errors.Unwrap(err)) && !os.IsNotExist(err) {
		// A malformed file is reported rather than silently overwritten.
		return err
	}
	if err := mutate(&existing); err != nil {
		return err
	}
	return WriteProfile(path, name, existing)
}

// WriteProfile creates or updates one profile in the credentials file at path.
// Other profiles are left untouched, and within the profile being written only
// the keys cinc manages are rewritten; anything else the file holds is carried
// across verbatim. Comments and original key ordering are not retained, because
// the file is re-encoded as TOML.
func WriteProfile(path, name string, p Profile) error {
	return WriteProfileWithExtras(path, name, p, nil)
}

// WriteProfileWithExtras is WriteProfile for a profile that also carries keys
// cinc does not model, copied from somewhere other than the file being
// written: first-run migration moves a knife profile's node_name,
// validation_key and the like across this way. Each extra key is written
// verbatim, replacing any value the file already has for it. A key cinc
// manages is ignored in extra, because p is the source of truth for those.
func WriteProfileWithExtras(path, name string, p Profile, extra map[string]any) error {
	if name == "" {
		return fmt.Errorf("config: profile name is required")
	}
	if err := p.ValidateIdentity(); err != nil {
		return err
	}
	// Decode into an untyped map so keys cinc does not model survive the
	// round trip rather than being silently dropped on re-encode.
	raw := map[string]map[string]any{}
	if _, err := os.Stat(path); err == nil {
		if _, err := toml.DecodeFile(path, &raw); err != nil {
			return fmt.Errorf("config: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("config: stat %s: %w", path, err)
	}

	profile := raw[name]
	if profile == nil {
		profile = map[string]any{}
	}
	for key, value := range extra {
		if !slices.Contains(managedKeys, key) {
			profile[key] = value
		}
	}
	// Write the cinc-canonical server URL key. Reads still accept
	// chef_server_url (cinc wins when both appear), so knife-shared files
	// keep loading unchanged.
	managed := map[string]string{
		"cinc_server_url":         profileServerURL(p),
		"chef_server_url":         "",
		"supermarket_site":        p.SupermarketSite,
		"client_name":             p.ClientName,
		"client_key":              p.KeyPath,
		"supermarket_client_name": p.SupermarketClientName,
		"supermarket_key":         p.SupermarketKey,
		"ssl_verify_mode":         p.SSLVerifyMode,
		"secret_file":             p.SecretFile,
		"trusted_certs_dir":       p.TrustedCertsDir,
	}
	// A profile that already carries chef_server_url is shared with chef
	// tools that read only that key, and chef-config knows nothing of
	// cinc_server_url. Retiring it would leave knife on its built-in
	// default server URL, so keep it pointing wherever cinc_server_url
	// points. Mirror on presence, not on value: when the rewrite has no
	// server URL to offer (a Supermarket-only profile, or a URL that did
	// not parse) the key is left exactly as it was rather than emptied,
	// which would delete it below. A profile without it stays
	// cinc-canonical only.
	_, shared := profile["chef_server_url"]

	for _, key := range managedKeys {
		if key == "chef_server_url" && shared {
			if url := managed["cinc_server_url"]; url != "" {
				profile[key] = url
			}
			continue
		}
		if value := managed[key]; value != "" {
			profile[key] = value
		} else {
			delete(profile, key)
		}
	}
	raw[name] = profile

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("config: create credentials directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	// The mode above applies only to a new file. The file names private
	// keys and data bag secrets, so tighten an existing one too, before
	// anything is written into it; knife setups often leave it 0644.
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("config: restrict %s to its owner: %w", path, err)
	}
	if err := toml.NewEncoder(f).Encode(raw); err != nil {
		_ = f.Close()
		return fmt.Errorf("config: encode %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("config: close %s: %w", path, err)
	}
	return nil
}

// NewProfile builds a Profile from user-supplied configure values.
func NewProfile(serverURL, clientName, clientKey, sslVerifyMode, supermarketSite string) (Profile, error) {
	var server, org string
	if serverURL != "" {
		parsedServer, parsedOrg, err := cinc.ParseServerURL(serverURL)
		if err == nil {
			server = parsedServer
			org = parsedOrg
		} else {
			if supermarketSite != "" {
				return Profile{}, err
			}
			if err := ValidateSiteURL(serverURL); err != nil {
				return Profile{}, err
			}
			supermarketSite = serverURL
		}
	}
	p := Profile{
		ServerURL:       server,
		Org:             org,
		SupermarketSite: supermarketSite,
		ClientName:      clientName,
		KeyPath:         clientKey,
		SSLVerifyMode:   sslVerifyMode,
	}
	if err := p.ValidateIdentity(); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// profileServerURL is the server URL to write for p. A URL that did not
// parse into server and organization is written back as it was, so
// rewriting a profile never quietly deletes a URL the user typed; config
// validate reports what is wrong with it instead.
func profileServerURL(p Profile) string {
	if p.ServerURL == "" || p.Org == "" {
		return p.RawServerURL
	}
	return strings.TrimRight(p.ServerURL, "/") + "/organizations/" + p.Org
}

// resolveProfile turns a raw on-disk entry into a usable Profile, splitting
// the configured server URL into a server URL and an organization name.
func resolveProfile(rp rawProfile) (Profile, error) {
	p := Profile{
		SupermarketSite:       rp.SupermarketSite,
		ClientName:            rp.ClientName,
		KeyPath:               rp.ClientKey,
		SupermarketClientName: rp.SupermarketClientName,
		SupermarketKey:        rp.SupermarketKey,
		SSLVerifyMode:         rp.SSLVerifyMode,
		SecretFile:            rp.SecretFile,
		TrustedCertsDir:       rp.TrustedCertsDir,
	}
	if raw := rp.serverURL(); raw != "" {
		// Preserve the raw URL even when it doesn't parse, so validation can
		// report the precise problem instead of the whole load failing. A
		// malformed URL leaves ServerURL/Org empty; Profile.Validate surfaces
		// the parse error for callers that need a usable connection.
		p.RawServerURL = raw
		if server, org, err := cinc.ParseServerURL(raw); err == nil {
			p.ServerURL = server
			p.Org = org
		}
	}
	return p, nil
}

func ValidateSiteURL(raw string) error {
	u, parseErr := url.Parse(raw)
	if parseErr != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("invalid site URL %q", raw)
	}
	return nil
}

// Profile resolves a connection profile by name. An empty name selects
// the profile named by $CINC_PROFILE, then $CHEF_PROFILE, falling back to
// "default". When both env vars are set CINC_PROFILE wins.
func (c *Config) Profile(name string) (Profile, error) {
	if name == "" {
		name = EnvProfile()
	}
	if name == "" {
		name = "default"
	}
	p, ok := c.Profiles[name]
	if !ok {
		return Profile{}, fmt.Errorf("config: unknown profile %q", name)
	}
	return p, nil
}

// EnvProfile returns the profile name selected by environment variables,
// preferring CINC_PROFILE over CHEF_PROFILE, or "" when neither is set.
func EnvProfile() string {
	if v := os.Getenv("CINC_PROFILE"); v != "" {
		return v
	}
	return os.Getenv("CHEF_PROFILE")
}

func validateProfile(name string, p Profile) []ValidationIssue {
	var issues []ValidationIssue
	add := func(field, message string) {
		issues = append(issues, ValidationIssue{Profile: name, Field: field, Message: message})
	}
	if p.ClientName == "" {
		add("client_name", "client_name is required")
	}
	if p.KeyPath == "" {
		add("client_key", "client_key is required")
	}
	switch {
	case p.ServerURL == "" && p.Org == "" && p.RawServerURL == "" && p.SupermarketSite == "":
		add("endpoint", "configure cinc_server_url, chef_server_url, or supermarket_site")
	case p.RawServerURL != "" && p.ServerURL == "":
		// A server URL was set but couldn't be parsed into server + org.
		add("cinc_server_url", "server URL must include /organizations/<org>")
	case (p.ServerURL == "") != (p.Org == ""):
		add("cinc_server_url", "server URL must include /organizations/<org>")
	}
	if p.SupermarketSite != "" {
		if err := ValidateSiteURL(p.SupermarketSite); err != nil {
			add("supermarket_site", err.Error())
		}
	}
	if p.SSLVerifyMode != "" && p.SSLVerifyMode != ":verify_peer" && p.SSLVerifyMode != ":verify_none" {
		add("ssl_verify_mode", "ssl_verify_mode must be :verify_peer or :verify_none")
	}
	return issues
}
