package cmd

import (
	"bytes"
	"encoding/json"
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
	"github.com/cinc-project/cinc-cli/cli/progname"
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

// checkChef reports why ~/.chef/credentials cannot be migrated, or nil if
// it can, before first-run setup offers the migration. It is a
// package-level variable so tests can swap in a fake.
var checkChef = setup.CheckChef

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
	format, err := printer.ParseFormat(name)
	if err != nil {
		return "", fmt.Errorf("we don't know the output format %q. Use --format human or --format json.", name)
	}
	return format, nil
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
// The cinc-api codec derives the AES key from these bytes itself. A
// literal is used exactly as given, as knife uses its --secret. A file's
// contents are stripped of leading and trailing whitespace, as Chef's
// EncryptedDataBagItem.load_secret strips them, so a secret file ending in
// a newline works the same for cinc, knife and chef-client; a file that is
// empty once stripped is refused, as Chef refuses it. --secret and
// --secret-file are mutually exclusive.
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
		path, err := config.ExpandHome(profile.SecretFile)
		if err != nil {
			return nil, err
		}
		return readSecretFile(path)
	}
	return nil, errors.New("we need an encrypted data bag secret but couldn't find one. Pass --secret-file <path> (or --secret <literal>), set $CINC_SECRET_FILE, or add a secret_file key to your credentials profile.")
}

// readSecretFile reads a secret file the way Chef does: its contents with
// leading and trailing whitespace stripped. A read error, or a file with
// nothing left once stripped, becomes a conversational message.
func readSecretFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("can't read the data bag secret at %s: %w", path, err)
	}
	secret := bytes.TrimSpace(data)
	if len(secret) == 0 {
		return nil, fmt.Errorf("the data bag secret file at %s is empty. Put the shared secret in it, or point --secret-file at the right file.", path)
	}
	return secret, nil
}

// resolveClient builds a server client from the --config and --profile
// flags. An empty --config falls back to the default config path.
func resolveClient(cmd *cobra.Command) (*cinc.Client, error) {
	profile, err := resolveProfile(cmd)
	if err != nil {
		return nil, err
	}
	c, err := client.New(profile)
	if errors.Is(err, config.ErrMissingServerURL) {
		return nil, missingServerURLError(cmd)
	}
	if err != nil {
		return nil, friendlyKeyFileError(cmd, profile, err)
	}
	return c, nil
}

// missingServerURLError explains a profile with no server to talk to,
// which is what pressing Enter through every first-run prompt writes. It
// names the profile and the file, and how to add the missing URL.
func missingServerURLError(cmd *cobra.Command) error {
	return fmt.Errorf("your %q profile in %s doesn't say which server to use yet. Run `%s config create` to add one, or set cinc_server_url to your server's URL, like https://cinc.example.com/organizations/<org>.",
		profileNameForCommand(cmd), resolveConfigPath(cmd), progname.Get())
}

// friendlyKeyFileError rewrites a "client key file missing/unreadable"
// error into a conversational message that names both the key path
// and the credentials file where client_key is configured. Other
// errors pass through unchanged.
func friendlyKeyFileError(cmd *cobra.Command, p config.Profile, err error) error {
	// Name the path we actually tried, which for a ~/ key is not the one
	// written in the file.
	keyPath := p.KeyPath
	if expanded, expandErr := config.ExpandHome(keyPath); expandErr == nil {
		keyPath = expanded
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("can't find your client key at %s — it's set as client_key in %s. Create the key file, or update client_key to point at the right path.", keyPath, resolveConfigPath(cmd))
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("can't read your client key at %s — it's set as client_key in %s. Check the file's permissions; cinc needs to read it to sign requests.", keyPath, resolveConfigPath(cmd))
	}
	return err
}

// resolveConfigPath returns the credentials file path the command is
// pointed at, matching loadCredentials' behavior so error messages
// name the same path the loader used.
func resolveConfigPath(cmd *cobra.Command) string {
	if p, _ := cmd.Flags().GetString("config"); p != "" {
		if expanded, err := config.ExpandHome(p); err == nil {
			return expanded
		}
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
	} else {
		// A shell leaves --config=~/... and a quoted "~/..." alone.
		p, err := config.ExpandHome(cfgPath)
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
	if !confirmFirstRun(cmd, out) {
		return false, true, nil
	}

	chefPath := filepath.Join(home, ".chef", "credentials")
	if _, err := os.Stat(chefPath); err == nil {
		// Offering to migrate a file that can only fail, or that would
		// migrate nothing, leaves the user stuck at this prompt on every
		// run. Say why and set up a new profile instead.
		if problem := checkChef(chefPath); problem != nil {
			fmt.Fprintf(out, "We found a Chef config at %s, but we can't migrate it: %v. Let's set up a new profile instead.\n", chefPath, problem)
			return runConfigurePrompt(cmd, cincPath, out)
		}
		return runMigrationPrompt(cmd, chefPath, cincPath, out)
	}
	return runConfigurePrompt(cmd, cincPath, out)
}

// readPromptLine reads a single line from r, returning it trimmed of
// surrounding whitespace. It reads one byte at a time and stops at the first
// newline, so it never buffers past the line — a later reader (e.g. the
// configure prompts) still sees the rest of stdin intact.
func readPromptLine(r io.Reader) string {
	line, _ := readPromptAnswer(r)
	return line
}

// readPromptAnswer is readPromptLine that also reports whether the user
// answered at all: ok is false when input ended before a single byte
// arrived, which on a terminal means Ctrl-D rather than Enter.
func readPromptAnswer(r io.Reader) (line string, ok bool) {
	var b strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			ok = true
			if buf[0] == '\n' {
				break
			}
			b.WriteByte(buf[0])
		}
		if err != nil {
			break
		}
	}
	return strings.TrimSpace(b.String()), ok
}

// confirmFirstRun reads the answer to a first-run yes/no question, where
// Enter means yes. No, or end of input, prints the decline message and
// returns false: a user who presses Ctrl-D is backing out, and treating it
// as yes would carry on into prompts nobody is answering.
func confirmFirstRun(cmd *cobra.Command, out io.Writer) bool {
	answer, ok := readPromptAnswer(cmd.InOrStdin())
	switch strings.ToLower(answer) {
	case "n", "no":
	default:
		if ok {
			return true
		}
		// Ctrl-D echoes no newline, so end the prompt's line first.
		fmt.Fprintln(out)
	}
	fmt.Fprintf(out, "No problem — run `%s config create` whenever you're ready to set up a profile.\n", progname.Get())
	fmt.Fprintln(out)
	return false
}

// runMigrationPrompt asks the user whether to migrate ~/.chef/credentials
// and either runs the migration or returns a friendly decline. The
// declined flag matches offerFirstRun's contract.
func runMigrationPrompt(cmd *cobra.Command, chefPath, cincPath string, out io.Writer) (succeeded, declined bool, err error) {
	fmt.Fprintf(out, "We found an existing Chef config at %s. Want us to migrate it to %s for you? [Y/n] ", chefPath, cincPath)
	if !confirmFirstRun(cmd, out) {
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
	// Expand ~ the way `config create` does, or a typed ~/work/credentials
	// lands in a directory named ~ under wherever the command ran.
	if answers.ConfigPath, err = expandHome(answers.ConfigPath); err != nil {
		return err
	}
	if answers.ClientKey, err = expandHome(answers.ClientKey); err != nil {
		return err
	}
	if answers.ReplaceFile {
		if err := clearCredentials(answers.ConfigPath); err != nil {
			return err
		}
	}
	// Same reasoning as `config create`: the prompts do not cover every
	// key, so update what is on disk rather than replacing it.
	err = config.UpdateProfile(answers.ConfigPath, answers.ProfileName, func(p *config.Profile) error {
		updated, err := config.NewProfile(
			answers.ChefServerURL,
			answers.ClientName,
			answers.ClientKey,
			answers.SSLVerifyMode,
			answers.SupermarketSite,
		)
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
	fmt.Fprintf(out, "Wrote credentials profile %q to %s\n", answers.ProfileName, answers.ConfigPath)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Cinc CLI is now configured and you're ready to go!")
	return nil
}

func missingCredentialsError(cincPath string) error {
	return fmt.Errorf("no credentials yet at %s — run `%s config create` to set one up", cincPath, progname.Get())
}

// unchanged reports whether an edited object would send the same JSON as the
// original. The comparison is on the encoding rather than the Go values: the
// server sends empty attribute maps as {}, the editor's round trip through
// omitempty turns them into nil, and reflect.DeepEqual calls those different,
// so an edit that changed nothing would still be PUT and reported as updated.
func unchanged(before, after any) bool {
	a, errA := json.Marshal(before)
	b, errB := json.Marshal(after)
	return errA == nil && errB == nil && bytes.Equal(a, b)
}
