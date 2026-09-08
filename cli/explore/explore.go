package explore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	tea "github.com/charmbracelet/bubbletea"
	cinc "github.com/cinc-project/cinc-api"
	"github.com/mattn/go-isatty"

	"github.com/cinc-project/cinc-cli/cli/client"
	"github.com/cinc-project/cinc-cli/cli/config"
)

// Options configures Run.
type Options struct {
	// Profiles is every profile available to choose from.
	Profiles map[string]config.Profile
	// Preselected, when set, names the profile to use directly,
	// skipping the picker (e.g. --profile or $CINC_PROFILE was given).
	Preselected string
	// NewClient builds an API client from a profile; injected so tests
	// can supply an httptest-backed client. Defaults to client.New.
	NewClient func(config.Profile) (*cinc.Client, error)

	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Run launches the explore TUI. It resolves the entry screen — straight
// to the object-type menu when the profile is unambiguous, otherwise
// the profile picker — and drives the bubbletea program.
func Run(ctx context.Context, opts Options) error {
	if opts.Stdin == nil {
		opts.Stdin = os.Stdin
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.NewClient == nil {
		opts.NewClient = client.New
	}
	if !stdoutIsTTY(opts.Stdout) {
		return errors.New("cinc explore needs an interactive terminal — run it from a normal shell session")
	}
	if len(opts.Profiles) == 0 {
		return errors.New("we couldn't find any profiles to explore. Run `cinc config create` to set one up")
	}

	s, err := resolveStartup(opts)
	if err != nil {
		return err
	}

	p := tea.NewProgram(
		newModel(ctx, opts, s),
		tea.WithContext(ctx),
		tea.WithInput(opts.Stdin),
		tea.WithOutput(opts.Stdout),
		tea.WithAltScreen(),
	)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("explore: %w", err)
	}
	return nil
}

// resolveStartup decides the entry screen and, when the profile is
// unambiguous, builds the client up front so a bad credential is
// reported before the alt-screen takes over.
func resolveStartup(opts Options) (startup, error) {
	names := sortedKeys(opts.Profiles)
	s := startup{profileNames: names}

	switch {
	case opts.Preselected != "":
		profile, ok := opts.Profiles[opts.Preselected]
		if !ok {
			return startup{}, fmt.Errorf("profile %q is not in the credentials file", opts.Preselected)
		}
		c, err := opts.NewClient(profile)
		if err != nil {
			return startup{}, err
		}
		s.client, s.profileName, s.screen = c, opts.Preselected, screenKinds
	case len(names) == 1:
		c, err := opts.NewClient(opts.Profiles[names[0]])
		if err != nil {
			return startup{}, err
		}
		s.client, s.profileName, s.screen = c, names[0], screenKinds
	default:
		s.screen = screenProfiles
	}
	return s, nil
}

// sortedKeys returns the keys of m in stable, sorted order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// stdoutIsTTY reports whether the output is an interactive terminal. We
// only treat *os.File outputs as candidates so tests passing a
// bytes.Buffer correctly say "not a TTY".
func stdoutIsTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fd := f.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}
