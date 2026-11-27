package client

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	cinc "github.com/cinc-project/cinc-api"

	"github.com/cinc-project/cinc-cli/cli/config"
	"github.com/cinc-project/cinc-cli/cli/progname"
)

// Target names who a request was signed as and where it went: the profile
// a command built its client from. ExplainFor uses it to name the client,
// server, key and credentials file in a server error. Every field may be
// empty; a command that never built a client has no target.
type Target struct {
	Profile    string // profile name, e.g. "default"
	ConfigPath string // credentials file the profile came from
	ServerURL  string // server root, without /organizations/<org>
	ClientName string
	KeyPath    string // client_key with ~ expanded
}

// TargetFor describes profile p, named profile in the credentials file at
// configPath.
func TargetFor(profile, configPath string, p config.Profile) Target {
	keyPath := p.KeyPath
	if expanded, err := config.ExpandHome(keyPath); err == nil {
		keyPath = expanded
	}
	return Target{
		Profile:    profile,
		ConfigPath: configPath,
		ServerURL:  p.ServerURL,
		ClientName: p.ClientName,
		KeyPath:    keyPath,
	}
}

// ExplainFor is Explain plus the errors the server itself returned: a 400,
// 401, 403, 404, 409, 413 or 5xx is retold as what went wrong, which
// client, org and object it concerns (from t and the request path), and
// what to do about it. The server's own message and the request stay in
// the text.
//
// Only an error whose text still carries the server's response verbatim is
// rewritten, so a command that already explained the failure in its own
// words (the acl commands do) is left alone, while a plain prefix such as
// "explore: " is kept. The result wraps err, so errors.Is and errors.As
// see everything they did before.
func ExplainFor(err error, t Target) error {
	if explained := Explain(err); explained != err {
		return explained
	}
	er, ok := errors.AsType[*cinc.ErrorResponse](err)
	if !ok {
		return err
	}
	raw, full := er.Error(), err.Error()
	if !strings.Contains(full, raw) {
		return err
	}
	msg := explainResponse(er, t)
	if msg == "" {
		return err
	}
	return &explainedError{err: err, msg: strings.Replace(full, raw, msg, 1)}
}

// explainResponse is the explanation for one server response, or "" when
// there's nothing to add to the status.
func explainResponse(er *cinc.ErrorResponse, t Target) string {
	e := responseExplainer{er: er, t: t, s: subjectFor(er.Method, er.Path), prog: progname.Get()}
	switch code := er.StatusCode; {
	case code == 400:
		return e.badRequest()
	case code == 401:
		return e.unauthorized()
	case code == 403:
		return e.forbidden()
	case code == 404:
		return e.notFound()
	case code == 409:
		return e.conflict()
	case code == 413:
		return e.tooLarge()
	case code >= 500 && code <= 599:
		return e.serverFailed()
	}
	return ""
}

type responseExplainer struct {
	er   *cinc.ErrorResponse
	t    Target
	s    subject
	prog string
}

// said is the line that keeps the server's own words and the request in
// the message.
func (e responseExplainer) said() string {
	msg := e.er.ServerMessage()
	if msg == "" {
		msg = "(no message)"
	}
	return fmt.Sprintf("The server said: %s (%d from %s %s)", msg, e.er.StatusCode, e.er.Method, e.er.Path)
}

// server is "the Cinc server at URL", or just "the Cinc server".
func (e responseExplainer) server() string {
	if e.t.ServerURL != "" {
		return "the Cinc server at " + e.t.ServerURL
	}
	return "the Cinc server"
}

// on is " on URL", or "" when the server isn't known.
func (e responseExplainer) on() string {
	if e.t.ServerURL != "" {
		return " on " + e.t.ServerURL
	}
	return ""
}

// actor is the name the request was signed as: the one the server quoted
// back, else the profile's client_name.
func (e responseExplainer) actor() string {
	if m := quotedName.FindStringSubmatch(e.er.ServerMessage()); m != nil {
		return m[1]
	}
	return e.t.ClientName
}

// who is the signer quoted for a sentence, or "you".
func (e responseExplainer) who() string {
	if a := e.actor(); a != "" {
		return fmt.Sprintf("%q", a)
	}
	return "you"
}

// actorArg is the signer as a command argument, or placeholder.
func (e responseExplainer) actorArg(placeholder string) string {
	if a := e.actor(); a != "" {
		return a
	}
	return placeholder
}

// profile is `your "default" profile in /path`, or as much of it as is
// known.
func (e responseExplainer) profile() string {
	switch {
	case e.t.Profile != "" && e.t.ConfigPath != "":
		return fmt.Sprintf("your %q profile in %s", e.t.Profile, e.t.ConfigPath)
	case e.t.Profile != "":
		return fmt.Sprintf("your %q profile", e.t.Profile)
	}
	return "your profile"
}

// inOrg is ` in organization "acme"`, or "" when the request wasn't under
// an organization (or was about the organization itself).
func (e responseExplainer) inOrg() string {
	if e.s.org == "" || e.s.kind == "organization" {
		return ""
	}
	return fmt.Sprintf(" in organization %q", e.s.org)
}

// cmd is a runnable command line with the program name in front.
func (e responseExplainer) cmd(args string) string {
	return "`" + e.prog + " " + args + "`"
}

// quotedName picks the actor out of the 401 and 403 messages that quote it:
// "Invalid signature for user or client 'tim'", "'tim' not associated with
// organization 'acme'".
var quotedName = regexp.MustCompile(`'([^']+)'`)

// missingPerm picks the permission out of "missing delete permission".
var missingPerm = regexp.MustCompile(`missing (\w+) permission`)

func (e responseExplainer) badRequest() string {
	msg := strings.TrimSpace(e.er.ServerMessage())
	if msg == "" {
		return fmt.Sprintf("the server refused the request as invalid without saying why (400 from %s %s). Check the names and values you gave and try again.",
			e.er.Method, e.er.Path)
	}
	return fmt.Sprintf("the server refused the request: %s\n\n(400 from %s %s) Fix what the server pointed out and try again.",
		msg, e.er.Method, e.er.Path)
}

func (e responseExplainer) unauthorized() string {
	lower := strings.ToLower(e.er.ServerMessage())
	who := e.who()
	key := "the client key"
	if e.t.KeyPath != "" {
		key = "the client key at " + e.t.KeyPath
	}
	switch {
	case strings.Contains(lower, "time stamp") || strings.Contains(lower, "timestamp"):
		return fmt.Sprintf(`%s turned the request away because this computer's clock doesn't agree with the server's.

%s

Every signed request carries the time it was made, and the server refuses one whose time is too far from its own (15 minutes on a default install), so a captured request can't be replayed later. Turn on automatic time sync on this computer, check its time zone, and try again. If this computer's clock is right, the server's clock is the one that's off, so let whoever runs the server know.`,
			e.server(), e.said())

	case strings.Contains(lower, "invalid signature"):
		return fmt.Sprintf(`%s didn't accept the signature on the request from %s.

%s

%s signs every request with a client key, a private key file the server checks against the public key it has on file for %s. The server knows %s, but %s (client_key in %s) isn't the key it has for them. Here's how to fix it:
  - If you have the right key for %s somewhere else, point client_key at it.
  - If you don't, ask whoever runs your Cinc server for a new one: they can run %s for a client, or %s for a user. Save the .pem file they send you somewhere private and point client_key at it.`,
			e.server(), who, e.said(),
			e.prog, who, who, key, e.profile(),
			who, e.cmd("client reregister "+e.actorArg("<name>")), e.cmd("user key create "+e.actorArg("<name>")+" <key-name>"))

	case strings.Contains(lower, "failed to authenticate"):
		return fmt.Sprintf(`%s doesn't know a client or user called %s%s.

%s

Every request is signed as a client or user that already exists on the server, and %s is the client_name in %s. Here's what to check:
  - Is client_name spelled exactly the way the server has it? Names are case-sensitive.
  - Is the organization right? A client only exists in the organization it was created in, and the organization comes from the server URL in your profile (the part after /organizations/).
  - If %s is new, ask whoever runs your Cinc server to create it (%s) and send you its key.`,
			e.server(), who, e.inOrg(), e.said(),
			who, e.profile(),
			who, e.cmd("client create "+e.actorArg("<name>")))
	}
	return fmt.Sprintf(`%s couldn't confirm that the request came from %s.

%s

That usually means one of these:
  - %s (client_key in %s) isn't the key the server has for %s.
  - The server has no client or user called %s%s, so check client_name in your profile.
  - This computer's clock is more than a few minutes off from the server's. Turn on automatic time sync and try again.`,
		e.server(), who, e.said(),
		capitalize(key), e.profile(), who,
		who, e.inOrg())
}

func (e responseExplainer) forbidden() string {
	msg := e.er.ServerMessage()
	if strings.Contains(msg, "not associated with organization") && e.s.org != "" {
		return fmt.Sprintf(`%s isn't a member of organization %q%s.

%s

A user can only work in the organizations they belong to. Ask an admin of %q to add you (%s) or invite you (%s). If you meant a different organization, fix the org in the server URL of %s.`,
			e.who(), e.s.org, e.on(), e.said(),
			e.s.org, e.cmd("org member add "+e.actorArg("<user>")), e.cmd("org invite create "+e.actorArg("<user>")), e.profile())
	}

	perm := permFor(e.er.Method)
	if m := missingPerm.FindStringSubmatch(msg); m != nil {
		perm = m[1]
	}
	s := e.s
	var action, steps string
	switch {
	case s.cmd == "":
		action = fmt.Sprintf("make this request (%s %s)", e.er.Method, e.er.Path)
		steps = fmt.Sprintf("Ask an admin of the organization to give %s %s permission on it.", e.who(), perm)
	case s.name == "":
		action = fmt.Sprintf("%s %s%s", verbFor(e.er.Method, false), s.plural, e.inOrg())
		steps = fmt.Sprintf("Ask an admin of the organization to give %s %s permission on %s, usually by adding it to a group that has it (%s).",
			e.who(), perm, s.plural, e.cmd("group member add <group> "+e.actorArg("<name>")))
	default:
		action = fmt.Sprintf("%s %s%s", verbFor(e.er.Method, true), s.phrase(), e.inOrg())
		steps = fmt.Sprintf("Ask an admin of the organization, or anyone with grant permission on %s, to give %s %s permission.", s.phrase(), e.who(), perm)
		if s.acl != "" {
			steps += fmt.Sprintf(" They can see who has access with %s and add you with %s (use --user instead of --client if %s is a user).",
				e.cmd(s.cmd+" acl show "+s.acl), e.cmd(s.cmd+" acl grant "+perm+" "+s.acl+" --client "+e.actorArg("<name>")), e.who())
		}
	}
	return fmt.Sprintf(`%s isn't allowed to %s.

%s

The server knows who you are; it's the permissions that stop you. Each object has an access list (ACL) saying who may create, read, update, delete, or grant on it. %s`,
		e.who(), action, e.said(), steps)
}

func (e responseExplainer) notFound() string {
	lower := strings.ToLower(e.er.ServerMessage())
	s := e.s
	if s.org != "" && (strings.Contains(lower, "cannot find org") || strings.Contains(lower, "cannot find organization") ||
		(strings.Contains(lower, "organization") && strings.Contains(lower, "does not exist"))) {
		return fmt.Sprintf(`organization %q doesn't exist%s.

%s

Everything on a Cinc server lives inside an organization, and the organization comes from the server URL in %s (the part after /organizations/). Run %s to see the organizations you can use, then fix the org name in that URL.`,
			s.org, e.on(), e.said(), e.profile(), e.cmd("org list"))
	}
	switch {
	case s.kind == "member" && s.name != "":
		return fmt.Sprintf("%q isn't a member of organization %q%s.\n\n%s\n\nRun %s to see who is.",
			s.name, s.org, e.on(), e.said(), e.cmd("org member list"))
	case s.name != "" && s.cmd != "":
		return fmt.Sprintf("we couldn't find %s%s%s.\n\n%s\n\nCheck the spelling, or run %s to see what's there.",
			s.phrase(), e.inOrg(), e.on(), e.said(), e.cmd(s.list))
	}
	return fmt.Sprintf("%s has nothing at %s %s.\n\n%s\n\nIf you expected something there, check the names you gave, and that the server URL in %s points at your Cinc server.",
		e.server(), e.er.Method, e.er.Path, e.said(), e.profile())
}

func (e responseExplainer) conflict() string {
	s := e.s
	switch {
	case strings.Contains(e.er.ServerMessage(), "is frozen") && s.name != "":
		return fmt.Sprintf("%s is frozen, so it can't be replaced.\n\n%s\n\nBump the version in the cookbook's metadata.rb and upload it again.",
			s.phrase(), e.said())
	case s.kind == "member":
		return fmt.Sprintf("that user is already a member of organization %q, or already invited to it.\n\n%s\n\nRun %s to see who's in it.",
			s.org, e.said(), e.cmd("org member list"))
	case s.cmd == "":
		return fmt.Sprintf("that already exists on the server.\n\n%s\n\nPick a different name and try again.", e.said())
	case s.name == "":
		return fmt.Sprintf("%s%s with that name already exists%s.\n\n%s\n\nPick a different name, or run %s to see the ones that are there.",
			article(s.kind), s.parent, e.inOrg(), e.said(), e.cmd(s.list))
	}
	return fmt.Sprintf("%s already exists%s.\n\n%s\n\nPick a different name, or run %s to see the ones that are there.",
		s.phrase(), e.inOrg(), e.said(), e.cmd(s.list))
}

func (e responseExplainer) tooLarge() string {
	return fmt.Sprintf("the request was too big for %s to accept.\n\n%s\n\nThe server (or a proxy in front of it) caps how large one request can be. Make what you're sending smaller (large node attributes and data bag items are the usual culprits), or ask whoever runs the server to raise the limit.",
		e.server(), e.said())
}

func (e responseExplainer) serverFailed() string {
	what := "ran into a problem handling the request"
	switch e.er.StatusCode {
	case 502, 504:
		what = "didn't give the proxy in front of it a usable answer"
	case 503:
		what = "is unavailable right now"
	}
	return fmt.Sprintf("%s %s. This is not a problem with your command or your setup.\n\n%s\n\nWait a moment and try again. If it keeps happening, whoever runs the server can find the cause in its logs.",
		e.server(), what, e.said())
}

// subject is what a request path is about, worked out from the path alone:
// /organizations/acme/nodes/web1 is node "web1" in org "acme".
type subject struct {
	org    string
	kind   string // "node", "item", "version", "member", ... or "" when unknown
	plural string // "nodes", for a request on the whole collection
	name   string // "" for a collection
	parent string // ` in data bag "creds"`, ` of client "web1"`
	bare   bool   // print name unquoted ("version 1.2.3")
	cmd    string // the CLI noun: "node", "databag", "policy-group"; "" when unknown
	list   string // command (without the program name) that lists its siblings
	acl    string // argument to `<cmd> acl show`, when the object has an ACL
}

// phrase is `node "web1"`, `item "db" in data bag "creds"`, or
// `version 1.2.3 of cookbook "apache2"`.
func (s subject) phrase() string {
	name := fmt.Sprintf("%q", s.name)
	if s.bare {
		name = s.name
	}
	return s.kind + " " + name + s.parent
}

// collections maps an org-scoped collection segment to its CLI noun.
var collections = map[string]struct{ kind, plural, cmd string }{
	"nodes":         {"node", "nodes", "node"},
	"clients":       {"client", "clients", "client"},
	"roles":         {"role", "roles", "role"},
	"environments":  {"environment", "environments", "environment"},
	"groups":        {"group", "groups", "group"},
	"cookbooks":     {"cookbook", "cookbooks", "cookbook"},
	"policies":      {"policy", "policies", "policy"},
	"policy_groups": {"policy group", "policy groups", "policy-group"},
	"data":          {"data bag", "data bags", "databag"},
}

// subjectFor works out what method on path was about. A POST creates a
// child of path, so its subject is that unnamed child: POST /nodes is about
// "a node". A path it doesn't recognize gives a subject with no cmd.
func subjectFor(method, path string) subject {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	var segs []string
	for _, raw := range strings.Split(strings.Trim(path, "/"), "/") {
		seg, err := url.PathUnescape(raw)
		if err != nil {
			seg = raw
		}
		segs = append(segs, seg)
	}
	// An ACL request is about the object it belongs to.
	for i, seg := range segs {
		if seg == "_acl" {
			segs = segs[:i]
			break
		}
	}
	if method == "POST" {
		segs = append(segs, "")
	}
	if len(segs) < 2 {
		return subject{}
	}
	switch segs[0] {
	case "users":
		return userSubject(segs[1:])
	case "organizations":
		if len(segs) == 2 {
			return subject{org: segs[1], kind: "organization", plural: "organizations", name: segs[1], cmd: "org", list: "org list"}
		}
		s := orgSubject(segs[2:])
		s.org = segs[1]
		return s
	}
	return subject{}
}

func userSubject(rest []string) subject {
	switch {
	case len(rest) == 1:
		return subject{kind: "user", plural: "users", name: rest[0], cmd: "user", list: "user list", acl: rest[0]}
	case len(rest) == 3 && rest[1] == "keys":
		return subject{kind: "key", plural: "keys", name: rest[2], parent: fmt.Sprintf(" of user %q", rest[0]),
			cmd: "user key", list: "user key list " + rest[0]}
	case len(rest) == 2 && rest[1] != "":
		// A collection under the user (its keys, its orgs): a 404 there
		// means the user is missing.
		return userSubject(rest[:1])
	}
	return subject{}
}

func orgSubject(rest []string) subject {
	if rest[0] == "users" {
		if len(rest) == 2 {
			return subject{kind: "member", plural: "members", name: rest[1], cmd: "org member", list: "org member list"}
		}
		return subject{}
	}
	c, ok := collections[rest[0]]
	if !ok {
		return subject{}
	}
	switch len(rest) {
	case 1:
		return subject{kind: c.kind, plural: c.plural, cmd: c.cmd, list: c.cmd + " list"}
	case 2:
		return subject{kind: c.kind, plural: c.plural, cmd: c.cmd, list: c.cmd + " list", name: rest[1], acl: rest[1]}
	}
	parent := rest[1]
	switch {
	case rest[0] == "data" && len(rest) == 3:
		return subject{kind: "item", plural: "items", name: rest[2], parent: fmt.Sprintf(" in data bag %q", parent),
			cmd: "databag item", list: "databag item list " + parent}
	case rest[0] == "cookbooks" && len(rest) == 3:
		return subject{kind: "version", plural: "versions", name: rest[2], bare: true, parent: fmt.Sprintf(" of cookbook %q", parent),
			cmd: "cookbook", list: "cookbook show " + parent}
	case rest[0] == "clients" && len(rest) == 4 && rest[2] == "keys":
		return subject{kind: "key", plural: "keys", name: rest[3], parent: fmt.Sprintf(" of client %q", parent),
			cmd: "client key", list: "client key list " + parent}
	case rest[0] == "policies" && len(rest) == 4 && rest[2] == "revisions":
		return subject{kind: "revision", plural: "revisions", name: rest[3], bare: true, parent: fmt.Sprintf(" of policy %q", parent),
			cmd: "policy", list: "policy show " + parent}
	case rest[0] == "policy_groups" && len(rest) == 4 && rest[2] == "policies":
		return subject{kind: "policy", plural: "policies", name: rest[3], parent: fmt.Sprintf(" in policy group %q", parent),
			cmd: "policy-group", list: "policy-group show " + parent}
	case len(rest) == 3 && rest[2] != "":
		// A collection under the object (a client's keys, an
		// environment's cookbooks): a 404 there means the object is missing.
		return orgSubject(rest[:2])
	}
	return subject{}
}

// permFor is the ACL permission a request method needs.
func permFor(method string) string {
	switch method {
	case "POST":
		return "create"
	case "PUT":
		return "update"
	case "DELETE":
		return "delete"
	}
	return "read"
}

// verbFor is what a request method does, for "isn't allowed to <verb> ...".
// named says whether it acts on one object or the whole collection.
func verbFor(method string, named bool) string {
	switch method {
	case "POST":
		return "create"
	case "PUT":
		return "change"
	case "DELETE":
		return "delete"
	}
	if named {
		return "read"
	}
	return "list"
}

// article is "a node" or "an environment".
func article(noun string) string {
	if noun != "" && strings.ContainsRune("aeiou", rune(noun[0])) {
		return "an " + noun
	}
	return "a " + noun
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
