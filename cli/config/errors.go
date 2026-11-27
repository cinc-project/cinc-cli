package config

// This file holds the messages for problems with the credentials file and
// its profiles. They are written for someone who has never used Cinc: each
// names the profile and file involved, says what the setting is for, and
// shows the line that fixes it.

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	cinc "github.com/cinc-project/cinc-api"

	"github.com/cinc-project/cinc-cli/cli/progname"
)

// exampleServerURL is the server URL the messages show as the shape to copy.
const exampleServerURL = "https://cinc.example.com/organizations/your-org"

// explainedError is an error whose message is written for the user, which
// unwraps to the error behind it so errors.Is and errors.As keep working
// (ErrMissingServerURL, fs.ErrNotExist, a toml.ParseError, ...) without
// that error's terser text being appended to the explanation.
type explainedError struct {
	msg string
	err error
}

func (e *explainedError) Error() string { return e.msg }
func (e *explainedError) Unwrap() error { return e.err }

func explain(cause error, format string, args ...any) error {
	return &explainedError{msg: fmt.Sprintf(format, args...), err: cause}
}

// WithName returns p labeled with the section name it was read from, so
// its validation errors can name it. Load does this itself; it is for
// callers that build a Profile from another file, such as migration from
// knife's credentials.
func (p Profile) WithName(name string) Profile {
	p.name = name
	return p
}

// where names the profile for a message: `your "default" profile in
// /home/you/.cinc/credentials`. A profile built in memory rather than loaded
// has neither, and is just "this profile".
func (p Profile) where() string {
	switch {
	case p.name != "" && p.source != "":
		return fmt.Sprintf("your %q profile in %s", p.name, p.source)
	case p.name != "":
		return fmt.Sprintf("your %q profile", p.name)
	}
	return "this profile"
}

// snippet renders an example of the profile's section with one key set,
// indented for a message.
func (p Profile) snippet(key, value string) string {
	name := p.name
	if name == "" {
		name = "default"
	}
	return fmt.Sprintf("  [%s]\n  %s = %q", name, key, value)
}

// serverKey is the key the profile's server URL was read from, so a fix
// names the line the user actually has. A profile with no server URL is
// told about the cinc form.
func (p Profile) serverKey() string {
	if p.serverURLKey != "" {
		return p.serverURLKey
	}
	return "cinc_server_url"
}

// createCommand is the `config create` invocation that rewrites the named
// profile in the file at path, leaving out the flags that match the
// defaults.
func createCommand(profile, path string) string {
	cmd := progname.Get() + " config create"
	if profile != "" && profile != "default" {
		cmd += " --profile " + profile
	}
	if path != "" {
		if def, err := DefaultPath(); err != nil || def != path {
			cmd += " --config " + path
		}
	}
	return cmd
}

func (p Profile) missingClientNameError() error {
	return fmt.Errorf(`%s doesn't set client_name.

Every request %s sends to your Cinc server is signed as a particular client, usually your username or a node's name, and client_name says which one. Add it to the profile, for example:
%s
Or run `+"`%s`"+` to set the profile up again.`,
		p.where(), progname.Get(), p.snippet("client_name", "your-name"), createCommand(p.name, p.source))
}

func (p Profile) missingClientKeyError() error {
	return fmt.Errorf(`%s doesn't set client_key.

client_key is the path to the private key file (a .pem file) that %s uses to sign requests as %q. Your Cinc server only accepts the key that was made for that client. Add the path to the profile, for example:
%s
If you don't have a key yet, ask whoever runs your Cinc server to create one for %q. Or run `+"`%s`"+` to set the profile up again.`,
		p.where(), progname.Get(), p.ClientName,
		p.snippet("client_key", "~/.cinc/"+p.ClientName+".pem"),
		p.ClientName, createCommand(p.name, p.source))
}

func (p Profile) missingServerURLError() error {
	return explain(ErrMissingServerURL, `%s doesn't say which Cinc server to use.

Set cinc_server_url to your server's address, ending with your organization, for example:
%s
(Files shared with knife can use chef_server_url instead.) Or run `+"`%s`"+` to set the profile up again.`,
		p.where(), p.snippet("cinc_server_url", exampleServerURL), createCommand(p.name, p.source))
}

// badServerURLError explains a server URL that is written but unusable:
// either not a URL at all, or one that leaves off the organization.
func (p Profile) badServerURLError(cause error) error {
	key := p.serverKey()
	base, ok := serverURLBase(p.RawServerURL)
	if !ok {
		return explain(cause, `the server URL in %s, %q, isn't a full URL.

%s needs the whole address of your Cinc server, starting with https:// and ending with your organization, for example:
%s`,
			p.where(), p.RawServerURL, key, p.snippet(key, suggestedServerURL(p.RawServerURL)))
	}
	return explain(cause, `the server URL in %s, %s, doesn't say which organization to use.

A Cinc server can host several organizations, and every request goes to one of them, so %s has to end with /organizations/<org>. For example:
%s
If you're not sure of your organization's name, ask whoever runs your Cinc server.`,
		p.where(), p.RawServerURL, key, p.snippet(key, base+"/organizations/your-org"))
}

// ServerURLProblem returns a one-sentence explanation of what is wrong with
// raw as a Cinc Server URL, or nil when it is fine. It suits a report line,
// like a `config validate` check, where a profile's full explanation would
// be too long.
func ServerURLProblem(raw string) error {
	_, _, err := cinc.ParseServerURL(raw)
	if err == nil {
		return nil
	}
	if base, ok := serverURLBase(raw); ok {
		return explain(err, "%s doesn't say which organization to use. Add /organizations/<org> to the end, like %s/organizations/your-org", raw, base)
	}
	return explain(err, "%q isn't a full URL. Use your server's whole address, like %s", raw, suggestedServerURL(raw))
}

// serverURLBase returns scheme://host for a raw URL that has both, which
// means a parse failure was about the organization, not the URL itself.
func serverURLBase(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false
	}
	return u.Scheme + "://" + u.Host, true
}

// suggestedServerURL guesses the URL meant by one that isn't a full URL: a
// bare host such as cinc.example.com gets https:// and an organization.
func suggestedServerURL(raw string) string {
	if !strings.Contains(raw, "://") && raw != "" {
		if base, ok := serverURLBase("https://" + strings.TrimRight(raw, "/")); ok {
			return base + "/organizations/your-org"
		}
	}
	return exampleServerURL
}

func (p Profile) missingSupermarketIdentityError() error {
	return fmt.Errorf(`we need a Supermarket identity to sign your upload, and %s doesn't have one.

Supermarket checks each upload against a username and the private key that belongs to it. Set supermarket_client_name and supermarket_key to your Supermarket username and key, or set client_name and client_key to use the same identity as your Cinc server.`,
		p.where())
}

// unknownProfileError explains a profile name the file doesn't have, listing
// the ones it does. envVar names the environment variable the name came
// from, or is empty when the caller asked for it directly.
func (c *Config) unknownProfileError(name, envVar string) error {
	file := c.path
	if file == "" {
		file = "your credentials file"
	}
	names := make([]string, 0, len(c.Profiles))
	for n := range c.Profiles {
		names = append(names, n)
	}
	create := createCommand(name, c.path)
	if len(names) == 0 {
		return fmt.Errorf(`there's no %q profile in %s. The file doesn't have any profiles yet.

A profile tells %s which Cinc server to talk to and who you are there. Run `+"`%s`"+` to add one.`,
			name, file, progname.Get(), create)
	}
	slices.Sort(names)
	from := ""
	pick := "Pick one of those with --profile"
	if envVar != "" {
		from = fmt.Sprintf(" (%s is set to %q)", envVar, name)
		pick = fmt.Sprintf("Set %s to one of those, or unset it", envVar)
	}
	has := fmt.Sprintf("The profiles it has are %s.", strings.Join(names, ", "))
	if len(names) == 1 {
		has = fmt.Sprintf("The only profile it has is %q.", names[0])
		pick = strings.Replace(pick, "one of those", "that one", 1)
	}
	return fmt.Errorf("there's no %q profile in %s%s. %s\n\n%s, or run `%s` to add a %q profile.",
		name, file, from, has, pick, create, name)
}

// loadError explains why the credentials file at path couldn't be read.
func loadError(path string, err error) error {
	if pathErr, ok := errors.AsType[*fs.PathError](err); ok {
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return explain(err, "there's no credentials file at %s.\n\nThe credentials file holds the profiles %s uses to reach your Cinc server. Check the path, or run `%s` to create one there.",
				path, progname.Get(), createCommand("", path))
		case errors.Is(err, fs.ErrPermission):
			fix := fmt.Sprintf("Make it readable by you and nobody else:\n  chmod 600 %s\nIf another account owns the file, take ownership first with `sudo chown $(whoami) %s`.", path, path)
			if runtime.GOOS == "windows" {
				fix = "Right-click the file, open Properties > Security, and give your account Read access."
			}
			return explain(err, "we can't open the credentials file at %s: your account doesn't have permission to read it.\n\n%s", path, fix)
		}
		return explain(err, "we couldn't open the credentials file at %s: %v.", path, pathErr.Err)
	}
	return syntaxError(path, err)
}

// tomlErrorText matches the text BurntSushi/toml gives a decode error that
// isn't a *toml.ParseError (a value of the wrong type, a key outside any
// table): `toml: line 2 (last key "default.client_name"): <reason>`.
var tomlErrorText = regexp.MustCompile(`^toml: line (\d+)(?: \(last key "([^"]*)"\))?: (.*)$`)

// syntaxError explains a credentials file that isn't valid TOML, or holds
// a value of the wrong shape, pointing at the line to fix.
func syntaxError(path string, err error) error {
	problem, line, ok := tomlProblem(err)
	if !ok {
		return explain(err, "we couldn't read the credentials file at %s: %v.", path, err)
	}
	return explain(err, `we couldn't read the credentials file at %s: %s.

Each profile in the file starts with its name in square brackets, followed by one setting per line, with text values in double quotes:
  [default]
  cinc_server_url = "https://cinc.example.com/organizations/acme"
  client_name     = "tim"
  client_key      = "~/.cinc/tim.pem"
Fix line %d and try again.`, path, problem, line)
}

// TOMLProblem describes a decode error from a credentials file as a phrase
// naming the line at fault, such as `line 2 isn't valid TOML (expected
// value but found "tim" instead)`. It suits the middle of a sentence. An
// error that names no line is returned as its own text.
func TOMLProblem(err error) string {
	if problem, _, ok := tomlProblem(err); ok {
		return problem
	}
	return err.Error()
}

// tomlProblem is TOMLProblem that also returns the line number, with ok
// false when the error names no line.
func tomlProblem(err error) (problem string, line int, ok bool) {
	var lastKey, reason string
	if perr, isParse := errors.AsType[toml.ParseError](err); isParse {
		line, lastKey, reason = perr.Position.Line, perr.LastKey, perr.Message
	} else if m := tomlErrorText.FindStringSubmatch(err.Error()); m != nil {
		line, _ = strconv.Atoi(m[1])
		lastKey, reason = m[2], m[3]
	} else {
		return "", 0, false
	}
	switch {
	case lastKey != "" && !strings.Contains(lastKey, ".") && strings.Contains(reason, "expected table"):
		problem = fmt.Sprintf("line %d sets %s outside any profile, so we don't know which profile it belongs to", line, lastKey)
	case strings.Contains(reason, "incompatible types"):
		problem = fmt.Sprintf("line %d gives %s the wrong kind of value (%s)", line, displayKey(lastKey), reason)
	default:
		problem = fmt.Sprintf("line %d isn't valid TOML (%s)", line, reason)
	}
	return problem, line, true
}

// displayKey renders toml's dotted last key ("default.client_name") as the
// reader sees it in the file: client_name in [default].
func displayKey(key string) string {
	profile, name, ok := strings.Cut(key, ".")
	if !ok {
		return key
	}
	return fmt.Sprintf("%s in [%s]", name, profile)
}
