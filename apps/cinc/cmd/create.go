package cmd

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	cinc "github.com/cinc-project/cinc-api"
	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/cli/components"
	"github.com/cinc-project/cinc-cli/cli/config"
	"github.com/cinc-project/cinc-cli/cli/progname"
	"github.com/cinc-project/cinc-cli/cli/supermarket"
)

// newConfigCreateCmd builds `cinc config create`, the local workstation credentials
// setup command. It writes TOML credentials only; Cinc does not emit Ruby
// config.rb/client.rb files.
func newConfigCreateCmd() *cobra.Command {
	var (
		serverURLs      [len(serverURLFlags)]string
		supermarketSite string
		clientName      string
		clientKey       string
		sslVerifyMode   string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create or update a local credentials profile",
		Example: `Create or update the default credentials profile interactively.
cinc config create`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			serverURL, err := serverURLFlag(serverURLs)
			if err != nil {
				return err
			}
			cfgPath, err := configPathForCommand(cmd)
			if err != nil {
				return err
			}
			profileName := profileNameForCommand(cmd)
			profileNameExplicit := false
			replaceFile := false

			if !configureOptionsChanged(cmd) {
				defaultProfileName := configureProfileNameForCommand(cmd)
				answers, err := promptConfigure(cmd, configureDefaults{
					ConfigPath:      cfgPath,
					ProfileName:     defaultProfileName,
					SupermarketSite: supermarket.DefaultSite,
					ClientName:      defaultClientName(),
					ChefServerURL:   serverURL,
					SSLVerifyMode:   sslVerifyMode,
				})
				if err != nil {
					return err
				}
				cfgPath = answers.ConfigPath
				profileName = answers.ProfileName
				supermarketSite = answers.SupermarketSite
				clientName = answers.ClientName
				clientKey = answers.ClientKey
				serverURL = answers.ChefServerURL
				sslVerifyMode = answers.SSLVerifyMode
				// A name typed at the profile-name prompt is as much the
				// user's choice as one picked from the existing-file menu,
				// so it too is kept rather than renamed to [supermarket].
				profileNameExplicit = profileNameExplicit || answers.ProfileNameExplicit ||
					answers.ProfileName != defaultProfileName
				replaceFile = answers.ReplaceFile
			} else if serverURL == "" && supermarketSite == "" {
				supermarketSite = supermarket.DefaultSite
			}
			if err := checkServerURLFlag(serverURL); err != nil {
				return err
			}
			// A profile that only talks to the public Supermarket is named
			// [supermarket], where the supermarket commands look first. One
			// with a Cinc Server keeps its name, whatever Supermarket it
			// also names.
			if !configureProfileExplicit(cmd) && !profileNameExplicit && !replaceFile &&
				!isServerURL(serverURL) && isPublicSupermarketProfile(serverURL, supermarketSite) {
				profileName = "supermarket"
			}

			if clientName == "" {
				return fmt.Errorf("cinc: --client-name is required")
			}
			if clientKey == "" {
				return fmt.Errorf("cinc: --client-key is required")
			}
			cfgPath, err = config.ExpandHome(cfgPath)
			if err != nil {
				return err
			}
			clientKey, err = config.ExpandHome(clientKey)
			if err != nil {
				return err
			}
			if replaceFile {
				if err := clearCredentials(cfgPath); err != nil {
					return err
				}
			}
			// UpdateProfile starts from whatever is already on disk, so
			// keys this command never asks about (secret_file,
			// trusted_certs_dir and the supermarket identity overrides) survive an update without
			// being threaded through the prompts.
			err = config.UpdateProfile(cfgPath, profileName, func(p *config.Profile) error {
				updated, err := config.NewProfile(serverURL, clientName, clientKey, sslVerifyMode, supermarketSite)
				if err != nil {
					return err
				}
				p.ServerURL = updated.ServerURL
				p.Org = updated.Org
				p.RawServerURL = updated.RawServerURL
				p.SupermarketSite = updated.SupermarketSite
				p.ClientName = updated.ClientName
				p.KeyPath = updated.KeyPath
				p.SSLVerifyMode = updated.SSLVerifyMode
				return nil
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintln(out)
			fmt.Fprintf(out, "Wrote credentials profile %q to %s\n", profileName, cfgPath)
			fmt.Fprintln(out)
			fmt.Fprintln(out, "Cinc CLI is now configured and you're ready to go!")
			return nil
		},
	}
	// Three spellings of one setting, each with its own variable so
	// serverURLFlag can tell when they disagree.
	cmd.Flags().StringVar(&serverURLs[0], "server-url", "", "Cinc Server URL including /organizations/<org>")
	cmd.Flags().StringVar(&serverURLs[1], "chef-server-url", "", "Cinc Server URL including /organizations/<org>")
	cmd.Flags().StringVar(&serverURLs[2], "cinc-server-url", "", "Cinc Server URL including /organizations/<org>")
	cmd.Flags().StringVar(&supermarketSite, "supermarket-site", "", "Chef Supermarket URL for cookbook uploads")
	cmd.Flags().StringVar(&clientName, "client-name", "", "client name used to sign API requests")
	cmd.Flags().StringVar(&clientKey, "client-key", "", "path to the PEM private key for the client")
	cmd.Flags().StringVar(&sslVerifyMode, "ssl-verify-mode", "", "optional SSL verify mode such as :verify_peer or :verify_none")
	return cmd
}

type configureDefaults struct {
	ConfigPath      string
	ProfileName     string
	SupermarketSite string
	ClientName      string
	ClientKey       string
	ChefServerURL   string
	SSLVerifyMode   string
	// ProfileNameExplicit is true once the interactive flow has captured
	// a concrete profile name from the user (by typing it for the
	// add-new branch or picking it from the existing-profile list for
	// the update branch). When set, the auto-rename-to-"supermarket"
	// behaviour in RunE is suppressed and the redundant "Profile name"
	// prompt is skipped so the user's choice wins.
	ProfileNameExplicit bool
	// ReplaceFile signals that the user asked to start fresh, so the
	// existing credentials file should be removed before the new
	// profile is written rather than merged into.
	ReplaceFile bool
}

func promptConfigure(cmd *cobra.Command, defaults configureDefaults) (configureDefaults, error) {
	reader := bufio.NewReader(cmd.InOrStdin())
	out := cmd.OutOrStdout()
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Let's set up your Cinc credentials — press Enter on any prompt to accept the default.")
	fmt.Fprintln(out)

	var err error
	defaults.ConfigPath, err = components.PromptWithDefault(reader, out, "Credentials file location", defaults.ConfigPath)
	if err != nil {
		return configureDefaults{}, err
	}

	if resolvedPath, expandErr := config.ExpandHome(defaults.ConfigPath); expandErr == nil {
		if existing, loadErr := config.Load(resolvedPath); loadErr == nil && len(existing.Profiles) > 0 {
			adjusted, err := promptExistingFileAction(reader, out, resolvedPath, existing, defaults)
			if err != nil {
				return configureDefaults{}, err
			}
			defaults = adjusted
		}
	}

	if !defaults.ProfileNameExplicit {
		defaults.ProfileName, err = components.PromptWithDefault(reader, out, "Profile name", defaults.ProfileName)
		if err != nil {
			return configureDefaults{}, err
		}
	}
	defaults.SupermarketSite, err = components.PromptWithDefault(reader, out, "Supermarket site", defaults.SupermarketSite)
	if err != nil {
		return configureDefaults{}, err
	}
	defaults.ClientName, err = components.PromptWithDefault(reader, out, "Client name", defaults.ClientName)
	if err != nil {
		return configureDefaults{}, err
	}
	if defaults.ClientKey == "" {
		defaults.ClientKey = defaultClientKey(defaults.ClientName)
	}
	defaults.ClientKey, err = components.PromptWithDefault(reader, out, "Client key path", defaults.ClientKey)
	if err != nil {
		return configureDefaults{}, err
	}
	defaultHost, defaultOrg := splitChefServerURL(defaults.ChefServerURL)
	serverHost, err := components.PromptWithDefault(reader, out, "Chef server host (optional, e.g. chef.example.com)", defaultHost)
	if err != nil {
		return configureDefaults{}, err
	}
	if serverHost != "" {
		// The answer may be a whole URL rather than a bare host: keep its
		// scheme and port, and offer its organization as the default.
		base, urlOrg := serverBaseURL(serverHost)
		if urlOrg != "" && serverHost != defaultHost {
			defaultOrg = urlOrg
		}
		serverOrg, err := components.PromptWithDefault(reader, out, "Chef server organization", defaultOrg)
		if err != nil {
			return configureDefaults{}, err
		}
		if serverOrg != "" {
			defaults.ChefServerURL = base + "/organizations/" + serverOrg
		} else {
			defaults.ChefServerURL = ""
		}
	} else {
		defaults.ChefServerURL = ""
	}
	if defaults.SSLVerifyMode == "" {
		defaults.SSLVerifyMode = ":verify_peer"
	}
	defaults.SSLVerifyMode, err = components.PromptWithDefault(reader, out, "SSL verify mode", defaults.SSLVerifyMode)
	if err != nil {
		return configureDefaults{}, err
	}
	return defaults, nil
}

// promptExistingFileAction handles the "we found existing credentials"
// branch of the interactive configure flow. It asks the user whether to
// add, update, or replace, then returns adjusted defaults whose
// ProfileName reflects the chosen profile.
func promptExistingFileAction(reader *bufio.Reader, out io.Writer, path string, existing *config.Config, defaults configureDefaults) (configureDefaults, error) {
	names := sortedProfileNames(existing)
	fmt.Fprintln(out)
	fmt.Fprintf(out, "You already have credentials at %s with profiles:\n", path)
	for _, name := range names {
		fmt.Fprintf(out, "  - %s\n", name)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "What would you like to do?")
	fmt.Fprintln(out, "  1) Add a new profile")
	fmt.Fprintln(out, "  2) Update an existing profile")
	fmt.Fprintln(out, "  3) Replace the credentials file")
	fmt.Fprintln(out)

	for {
		choice, err := components.PromptWithDefault(reader, out, "Choice", "1")
		if err != nil {
			return configureDefaults{}, err
		}
		switch choice {
		case "", "1":
			for {
				name, err := promptNoDefault(reader, out, "New profile name")
				if err != nil {
					return configureDefaults{}, err
				}
				if name == "" {
					fmt.Fprintln(out, "Profile name can't be empty.")
					continue
				}
				if _, collides := existing.Profiles[name]; collides {
					switchToUpdate, err := promptCollisionOffer(reader, out, name)
					if err != nil {
						return configureDefaults{}, err
					}
					if switchToUpdate {
						return applyExistingProfileDefaults(defaults, existing, name), nil
					}
					continue
				}
				defaults.ProfileName = name
				defaults.ProfileNameExplicit = true
				return defaults, nil
			}
		case "2":
			chosen, err := promptProfilePicker(reader, out, names)
			if err != nil {
				return configureDefaults{}, err
			}
			return applyExistingProfileDefaults(defaults, existing, chosen), nil
		case "3":
			confirmed, err := promptReplaceConfirmation(reader, out, names)
			if err != nil {
				return configureDefaults{}, err
			}
			if confirmed {
				defaults.ReplaceFile = true
				return defaults, nil
			}
			fmt.Fprintln(out)
		default:
			fmt.Fprintf(out, "Please choose 1, 2, or 3.\n")
		}
	}
}

// promptCollisionOffer handles the case where the user typed a new
// profile name that already exists, by offering to switch into the
// update flow for that profile instead.
func promptCollisionOffer(reader *bufio.Reader, out io.Writer, name string) (bool, error) {
	fmt.Fprintln(out)
	fmt.Fprintf(out, "A profile named %q already exists.\n", name)
	answer, err := promptNoDefault(reader, out, "Update it instead? [Y/n]")
	if err != nil {
		return false, err
	}
	switch strings.ToLower(answer) {
	case "", "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// promptReplaceConfirmation warns the user which profiles are about to
// be deleted and asks for an explicit y/N confirmation before nuking
// the file.
func promptReplaceConfirmation(reader *bufio.Reader, out io.Writer, names []string) (bool, error) {
	fmt.Fprintln(out)
	fmt.Fprintf(out, "This will delete profiles: %s.\n", strings.Join(names, ", "))
	answer, err := promptNoDefault(reader, out, "Replace the file? [y/N]")
	if err != nil {
		return false, err
	}
	switch strings.ToLower(answer) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// promptProfilePicker shows the existing profiles as a numbered list and
// returns the name of the one the user chose.
func promptProfilePicker(reader *bufio.Reader, out io.Writer, names []string) (string, error) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Which profile would you like to update?")
	for i, name := range names {
		fmt.Fprintf(out, "  %d) %s\n", i+1, name)
	}
	fmt.Fprintln(out)
	for {
		choice, err := components.PromptWithDefault(reader, out, "Choice", "1")
		if err != nil {
			return "", err
		}
		if choice == "" {
			choice = "1"
		}
		idx, err := strconv.Atoi(choice)
		if err != nil || idx < 1 || idx > len(names) {
			fmt.Fprintf(out, "Please choose a number between 1 and %d.\n", len(names))
			continue
		}
		return names[idx-1], nil
	}
}

// applyExistingProfileDefaults seeds the interactive defaults from a
// profile the user picked for update, so each subsequent prompt offers
// the current value as its default.
func applyExistingProfileDefaults(defaults configureDefaults, existing *config.Config, name string) configureDefaults {
	p := existing.Profiles[name]
	defaults.ProfileName = name
	defaults.ProfileNameExplicit = true
	defaults.SupermarketSite = p.SupermarketSite
	defaults.ClientName = p.ClientName
	defaults.ClientKey = p.KeyPath
	defaults.SSLVerifyMode = p.SSLVerifyMode
	if p.ServerURL != "" && p.Org != "" {
		defaults.ChefServerURL = strings.TrimRight(p.ServerURL, "/") + "/organizations/" + p.Org
	} else {
		defaults.ChefServerURL = ""
	}
	return defaults
}

func sortedProfileNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Profiles))
	for name := range cfg.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// splitChefServerURL returns the host and organization name from a full
// server URL of the form https://host/organizations/<org>, as the host
// prompt offers them. An https URL gives the bare host; any other scheme
// is kept (http://host:port), because serverBaseURL would otherwise turn
// the answer back into https. It returns empty strings when the URL is
// empty or doesn't parse.
func splitChefServerURL(raw string) (host, org string) {
	if raw == "" {
		return "", ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", ""
	}
	host = u.Host
	if u.Scheme != "" && u.Scheme != "https" {
		host = u.Scheme + "://" + u.Host
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 2 && parts[0] == "organizations" {
		return host, parts[1]
	}
	return host, ""
}

// serverBaseURL turns the host prompt's answer into the server's base URL
// (scheme://host[:port], no trailing slash). A bare host, with or without a
// port, means https. An answer with a scheme is a URL the user pasted: its
// scheme and port are kept, and an /organizations/<org> path is split off
// and returned as org.
func serverBaseURL(answer string) (base, org string) {
	if !strings.Contains(answer, "://") {
		return "https://" + strings.TrimRight(answer, "/"), ""
	}
	u, err := url.Parse(answer)
	if err != nil || u.Host == "" {
		return strings.TrimRight(answer, "/"), ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 2 && parts[0] == "organizations" {
		org = parts[1]
	}
	return u.Scheme + "://" + u.Host, org
}

// errStdinExhausted is returned by promptNoDefault when there is no input
// left to read. The prompts that have no sensible default re-ask until they
// get an answer, so without this the flow would spin forever against a
// closed stdin (`cinc config create < /dev/null`, or a CI run).
func errStdinExhausted() error {
	return fmt.Errorf("we ran out of input while waiting for an answer. `%s config create` needs an interactive terminal; to configure without prompts, pass --client-name, --client-key, and --server-url", progname.Get())
}

// promptNoDefault asks for an answer that has no default. An empty line is a
// valid (if usually rejected) answer, so it is reported as one; only a reader
// with nothing left to give yields errStdinExhausted.
func promptNoDefault(reader *bufio.Reader, out io.Writer, label string) (string, error) {
	fmt.Fprintf(out, "%s: ", label)
	answer, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	if err == io.EOF && answer == "" {
		return "", errStdinExhausted()
	}
	return strings.TrimSpace(answer), nil
}

func configureOptionsChanged(cmd *cobra.Command) bool {
	for _, name := range []string{
		"server-url",
		"chef-server-url",
		"cinc-server-url",
		"supermarket-site",
		"client-name",
		"client-key",
		"ssl-verify-mode",
	} {
		if cmd.Flags().Changed(name) {
			return true
		}
	}
	return false
}

func configureProfileNameForCommand(cmd *cobra.Command) string {
	if configureProfileExplicit(cmd) {
		return profileNameForCommand(cmd)
	}
	return "default"
}

func configureProfileExplicit(cmd *cobra.Command) bool {
	return explicitProfile(cmd) != ""
}

// serverURLFlags are config create's spellings of the server URL flag,
// kept for knife users (--chef-server-url) and for symmetry with the
// cinc_server_url key (--cinc-server-url).
var serverURLFlags = [...]string{"server-url", "chef-server-url", "cinc-server-url"}

// serverURLFlag returns the server URL config create was given under any
// of its spellings. Two spellings naming different URLs are refused rather
// than resolved by flag order: they are one setting, and a silent pick
// writes a profile for a server the user may not have meant.
func serverURLFlag(values [len(serverURLFlags)]string) (string, error) {
	chosen, from := "", ""
	for i, v := range values {
		switch {
		case v == "":
		case chosen == "":
			chosen, from = v, serverURLFlags[i]
		case v != chosen:
			return "", fmt.Errorf("--%s and --%s name different servers (%s and %s). They set the same thing, so pass just one", from, serverURLFlags[i], chosen, v)
		}
	}
	return chosen, nil
}

// isServerURL reports whether raw is a Cinc Server URL, organization and all.
func isServerURL(raw string) bool {
	if raw == "" {
		return false
	}
	_, _, err := cinc.ParseServerURL(raw)
	return err == nil
}

// checkServerURLFlag rejects a --server-url that names no organization. The
// public Supermarket's URL is the one exception, kept so `--server-url
// https://supermarket.chef.io` still sets up a Supermarket-only profile.
// Anything else without /organizations/<org> is almost certainly a server
// URL with the org left off; filing it away as a Supermarket site instead
// would write a profile no server command can use.
func checkServerURLFlag(raw string) error {
	if raw == "" || isServerURL(raw) || strings.TrimRight(raw, "/") == supermarket.DefaultSite {
		return nil
	}
	return fmt.Errorf("the server URL %s doesn't include /organizations/<org>. Add your organization (for example %s/organizations/acme), or pass a Supermarket with --supermarket-site instead", raw, strings.TrimRight(raw, "/"))
}

func isPublicSupermarketProfile(serverURL, supermarketSite string) bool {
	return strings.TrimRight(serverURL, "/") == supermarket.DefaultSite ||
		strings.TrimRight(supermarketSite, "/") == supermarket.DefaultSite
}

func defaultClientName() string {
	if name := os.Getenv("USER"); name != "" {
		return name
	}
	return ""
}

func defaultClientKey(clientName string) string {
	if clientName == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cinc", clientName+".pem")
}

// clearCredentials empties the credentials file at path so a replace
// starts over. It truncates rather than deletes: truncating follows a
// symlink, so a file kept in a dotfiles checkout stays linked, and the
// file keeps its permissions. A file that does not exist yet is fine.
func clearCredentials(path string) error {
	if err := os.Truncate(path, 0); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cinc: clear old credentials: %w", err)
	}
	return nil
}

func configPathForCommand(cmd *cobra.Command) (string, error) {
	cfgPath, _ := cmd.Flags().GetString("config")
	if cfgPath != "" {
		return cfgPath, nil
	}
	return config.DefaultPath()
}

func profileNameForCommand(cmd *cobra.Command) string {
	if name := explicitProfile(cmd); name != "" {
		return name
	}
	return "default"
}

// explicitProfile returns the profile the user pinned with --profile or,
// failing that, $CINC_PROFILE then $CHEF_PROFILE, or "" when none is set.
func explicitProfile(cmd *cobra.Command) string {
	if name, _ := cmd.Flags().GetString("profile"); name != "" {
		return name
	}
	return config.EnvProfile()
}
