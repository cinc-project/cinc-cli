package supermarket

// Error messages for the Supermarket commands. They are written for someone
// who has never used Chef or Cinc: each one says what went wrong in terms of
// the things the user named (cookbook, version, site, key file, profile), why
// it happened, and the command or setting that fixes it. They are full
// sentences on purpose; see the ST1005 note in .golangci.yml.
//
// Every message keeps the underlying error reachable through errors.Is and
// errors.As (explainedError.Unwrap), so callers can still match on
// sm.ErrNotFound, fs.ErrNotExist and the rest.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"runtime"
	"strings"

	sm "github.com/cinc-project/cinc-supermarket-api"

	"github.com/cinc-project/cinc-cli/cli/progname"
)

// Option customizes New.
type Option func(*Client)

// WithProfileSource names the profile and credentials file the Supermarket
// identity came from, so error messages can point at the exact file to edit.
// Either may be empty when unknown.
func WithProfileSource(profile, path string) Option {
	return func(c *Client) {
		c.identity.profile = profile
		c.identity.configPath = path
	}
}

// explainedError carries a user-facing message while keeping the error it
// explains reachable through errors.Is/As.
type explainedError struct {
	msg string
	err error
}

func (e *explainedError) Error() string { return e.msg }
func (e *explainedError) Unwrap() error { return e.err }

func explain(err error, format string, args ...any) error {
	return &explainedError{msg: fmt.Sprintf(format, args...), err: err}
}

// validateSite parses a Supermarket base URL, rejecting anything the
// Supermarket client library would refuse, with a message that says what a
// valid address looks like and where it's configured.
func validateSite(site string) (*url.URL, error) {
	base, err := url.Parse(strings.TrimRight(site, "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" ||
		base.RawQuery != "" || base.Fragment != "" {
		return nil, invalidSiteError(site)
	}
	return base, nil
}

// ValidateSite reports whether site is a Supermarket address the client can
// use, with the same message New and NewAnonymous give.
func ValidateSite(site string) error {
	_, err := validateSite(site)
	return err
}

func invalidSiteError(site string) error {
	return fmt.Errorf(`%q isn't a Supermarket address we can use.

A Supermarket site is a full web address that starts with http:// or https://, such as %s or https://supermarket.example.com. It comes from the --supermarket-site flag or, when that isn't set, the supermarket_site setting in your profile. Fix the address, or leave both unset to use the public Supermarket.`,
		site, DefaultSite)
}

// identity says where the Supermarket signing identity came from, for the
// key and authentication messages.
type identity struct {
	user       string
	userSet    string // "supermarket_client_name" or "client_name"
	keyPath    string // expanded
	keySet     string // "supermarket_key" or "client_key"
	site       string
	profile    string
	configPath string
}

// where names the profile and file the settings live in, as precisely as we
// know them.
func (k identity) where() string {
	switch {
	case k.profile != "" && k.configPath != "":
		return fmt.Sprintf("your %q profile in %s", k.profile, k.configPath)
	case k.configPath != "":
		return "your profile in " + k.configPath
	case k.profile != "":
		return fmt.Sprintf("your %q profile", k.profile)
	}
	return "your profile"
}

// file names the credentials file to edit.
func (k identity) file() string {
	if k.configPath != "" {
		return k.configPath
	}
	return "your credentials file"
}

// setting describes the key setting in use, calling out the client_key
// fallback since it's invisible otherwise.
func (k identity) setting() string {
	if k.keySet == "client_key" {
		return "that's the client_key setting, which " + progname.Get() + " uses for Supermarket too because supermarket_key isn't set"
	}
	return "that's the supermarket_key setting"
}

// keyLoadError explains a signing key that couldn't be read (readErr) or
// parsed (parseErr).
func keyLoadError(k identity, readErr, parseErr error) error {
	prog := progname.Get()
	switch {
	case errors.Is(readErr, fs.ErrNotExist):
		steps := fmt.Sprintf(`Here's how to fix it:
  - If you have the key file somewhere else, set %s in %s to its full path.
  - If you don't have it, you need the private key of the account you sign in to %s with. Supermarket checks every upload against that account's public key, so another .pem file won't work.
  - To check the cookbook package without uploading anything, run `+"`%s supermarket share <cookbook> --dry-run`"+`, which doesn't need a key.`,
			k.keySet, k.file(), k.site, prog)
		if k.keySet == "client_key" {
			steps += `
  - If you publish under a different account than your Cinc server client, add supermarket_client_name and supermarket_key to the profile.`
		}
		return explain(readErr, `can't find your Supermarket key at %s.

%s signs every cookbook it shares with a private key file, so the Supermarket at %s knows the upload really comes from its user %q. %s says the key is at the path above (%s), but there's no file there.

%s`, k.keyPath, prog, k.site, k.user, capitalize(k.where()), k.setting(), steps)
	case errors.Is(readErr, fs.ErrPermission):
		fix := fmt.Sprintf(`To fix it, make the file readable by you and nobody else:
  chmod 600 %s
If another account owns the file, take ownership first with `+"`sudo chown $(whoami) %s`"+`.`, k.keyPath, k.keyPath)
		if runtime.GOOS == "windows" {
			fix = "To fix it, right-click the file, open Properties > Security, and give your account Read access."
		}
		return explain(readErr, `can't read your Supermarket key at %s.

The file is there, but your account doesn't have permission to open it. %s needs to read this key (the %s setting in %s) to sign the cookbooks it shares on %s.

%s`, k.keyPath, prog, k.keySet, k.where(), k.site, fix)
	case readErr != nil:
		return explain(readErr, `can't read your Supermarket key at %s: %v.

The %s setting in %s should name the private key file (.pem) %s signs Supermarket uploads with.`,
			k.keyPath, readErr, k.keySet, k.where(), prog)
	}
	return explain(parseErr, `your Supermarket key at %s isn't a private key we can use.

The file is there, but %s couldn't read an RSA private key from it (%v). It's the %s setting in %s. A private key is a PEM file whose first line is "-----BEGIN RSA PRIVATE KEY-----" or "-----BEGIN PRIVATE KEY-----"; a public key or certificate won't work.

Point %s at the private key (.pem) for %q.`,
		k.keyPath, prog, parseErr, k.keySet, k.where(), k.keySet, k.user)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// missingCookbookNameError is returned when a command gets an empty cookbook
// name, for example `cinc supermarket show ""`.
func missingCookbookNameError(verb string) error {
	return fmt.Errorf("tell us which cookbook to %s, for example: %s supermarket %s nginx", verb, progname.Get(), verb)
}

// RequestError explains a failed read from the Supermarket at site, for
// callers that talk to the library directly (the explore TUI). action
// completes "we couldn't ...", e.g. "list cookbooks".
func RequestError(site, action string, err error) error {
	return requestError(site, action, err)
}

// requestError explains a failed Supermarket read. action completes "we
// couldn't ...", e.g. "download nginx 1.2.0".
func requestError(site, action string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("we stopped before we could %s on %s: %w", action, site, err)
	}
	if errors.Is(err, sm.ErrRateLimited) {
		return explain(err, "the Supermarket at %s is limiting how many requests we can send, so we couldn't %s. Wait a minute and try again.", site, action)
	}
	var resp *sm.ErrorResponse
	if errors.As(err, &resp) && resp.StatusCode >= 500 {
		return explain(err, "the Supermarket at %s ran into a problem on its side, so we couldn't %s (HTTP %d). That's usually temporary, so try again in a minute.", site, action, resp.StatusCode)
	}
	var urlErr *url.Error
	var netErr net.Error
	if errors.As(err, &urlErr) || errors.As(err, &netErr) {
		// Wrap the transport's own error rather than the *url.Error around
		// it: the CLI's top-level cli/client.Explain rewrites any *url.Error
		// into advice about the Cinc server URL, which is the wrong setting
		// to check for a Supermarket.
		cause := rootCause(err)
		var certErr *tls.CertificateVerificationError
		var hostErr x509.HostnameError
		if errors.As(err, &certErr) || errors.As(err, &hostErr) {
			return explain(cause, `we couldn't verify the TLS certificate of the Supermarket at %s, so we didn't %s.

%v

A Supermarket's certificate has to be issued for the host name in its address and signed by a certificate authority this system trusts. Check the address (the --supermarket-site flag, or supermarket_site in your profile), or add the authority to your system's trusted certificates.`, site, action, cause)
		}
		return explain(cause, `we couldn't reach the Supermarket at %s to %s.

%v

Check the address (the --supermarket-site flag, or supermarket_site in your profile) and your network connection, then try again.`, site, action, cause)
	}
	return fmt.Errorf("the Supermarket at %s answered with an error when we tried to %s: %w", site, action, err)
}

// rootCause drops the library's "supermarket: GET /path:" prefixes, which
// say nothing a user can act on, and keeps the transport's own message
// (e.g. "dial tcp ...: connection refused").
func rootCause(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err
	}
	return err
}

// cookbookNotFoundError says a cookbook doesn't exist on site and how to
// look for it.
func cookbookNotFoundError(site, cookbook string, err error) error {
	prog := progname.Get()
	return explain(err, `we couldn't find a cookbook named %q on %s.

Check the spelling, or search for it:
  %s supermarket search %s
If it's published on a different Supermarket, point at that one with --supermarket-site.`, cookbook, site, prog, cookbook)
}

// notFoundError explains a 404 for cookbook (and version, when set). A 404
// for a version doesn't say whether the cookbook or the version is missing,
// so it asks Supermarket for the cookbook to tell the two apart.
func (c *Client) notFoundError(ctx context.Context, cookbook, version string, err error) error {
	site := c.base.String()
	if version == "" || version == "latest" {
		return cookbookNotFoundError(site, cookbook, err)
	}
	cb, _, getErr := c.api.Cookbooks.Get(ctx, cookbook)
	if errors.Is(getErr, sm.ErrNotFound) {
		return cookbookNotFoundError(site, cookbook, err)
	}
	prog := progname.Get()
	if getErr != nil {
		return explain(err, `we couldn't find %s %s on %s. Either the cookbook or that version isn't published there.

To check which versions exist, run:
  %s supermarket show %s`, cookbook, version, site, prog, cookbook)
	}
	newest := ""
	if latest := VersionFromURL(cb.LatestVersion); latest != "" {
		newest = fmt.Sprintf("The newest version is %s. ", latest)
	}
	return explain(err, `%s is on %s, but it has no version %s.

%sTo see every published version, run:
  %s supermarket show %s`, cookbook, site, version, newest, prog, cookbook)
}

// readError routes a failed read of cookbook (and version) to the not-found
// explanation or the generic request one.
func (c *Client) readError(ctx context.Context, cookbook, version, action string, err error) error {
	if errors.Is(err, sm.ErrNotFound) {
		return c.notFoundError(ctx, cookbook, version, err)
	}
	return requestError(c.base.String(), action, err)
}

// shareError explains why Supermarket refused a cookbook upload. The cases
// follow upstream Supermarket's CookbookUploadsController and its en.yml
// messages; anything unrecognised falls back to the server's own words.
func (c *Client) shareError(cookbook, version string, err error) error {
	prog := progname.Get()
	k := c.identity
	site := c.base.String()
	var resp *sm.ErrorResponse
	if !errors.As(err, &resp) {
		return requestError(site, "share "+cookbook+" "+version, err)
	}
	said := strings.Join(resp.Messages, " ")
	switch {
	case resp.Code == "AUTHENTICATION_FAILED" && strings.Contains(said, "Could not find user"):
		return explain(err, `the Supermarket at %s doesn't have a user named %q.

%s shares cookbooks as the user named by %s in %s, and Supermarket only accepts uploads from people who have signed in to it.
  - If your Supermarket account has a different name, set supermarket_client_name to that name in %s.
  - If you've never used %s, sign in there once to create your account, then share again.`,
			site, k.user, prog, k.userSet, k.where(), k.file(), site)
	case resp.Code == "AUTHENTICATION_FAILED" && strings.Contains(said, "log into"):
		return explain(err, `the Supermarket at %s doesn't have a key on file for %q yet.

Supermarket learns your public key the first time you sign in to its website. Sign in to %s as %q, allow it to read your account details when it asks, then share again.`,
			site, k.user, site, k.user)
	case resp.Code == "AUTHENTICATION_FAILED" && strings.Contains(said, "key pair"):
		fix := "Check that supermarket_key in " + k.file() + " points at the private key of your Supermarket account."
		if k.keySet == "client_key" {
			fix = "supermarket_key isn't set, so " + prog + " used client_key, your Cinc server client's key, which usually isn't the same. Set supermarket_key in " + k.file() + " to the private key of your Supermarket account."
		}
		return explain(err, `the Supermarket at %s didn't accept the key %s signed the upload with.

%s signed as %q with %s (the %s setting in %s), but that isn't the private key matching the public key Supermarket has for %q.
  - %s
  - If you changed your key recently, sign out of %s and sign back in so it picks up the new public key.`,
			site, prog, prog, k.user, k.keyPath, k.keySet, k.where(), k.user, fix, site)
	case resp.Code == "AUTHENTICATION_FAILED":
		return explain(err, `the Supermarket at %s couldn't verify the signature on the upload.

That usually means this computer's clock is off by more than a few minutes, since signed requests carry a timestamp. Check the system clock, then share again. Supermarket said: %s`,
			site, said)
	case errors.Is(err, sm.ErrUnauthorized) || errors.Is(err, sm.ErrForbidden):
		return explain(err, `%q isn't allowed to publish %s on %s.

Someone else already owns the %s cookbook there, and only its owner and collaborators can publish new versions. Ask the owner (run `+"`%s supermarket show %s`"+` to see who maintains it) to add %q as a collaborator, or publish your cookbook under a different name.`,
			k.user, cookbook, site, cookbook, prog, cookbook, k.user)
	case errors.Is(err, sm.ErrInvalidData) && strings.Contains(said, "already exists"):
		return explain(err, `%s %s is already published on %s.

Supermarket never replaces a published version, so each share needs a new version number. Raise the version in your cookbook's metadata.rb (or metadata.json), then share again.`,
			cookbook, version, site)
	case errors.Is(err, sm.ErrInvalidData) && strings.Contains(said, "Category"):
		return explain(err, `the Supermarket at %s turned down %s %s: %s

Pass one of the categories that Supermarket lists as the second argument, for example:
  %s supermarket share %s 'Web Servers'`,
			site, cookbook, version, said, prog, cookbook)
	case errors.Is(err, sm.ErrInvalidData):
		return explain(err, "the Supermarket at %s turned down %s %s: %s", site, cookbook, version, said)
	}
	return requestError(site, "share "+cookbook+" "+version, err)
}
