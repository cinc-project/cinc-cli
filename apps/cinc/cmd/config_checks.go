package cmd

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"sort"
	"time"

	cinc "github.com/tas50/cinc-api"
	"golang.org/x/term"

	cliclient "github.com/cinc-project/cinc-cli/cli/client"
	"github.com/cinc-project/cinc-cli/cli/config"
	"github.com/cinc-project/cinc-cli/cli/supermarket"
)

// resolveHost looks up a hostname; it is a package variable so the DNS check is
// testable without real network access.
var resolveHost = net.DefaultResolver.LookupHost

// checkResult is the outcome of one named pre-flight check. A check passes,
// warns, or fails: Passed is false only on a hard failure, so a warn does not
// make a profile invalid. Warn marks a passing-but-noteworthy state (a yellow
// check); Detail carries whatever message the check wants shown — a failure
// reason, a warning, or an informational note on a pass.
type checkResult struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Warn   bool   `json:"warn,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// checkOutcome is what a check's run func returns. The pass/passNote/warn/fail
// constructors keep the check bodies declarative.
type checkOutcome struct {
	passed bool
	warn   bool
	detail string
}

func pass() checkOutcome                  { return checkOutcome{passed: true} }
func passNote(detail string) checkOutcome { return checkOutcome{passed: true, detail: detail} }
func warn(detail string) checkOutcome     { return checkOutcome{passed: true, warn: true, detail: detail} }
func fail(detail string) checkOutcome     { return checkOutcome{passed: false, detail: detail} }

// profileResult collects one profile's check results and whether they all
// passed.
type profileResult struct {
	Name   string        `json:"name"`
	Valid  bool          `json:"valid"`
	Checks []checkResult `json:"checks"`
}

// configValidationResult is the full report: file-level (top-level) checks plus
// a per-profile breakdown.
type configValidationResult struct {
	Path     string          `json:"path"`
	Valid    bool            `json:"valid"`
	TopLevel []checkResult   `json:"top_level_checks"`
	Profiles []profileResult `json:"profiles,omitempty"`
}

// profileCheck is one named per-profile pre-flight check. applies (when set)
// gates whether the check runs for a given profile; inapplicable checks are
// omitted from the report rather than shown as passing or failing.
type profileCheck struct {
	name    string
	applies func(config.Profile) bool
	run     func(ctx context.Context, p config.Profile) checkOutcome
	// network marks a check that talks to the outside world and so needs its
	// own time budget. Structural checks are instant and take none.
	network bool
}

// networkCheckTimeout is how long any single network check may take. It is
// per check, not shared: a slow or unreachable endpoint in one profile must
// not spend the budget that a later profile's checks need, or those profiles
// get reported as broken when the only problem is the clock. It is a var (not
// a const) only so tests can shrink it; production never reassigns it.
var networkCheckTimeout = 10 * time.Second

// profileChecks is the ordered registry of per-profile pre-flight checks, each
// with a friendly name. Structural checks come first (instant), then the
// network checks.
var profileChecks = []profileCheck{
	{
		name: "Client name is configured",
		run: func(_ context.Context, p config.Profile) checkOutcome {
			if p.ClientName == "" {
				return fail("client_name is required")
			}
			return pass()
		},
	},
	{
		name: "Client key is configured",
		run: func(_ context.Context, p config.Profile) checkOutcome {
			if p.KeyPath == "" {
				return fail("client_key is required")
			}
			return pass()
		},
	},
	{
		name: "An endpoint is configured",
		run: func(_ context.Context, p config.Profile) checkOutcome {
			if p.RawServerURL == "" && p.SupermarketSite == "" {
				return fail("configure cinc_server_url, chef_server_url, or supermarket_site")
			}
			return pass()
		},
	},
	{
		// One check covers both failure modes: ParseServerURL rejects a URL
		// that isn't a valid http(s) URL and one that omits /organizations/<org>,
		// reporting the precise reason either way.
		name:    "Server URL is valid",
		applies: func(p config.Profile) bool { return p.RawServerURL != "" },
		run: func(_ context.Context, p config.Profile) checkOutcome {
			if _, _, err := cinc.ParseServerURL(p.RawServerURL); err != nil {
				return fail(err.Error())
			}
			return pass()
		},
	},
	{
		// Always runs: when supermarket_site is unset, the CLI falls back to the
		// public Supermarket, so the check passes with a note saying which URL.
		name: "Supermarket site URL is valid",
		run: func(_ context.Context, p config.Profile) checkOutcome {
			if p.SupermarketSite == "" {
				return passNote(fmt.Sprintf("using the default %s", supermarket.DefaultSite))
			}
			if err := config.ValidateSiteURL(p.SupermarketSite); err != nil {
				return fail(err.Error())
			}
			return pass()
		},
	},
	{
		name:    "ssl_verify_mode is valid",
		applies: func(p config.Profile) bool { return p.SSLVerifyMode != "" },
		run: func(_ context.Context, p config.Profile) checkOutcome {
			switch p.SSLVerifyMode {
			case ":verify_peer":
				return pass()
			case ":verify_none":
				return warn("Insecure :verify_none mode configured")
			default:
				return fail("ssl_verify_mode must be :verify_peer or :verify_none")
			}
		},
	},
	{
		name:    "Client key file is readable",
		applies: func(p config.Profile) bool { return p.KeyPath != "" },
		run: func(_ context.Context, p config.Profile) checkOutcome {
			if _, err := cinc.LoadKeyFile(p.KeyPath); err != nil {
				return fail(err.Error())
			}
			return pass()
		},
	},
	{
		name:    "Server is reachable",
		network: true,
		applies: func(p config.Profile) bool { return p.ServerURL != "" && p.Org != "" },
		run: func(ctx context.Context, p config.Profile) checkOutcome {
			// Resolve DNS first so a name-resolution failure is reported
			// distinctly from a connection failure, then try to connect.
			if host, err := parseServerHost(p.ServerURL); err == nil {
				if _, err := resolveHost(ctx, host); err != nil {
					return fail(fmt.Sprintf("cannot resolve host %q: %v", host, err))
				}
			}
			c, err := cliclient.New(p)
			if err != nil {
				return fail(err.Error())
			}
			if _, _, err := c.Clients.List(ctx); err != nil {
				return fail(err.Error())
			}
			return pass()
		},
	},
	{
		name:    "Supermarket is reachable",
		network: true,
		applies: func(p config.Profile) bool { return p.SupermarketSite != "" },
		run: func(ctx context.Context, p config.Profile) checkOutcome {
			client, err := supermarket.NewAnonymous(p.SupermarketSite)
			if err != nil {
				return fail(err.Error())
			}
			if err := client.Reachable(ctx); err != nil {
				return fail(err.Error())
			}
			return pass()
		},
	},
}

// runConfigChecks evaluates the top-level and per-profile checks for a loaded
// config. Each network check gets its own timeout; see networkCheckTimeout.
func runConfigChecks(ctx context.Context, path string, cfg *config.Config) configValidationResult {
	hasProfiles := len(cfg.Profiles) > 0
	result := configValidationResult{
		Path: path,
		TopLevel: []checkResult{
			{Name: "Credentials file is valid TOML", Passed: true},
			{
				Name:   "At least one profile is defined",
				Passed: hasProfiles,
				Detail: cond(hasProfiles, "", "the credentials file defines no profiles"),
			},
		},
	}

	names := make([]string, 0, len(cfg.Profiles))
	for name := range cfg.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := cfg.Profiles[name]
		pr := profileResult{Name: name, Valid: true}
		for _, def := range profileChecks {
			if def.applies != nil && !def.applies(p) {
				continue
			}
			out := runProfileCheck(ctx, def, p)
			pr.Checks = append(pr.Checks, checkResult{
				Name:   def.name,
				Passed: out.passed,
				Warn:   out.warn,
				Detail: out.detail,
			})
			if !out.passed {
				pr.Valid = false
			}
		}
		result.Profiles = append(result.Profiles, pr)
	}

	result.Valid = allPassed(result.TopLevel)
	for _, pr := range result.Profiles {
		if !pr.Valid {
			result.Valid = false
		}
	}
	return result
}

// runProfileCheck runs one check, giving a network check its own deadline so
// no single slow endpoint can starve the checks that follow it.
func runProfileCheck(ctx context.Context, def profileCheck, p config.Profile) checkOutcome {
	if !def.network {
		return def.run(ctx, p)
	}
	ctx, cancel := context.WithTimeout(ctx, networkCheckTimeout)
	defer cancel()
	return def.run(ctx, p)
}

// parseServerHost validates that serverURL is a well-formed http(s) URL and
// returns its hostname (without port).
func parseServerHost(serverURL string) (string, error) {
	u, err := url.Parse(serverURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", fmt.Errorf("not a valid http(s) server URL: %q", serverURL)
	}
	return u.Hostname(), nil
}

func allPassed(checks []checkResult) bool {
	for _, c := range checks {
		if !c.Passed {
			return false
		}
	}
	return true
}

func cond(b bool, yes, no string) string {
	if b {
		return yes
	}
	return no
}

// renderConfigChecks writes the human-readable check report: an overall line,
// the top-level checks, a blank line, then each profile tagged [VALID]/[INVALID]
// with its checks. ✓/✗ and the tags are colorized when the writer is a terminal
// and NO_COLOR is unset.
func renderConfigChecks(w io.Writer, result configValidationResult) {
	color := useColor(w)
	if result.Valid {
		fmt.Fprintf(w, "Config %s is valid\n", result.Path)
	} else {
		// Draw the whole headline in bold red so the failure reads at a glance.
		fmt.Fprintln(w, colorize(color, ansiBoldRed, fmt.Sprintf("Config %s is invalid!", result.Path)))
	}
	for _, c := range result.TopLevel {
		writeCheckLine(w, color, "", c)
	}
	for _, pr := range result.Profiles {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "%s profile %s\n", pr.Name, validTag(color, pr.Valid))
		for _, c := range pr.Checks {
			writeCheckLine(w, color, "  ", c)
		}
	}
}

func writeCheckLine(w io.Writer, color bool, indent string, c checkResult) {
	fmt.Fprintf(w, "%s%s %s", indent, mark(color, c.Passed, c.Warn), c.Name)
	// Detail is set by the check only when it's meant to be shown — a failure
	// reason, a warning, or an informational note on a pass. A warning's detail
	// is drawn orange to match its mark.
	if c.Detail != "" {
		detail := c.Detail
		if c.Warn {
			detail = colorize(color, ansiOrange, detail)
		}
		fmt.Fprintf(w, ": %s", detail)
	}
	fmt.Fprintln(w)
}

const (
	ansiReset = "\x1b[0m"

	ansiOrange = "\x1b[38;5;214m" // 256-color light orange, for warning detail

	// Status glyphs and tags are drawn bold so the pass/warn/fail column stands
	// out at a glance.
	ansiBoldGreen  = "\x1b[1;32m"
	ansiBoldOrange = "\x1b[1;38;5;214m"
	ansiBoldRed    = "\x1b[1;31m"
)

func colorize(on bool, color, s string) string {
	if !on {
		return s
	}
	return color + s + ansiReset
}

// mark renders a check's status glyph in bold: a green ✓ on a pass, an orange ✓
// on a warn (passing but noteworthy), and a red ✗ on a failure.
func mark(on, passed, warn bool) string {
	switch {
	case warn:
		return colorize(on, ansiBoldOrange, "✓")
	case passed:
		return colorize(on, ansiBoldGreen, "✓")
	default:
		return colorize(on, ansiBoldRed, "✗")
	}
}

func validTag(on, valid bool) string {
	if valid {
		return colorize(on, ansiBoldGreen, "[VALID]")
	}
	return colorize(on, ansiBoldRed, "[INVALID]")
}

// useColor reports whether ANSI color should be emitted: only when NO_COLOR is
// unset and the writer is a terminal (so piped output and tests stay plain).
func useColor(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}
