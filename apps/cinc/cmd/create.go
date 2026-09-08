package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/cli/config"
	"github.com/cinc-project/cinc-cli/cli/supermarket"
)

// newConfigCreateCmd builds `cinc config create`, the local workstation credentials
// setup command. It writes TOML credentials only; Cinc does not emit Ruby
// config.rb/client.rb files.
func newConfigCreateCmd() *cobra.Command {
	var (
		serverURL       string
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
			cfgPath, err := configPathForCommand(cmd)
			if err != nil {
				return err
			}
			profileName := profileNameForCommand(cmd)
			profileNameExplicit := false
			replaceFile := false

			if !configureOptionsChanged(cmd) {
				answers, err := promptConfigure(cmd, configureDefaults{
					ConfigPath:      cfgPath,
					ProfileName:     configureProfileNameForCommand(cmd),
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
				profileNameExplicit = profileNameExplicit || answers.ProfileNameExplicit
				replaceFile = answers.ReplaceFile
			} else if serverURL == "" && supermarketSite == "" {
				supermarketSite = supermarket.DefaultSite
			}
			if !configureProfileExplicit(cmd) && !profileNameExplicit && !replaceFile && isPublicSupermarketProfile(serverURL, supermarketSite) {
				profileName = "supermarket"
			}

			if clientName == "" {
				return fmt.Errorf("cinc: --client-name is required")
			}
			if clientKey == "" {
				return fmt.Errorf("cinc: --client-key is required")
			}
			cfgPath, err = expandHome(cfgPath)
			if err != nil {
				return err
			}
			clientKey, err = expandHome(clientKey)
			if err != nil {
				return err
			}
			profile, err := config.NewProfile(serverURL, clientName, clientKey, sslVerifyMode, supermarketSite)
			if err != nil {
				return err
			}
			if replaceFile {
				if err := os.Remove(cfgPath); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("cinc: remove old credentials: %w", err)
				}
			}
			if err := config.WriteProfile(cfgPath, profileName, profile); err != nil {
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
	cmd.Flags().StringVar(&serverURL, "server-url", "", "Cinc Server URL including /organizations/<org>")
	cmd.Flags().StringVar(&serverURL, "chef-server-url", "", "Cinc Server URL including /organizations/<org>")
	cmd.Flags().StringVar(&serverURL, "cinc-server-url", "", "Cinc Server URL including /organizations/<org>")
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
	defaults.ConfigPath, err = promptWithDefault(reader, out, "Credentials file location", defaults.ConfigPath)
	if err != nil {
		return configureDefaults{}, err
	}

	if resolvedPath, expandErr := expandHome(defaults.ConfigPath); expandErr == nil {
		if existing, loadErr := config.Load(resolvedPath); loadErr == nil && len(existing.Profiles) > 0 {
			adjusted, err := promptExistingFileAction(reader, out, resolvedPath, existing, defaults)
			if err != nil {
				return configureDefaults{}, err
			}
			defaults = adjusted
		}
	}

	if !defaults.ProfileNameExplicit {
		defaults.ProfileName, err = promptWithDefault(reader, out, "Profile name", defaults.ProfileName)
		if err != nil {
			return configureDefaults{}, err
		}
	}
	defaults.SupermarketSite, err = promptWithDefault(reader, out, "Supermarket site", defaults.SupermarketSite)
	if err != nil {
		return configureDefaults{}, err
	}
	defaults.ClientName, err = promptWithDefault(reader, out, "Client name", defaults.ClientName)
	if err != nil {
		return configureDefaults{}, err
	}
	if defaults.ClientKey == "" {
		defaults.ClientKey = defaultClientKey(defaults.ClientName)
	}
	defaults.ClientKey, err = promptWithDefault(reader, out, "Client key path", defaults.ClientKey)
	if err != nil {
		return configureDefaults{}, err
	}
	defaultHost, defaultOrg := splitChefServerURL(defaults.ChefServerURL)
	serverHost, err := promptWithDefault(reader, out, "Chef server host (optional, e.g. chef.example.com)", defaultHost)
	if err != nil {
		return configureDefaults{}, err
	}
	if serverHost != "" {
		serverOrg, err := promptWithDefault(reader, out, "Chef server organization", defaultOrg)
		if err != nil {
			return configureDefaults{}, err
		}
		if serverOrg != "" {
			defaults.ChefServerURL = fmt.Sprintf("https://%s/organizations/%s", serverHost, serverOrg)
		} else {
			defaults.ChefServerURL = ""
		}
	} else {
		defaults.ChefServerURL = ""
	}
	if defaults.SSLVerifyMode == "" {
		defaults.SSLVerifyMode = ":verify_peer"
	}
	defaults.SSLVerifyMode, err = promptWithDefault(reader, out, "SSL verify mode", defaults.SSLVerifyMode)
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
		choice, err := promptWithDefault(reader, out, "Choice", "1")
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
		choice, err := promptWithDefault(reader, out, "Choice", "1")
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

// splitChefServerURL returns the bare host and organization name from a
// full server URL of the form https://host/organizations/<org>. It
// returns empty strings when the URL is empty or doesn't parse.
func splitChefServerURL(raw string) (host, org string) {
	if raw == "" {
		return "", ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 2 && parts[0] == "organizations" {
		return u.Host, parts[1]
	}
	return u.Host, ""
}

// errStdinExhausted is returned by promptNoDefault when there is no input
// left to read. The prompts that have no sensible default re-ask until they
// get an answer, so without this the flow would spin forever against a
// closed stdin (`cinc config create < /dev/null`, or a CI run).
var errStdinExhausted = errors.New("we ran out of input while waiting for an answer. `cinc config create` needs an interactive terminal; to configure without prompts, pass --client-name, --client-key, and --server-url")

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
		return "", errStdinExhausted
	}
	return strings.TrimSpace(answer), nil
}

func promptWithDefault(reader *bufio.Reader, out io.Writer, label, defaultValue string) (string, error) {
	fmt.Fprintf(out, "%s [%s]: ", label, defaultValue)
	answer, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return defaultValue, nil
	}
	return answer, nil
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
	if profileName, _ := cmd.Flags().GetString("profile"); profileName != "" {
		return true
	}
	if os.Getenv("CINC_PROFILE") != "" {
		return true
	}
	if os.Getenv("CHEF_PROFILE") != "" {
		return true
	}
	return false
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

func expandHome(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cinc: locate home directory: %w", err)
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return path, nil
}

func configPathForCommand(cmd *cobra.Command) (string, error) {
	cfgPath, _ := cmd.Flags().GetString("config")
	if cfgPath != "" {
		return cfgPath, nil
	}
	return config.DefaultPath()
}

func profileNameForCommand(cmd *cobra.Command) string {
	profileName, _ := cmd.Flags().GetString("profile")
	if profileName != "" {
		return profileName
	}
	if profileName = os.Getenv("CINC_PROFILE"); profileName != "" {
		return profileName
	}
	if profileName = os.Getenv("CHEF_PROFILE"); profileName != "" {
		return profileName
	}
	return "default"
}
