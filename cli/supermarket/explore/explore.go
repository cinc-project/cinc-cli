package explore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	sm "github.com/cinc-project/cinc-supermarket-api"
	"github.com/mattn/go-isatty"
)

// Options configures Run.
type Options struct {
	Site   string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Install downloads a cookbook from Supermarket and uploads it to the
	// configured Cinc Server. It may be nil (install disabled). The
	// closure resolves credentials lazily, so launching explore stays
	// credential-free until the user actually installs something.
	Install func(ctx context.Context, name, version string) error
}

// Run launches the explore TUI against the configured Supermarket
// site. It is the single entry point used by the cobra command.
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
	if !stdoutIsTTY(opts.Stdout) {
		return errors.New("cinc supermarket explore needs an interactive terminal — run it from a normal shell session")
	}
	if opts.Site == "" {
		opts.Site = sm.DefaultBaseURL
	}
	client, err := newRealClient(opts.Site)
	if err != nil {
		return fmt.Errorf("supermarket explore: %w", err)
	}
	m := initialModel(ctx, client, opts.Site, openBrowser, opts.Install)
	p := tea.NewProgram(
		m,
		tea.WithContext(ctx),
		tea.WithInput(opts.Stdin),
		tea.WithOutput(opts.Stdout),
		tea.WithAltScreen(),
	)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("supermarket explore: %w", err)
	}
	return nil
}

// stdoutIsTTY checks whether the output is an interactive terminal. We
// only treat *os.File-shaped outputs as candidates so tests passing
// bytes.Buffer just say "no, not a TTY".
func stdoutIsTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fd := f.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// validateBrowserURL makes sure a URL handed to the platform opener is a
// plain http(s) web address. The value originates from the Supermarket API
// (a cookbook's ExternalURL), so we don't trust it: we reject anything that
// isn't http/https with a real host, and anything that looks like a flag
// (leading "-") so it can't be mistaken for an option by openers like
// xdg-open. Returns a friendly, user-facing error on a bad URL.
func validateBrowserURL(raw string) error {
	if raw == "" {
		return errors.New("we can't open that link — it's empty")
	}
	if strings.HasPrefix(raw, "-") {
		return fmt.Errorf("we won't open %q — it looks like a command-line flag, not a web address", raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("we can't open %q — it isn't a valid URL", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("we'll only open http or https links, not %q", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("we can't open %q — it has no host", raw)
	}
	return nil
}

// openBrowser shells out to the platform-native URL opener. Failures
// are returned so the TUI can surface them in the footer.
func openBrowser(rawURL string) error {
	if err := validateBrowserURL(rawURL); err != nil {
		return err
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		// "--" stops xdg-open from treating a URL as an option, a second
		// guard alongside the leading-dash rejection in validateBrowserURL.
		cmd = exec.Command("xdg-open", "--", rawURL)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	// Reap the child so we don't leave a zombie behind. We don't care
	// about the exit status — if the opener fails, the user will see
	// nothing happen, but that's a UX issue beyond our reach.
	go func() { _ = cmd.Wait() }()
	return nil
}
