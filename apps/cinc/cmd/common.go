package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	cinc "github.com/cinc-project/cinc-api"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/cli/client"
	"github.com/cinc-project/cinc-cli/cli/config"
	"github.com/cinc-project/cinc-cli/cli/printer"
	"github.com/cinc-project/cinc-cli/cli/setup"
	"github.com/cinc-project/cinc-cli/cli/supermarket"
)

// errFirstRunCompleted is a sentinel returned by loadCredentials after
// the first-run flow has written the credentials file. It signals the
// caller (and ultimately Execute) that the user has been welcomed and
// the invocation should exit cleanly without running the original
// server-touching command, so a fresh user isn't surprised by their
// `cinc node list` running against a server they just typed in.
var errFirstRunCompleted = errors.New("cinc: first-run setup completed")

// errAlreadyReported marks an error whose details a command has already
// printed for the user (e.g. `config validate`'s per-issue list). Execute
// returns it for a non-zero exit but does not print a second generic
// "Error: ..." line.
var errAlreadyReported = errors.New("cinc: already reported")

// migrateChef is the function used to migrate ~/.chef/credentials when the
// default cinc credentials file is missing. It is a package-level variable
// so tests can swap in a fake.
var migrateChef = setup.MigrateChef

// runFirstRunConfigure interactively configures a fresh credentials
// profile, the same way `cinc config create` does. It is a package-level
// variable so tests can swap in a fake.
var runFirstRunConfigure = realRunFirstRunConfigure

// stdinIsTTY reports whether os.Stdin is connected to an interactive
// terminal. It uses the TCGETS ioctl via go-isatty so the answer is
// the same whatever opaque file type the shell hands us — a regular
// pty, a tmux/screen-allocated pty, or a Cygwin-style mintty terminal.
// Tests swap this var directly.
var stdinIsTTY = func() bool {
	fd := os.Stdin.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// boldEnabled reports whether ANSI bold styling should be written to w.
// It honors the NO_COLOR convention and only styles real terminals, so
// piped output and test buffers stay plain.
func boldEnabled(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

// resolveFormat reads and validates the --format flag.
func resolveFormat(cmd *cobra.Command) (printer.Format, error) {
	name, _ := cmd.Flags().GetString("format")
	return printer.ParseFormat(name)
}

// resolveSecret returns the raw bytes of the encrypted data bag secret,
// used by the `cinc databag secret` commands. Resolution stops at the
// first hit, in order:
//
//  1. the --secret literal flag (its bytes verbatim),
//  2. the --secret-file flag (the file's contents),
//  3. $CINC_SECRET_FILE, then $CHEF_SECRET_FILE (cinc wins),
//  4. the resolved profile's secret_file key.
//
// The bytes are returned untouched — the cinc-api codec derives the AES
// key itself, and a secret file is never trimmed because Chef treats the
// whole file as the secret. --secret and --secret-file are mutually
// exclusive.
func resolveSecret(cmd *cobra.Command, profile config.Profile) ([]byte, error) {
	literal, _ := cmd.Flags().GetString("secret")
	file, _ := cmd.Flags().GetString("secret-file")
	if literal != "" && file != "" {
		return nil, errors.New("can't use --secret and --secret-file together — pick one")
	}
	if literal != "" {
		return []byte(literal), nil
	}
	if file != "" {
		return readSecretFile(file)
	}
	if env := os.Getenv("CINC_SECRET_FILE"); env != "" {
		return readSecretFile(env)
	}
	if env := os.Getenv("CHEF_SECRET_FILE"); env != "" {
		return readSecretFile(env)
	}
	if profile.SecretFile != "" {
		return readSecretFile(profile.SecretFile)
	}
	return nil, errors.New("we need an encrypted data bag secret but couldn't find one. Pass --secret-file <path> (or --secret <literal>), set $CINC_SECRET_FILE, or add a secret_file key to your credentials profile.")
}

// readSecretFile reads a secret file's full contents as the raw secret
// bytes, wrapping a read error in a conversational message.
func readSecretFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("can't read the data bag secret at %s: %w", path, err)
	}
	return data, nil
}

// resolveClient builds a server client from the --config and --profile
// flags. An empty --config falls back to the default config path.
func resolveClient(cmd *cobra.Command) (*cinc.Client, error) {
	profile, err := resolveProfile(cmd)
	if err != nil {
		return nil, err
	}
	c, err := client.New(profile)
	if err != nil {
		return nil, friendlyKeyFileError(cmd, profile, err)
	}
	return c, nil
}

// friendlyKeyFileError rewrites a "client key file missing/unreadable"
// error into a conversational message that names both the key path
// and the credentials file where client_key is configured. Other
// errors pass through unchanged.
func friendlyKeyFileError(cmd *cobra.Command, p config.Profile, err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("can't find your client key at %s — it's set as client_key in %s. Create the key file, or update client_key to point at the right path.", p.KeyPath, resolveConfigPath(cmd))
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("can't read your client key at %s — it's set as client_key in %s. Check the file's permissions; cinc needs to read it to sign requests.", p.KeyPath, resolveConfigPath(cmd))
	}
	return err
}

// resolveConfigPath returns the credentials file path the command is
// pointed at, matching loadCredentials' behavior so error messages
// name the same path the loader used.
func resolveConfigPath(cmd *cobra.Command) string {
	if p, _ := cmd.Flags().GetString("config"); p != "" {
		return p
	}
	if p, err := config.DefaultPath(); err == nil {
		return p
	}
	return ""
}

// resolveProfile reads the selected profile from the --config and --profile
// flags without constructing a server client.
func resolveProfile(cmd *cobra.Command) (config.Profile, error) {
	cfg, err := loadCredentials(cmd)
	if err != nil {
		return config.Profile{}, err
	}
	profileName, _ := cmd.Flags().GetString("profile")
	return cfg.Profile(profileName)
}

// resolveSupermarketProfile prefers the explicit --profile or environment
// profile when present. Otherwise it uses the conventional [supermarket]
// profile, falling back to [default] for existing credentials files.
func resolveSupermarketProfile(cmd *cobra.Command) (config.Profile, error) {
	cfg, err := loadCredentials(cmd)
	if err != nil {
		return config.Profile{}, err
	}
	return selectSupermarketProfile(cmd, cfg)
}

// resolveSupermarketSite returns the Supermarket a command should talk to:
// the --supermarket-site flag, then the resolved profile's supermarket_site,
// then "" for the caller's default (the public Supermarket).
//
// The read-only Supermarket commands need no credentials, so this never
// triggers the first-run flow and never fails: an absent or unreadable
// credentials file simply means no configured preference. Without this, a
// private supermarket_site was honored by `supermarket share` and ignored by
// every command that reads.
func resolveSupermarketSite(cmd *cobra.Command, siteFlag string) string {
	if siteFlag != "" {
		return siteFlag
	}
	path := resolveConfigPath(cmd)
	if path == "" {
		return ""
	}
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	cfg, err := config.Load(path)
	if err != nil {
		return ""
	}
	profile, err := selectSupermarketProfile(cmd, cfg)
	if err != nil {
		return ""
	}
	return profile.SupermarketSite
}

// selectSupermarketProfile picks which profile carries the Supermarket
// settings: an explicit --profile or environment profile wins, otherwise the
// conventional [supermarket] section, falling back to [default].
func selectSupermarketProfile(cmd *cobra.Command, cfg *config.Config) (config.Profile, error) {
	if profileName, _ := cmd.Flags().GetString("profile"); profileName != "" {
		return cfg.Profile(profileName)
	}
	if profileName := os.Getenv("CINC_PROFILE"); profileName != "" {
		return cfg.Profile(profileName)
	}
	if profileName := os.Getenv("CHEF_PROFILE"); profileName != "" {
		return cfg.Profile(profileName)
	}
	if profile, err := cfg.Profile("supermarket"); err == nil {
		return profile, nil
	}
	return cfg.Profile("default")
}

// loadCredentials returns the parsed credentials file the command was
// pointed at. If --config is empty (the default ~/.cinc/credentials
// path) and the file is missing, the first-run flow runs before the
// load is retried. An explicit --config pointing at a missing file is
// surfaced as the raw config.Load error.
func loadCredentials(cmd *cobra.Command) (*config.Config, error) {
	cfgPath, _ := cmd.Flags().GetString("config")
	usingDefault := cfgPath == ""
	if usingDefault {
		p, err := config.DefaultPath()
		if err != nil {
			return nil, err
		}
		cfgPath = p
	}
	if usingDefault {
		if _, err := os.Stat(cfgPath); errors.Is(err, fs.ErrNotExist) {
			if err := maybeFirstRun(cmd, cfgPath); err != nil {
				return nil, err
			}
			return nil, errFirstRunCompleted
		}
	}
	return config.Load(cfgPath)
}

// maybeFirstRun runs the welcome flow when the default cinc
// credentials file is missing. If credentials were set up (migrated
// or configured), it returns nil. Otherwise — no TTY, declined
// migration, or a write error — it returns an error pointing the
// caller (a server-touching command) at `cinc config create`.
func maybeFirstRun(cmd *cobra.Command, cincPath string) error {
	succeeded, _, err := offerFirstRun(cmd, cincPath)
	if err != nil {
		return err
	}
	if !succeeded {
		return missingCredentialsError(cincPath)
	}
	return nil
}

// offerFirstRun welcomes a first-time user and either migrates an
// existing ~/.chef/credentials file or walks them through the
// configure prompts inline. Returns (true, false, nil) when
// credentials were written, (false, false, nil) for benign no-ops
// (non-TTY), (false, true, nil) when the user explicitly declined a
// setup prompt, and (false, _, err) on failure. The declined flag
// lets the bare-`cinc` caller exit cleanly instead of falling through
// to help.
func offerFirstRun(cmd *cobra.Command, cincPath string) (succeeded, declined bool, err error) {
	if !stdinIsTTY() {
		return false, false, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false, false, nil
	}

	out := cmd.ErrOrStderr()
	writeWelcomeBanner(out)
	fmt.Fprintln(out)

	// Gate the whole setup on a single yes/no so a user who just wants help, or
	// who'll configure later, isn't dropped into the prompts. Default is yes.
	fmt.Fprintln(out, "It looks like this is your first time using Cinc.")
	fmt.Fprint(out, "Would you like to run the interactive setup? (Y/n) ")
	switch strings.ToLower(readPromptLine(cmd.InOrStdin())) {
	case "n", "no":
		fmt.Fprintln(out, "No problem — run `cinc config create` whenever you're ready to set up a profile.")
		fmt.Fprintln(out)
		return false, true, nil
	}

	chefPath := filepath.Join(home, ".chef", "credentials")
	if _, err := os.Stat(chefPath); err == nil {
		return runMigrationPrompt(cmd, chefPath, cincPath, out)
	}
	return runConfigurePrompt(cmd, cincPath, out)
}

// readPromptLine reads a single line from r, returning it trimmed of
// surrounding whitespace. It reads one byte at a time and stops at the first
// newline, so it never buffers past the line — a later reader (e.g. the
// configure prompts) still sees the rest of stdin intact.
func readPromptLine(r io.Reader) string {
	var b strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				break
			}
			b.WriteByte(buf[0])
		}
		if err != nil {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

// runMigrationPrompt asks the user whether to migrate ~/.chef/credentials
// and either runs the migration or returns a friendly decline. The
// declined flag matches offerFirstRun's contract.
func runMigrationPrompt(cmd *cobra.Command, chefPath, cincPath string, out io.Writer) (succeeded, declined bool, err error) {
	fmt.Fprintf(out, "We found an existing Chef config at %s. Want us to migrate it to %s for you? [Y/n] ", chefPath, cincPath)
	line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "n", "no":
		fmt.Fprintln(out, "No problem — run `cinc config create` whenever you're ready to set up a profile.")
		fmt.Fprintln(out)
		return false, true, nil
	}

	n, err := migrateChef(chefPath, cincPath)
	if err != nil {
		return false, false, err
	}
	fmt.Fprintf(out, "Done! Wrote %d profile(s) to %s.\n", n, cincPath)
	fmt.Fprintln(out)
	return true, false, nil
}

// runConfigurePrompt drives the interactive configure flow when no
// chef credentials file exists to migrate. The welcome line above is
// enough context to explain why the prompts are appearing; the
// configure flow prints its own opener so we don't repeat ourselves
// here.
func runConfigurePrompt(cmd *cobra.Command, cincPath string, out io.Writer) (succeeded, declined bool, err error) {
	if err := runFirstRunConfigure(cmd, cincPath); err != nil {
		return false, false, err
	}
	fmt.Fprintln(out)
	return true, false, nil
}

// realRunFirstRunConfigure delegates to the same prompt + write
// machinery `cinc config create` uses, so the on-disk result matches.
func realRunFirstRunConfigure(cmd *cobra.Command, cincPath string) error {
	answers, err := promptConfigure(cmd, configureDefaults{
		ConfigPath:      cincPath,
		ProfileName:     "default",
		SupermarketSite: supermarket.DefaultSite,
		ClientName:      defaultClientName(),
	})
	if err != nil {
		return err
	}
	if answers.ClientKey == "" {
		answers.ClientKey = defaultClientKey(answers.ClientName)
	}
	profile, err := config.NewProfile(
		answers.ChefServerURL,
		answers.ClientName,
		answers.ClientKey,
		answers.SSLVerifyMode,
		answers.SupermarketSite,
	)
	if err != nil {
		return err
	}
	if err := config.WriteProfile(answers.ConfigPath, answers.ProfileName, profile); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	fmt.Fprintln(out)
	fmt.Fprintf(out, "Wrote credentials profile %q to %s\n", answers.ProfileName, answers.ConfigPath)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Cinc CLI is now configured and you're ready to go!")
	return nil
}

func missingCredentialsError(cincPath string) error {
	return fmt.Errorf("no credentials yet at %s — run `cinc config create` to set one up", cincPath)
}
