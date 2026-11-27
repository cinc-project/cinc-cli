package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	cinc "github.com/cinc-project/cinc-api"

	"github.com/cinc-project/cinc-cli/cli/progname"
)

// serverError sends one signed request to an httptest server that answers
// status with body, and returns the error cinc-api built from it, so each
// case explains exactly what the real client hands the CLI.
func serverError(t *testing.T, method, path string, status int, body string) (error, Target) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	p := profileFor(t, srv)
	c, err := New(p)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var reqBody []byte
	if method != http.MethodGet && method != http.MethodDelete {
		reqBody = []byte(`{}`)
	}
	_, _, err = c.Do(ctx, method, path, reqBody)
	if err == nil {
		t.Fatalf("%s %s: want a %d error", method, path, status)
	}
	return err, TargetFor("default", "/home/tim/.cinc/credentials", p)
}

// wantMentions fails unless msg contains every want.
func wantMentions(t *testing.T, msg string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(msg, want) {
			t.Errorf("explained error should mention %q:\n%s", want, msg)
		}
	}
}

// explainServer explains raw for target, checking the result still wraps
// the server's response for errors.Is and errors.As.
func explainServer(t *testing.T, raw error, target Target) string {
	t.Helper()
	got := ExplainFor(raw, target)
	if got == raw {
		t.Fatalf("ExplainFor left the %v unchanged", raw)
	}
	if !errors.Is(got, raw) {
		t.Error("the explained error should still wrap the original")
	}
	if _, ok := errors.AsType[*cinc.ErrorResponse](got); !ok {
		t.Error("the explained error should still unwrap to the *cinc.ErrorResponse")
	}
	return got.Error()
}

// The 404 bodies below are the two servers' wording for the same thing:
// erchef's "Cannot load node web1" and cinc-server-ng's "Cannot find nodes
// web1". The explanation comes from the path, so either works.
func TestExplainNotFoundNamesTheObjectAndTheListCommand(t *testing.T) {
	for _, body := range []string{`{"error":["Cannot load node web1"]}`, `{"error":["Cannot find nodes web1"]}`} {
		raw, target := serverError(t, "GET", "/organizations/acme/nodes/web1", 404, body)
		msg := explainServer(t, raw, target)
		wantMentions(t, msg,
			`we couldn't find node "web1" in organization "acme"`,
			target.ServerURL,
			progname.Get()+" node list",
			// The server's own words and the request stay visible.
			strings.TrimSuffix(strings.TrimPrefix(body, `{"error":["`), `"]}`),
			"404", "GET /organizations/acme/nodes/web1")
	}
}

func TestExplainNotFoundShapes(t *testing.T) {
	prog := progname.Get()
	for _, tc := range []struct {
		path string
		body string
		want []string
	}{
		{"/organizations/acme/data/creds/db", `{"error":["Cannot load data bag item db for data bag creds"]}`,
			[]string{`item "db" in data bag "creds"`, prog + " databag item list creds"}},
		{"/organizations/acme/data/creds", `{"error":["Cannot load data bag creds"]}`,
			[]string{`data bag "creds"`, prog + " databag list"}},
		{"/organizations/acme/cookbooks/apache2/1.2.3", `{"error":["Cannot find a cookbook named apache2 with version 1.2.3"]}`,
			[]string{`version 1.2.3 of cookbook "apache2"`, prog + " cookbook show apache2"}},
		{"/organizations/acme/clients/web1/keys/old", `{"error":["Cannot find key old"]}`,
			[]string{`key "old" of client "web1"`, prog + " client key list web1"}},
		{"/organizations/acme/policy_groups/prod", `{"error":["Cannot find policy group prod"]}`,
			[]string{`policy group "prod"`, prog + " policy-group list"}},
		{"/organizations/acme/users/bob", `{"error":["Cannot find a user bob in organization acme"]}`,
			[]string{`"bob" isn't a member of organization "acme"`, prog + " org member list"}},
		{"/users/bob", `{"error":["Cannot load user bob"]}`,
			[]string{`we couldn't find user "bob"`, prog + " user list"}},
		{"/organizations/acme/nodes/web%201", `{"error":["Cannot load node web 1"]}`,
			[]string{`node "web 1"`}},
		// A 404 on a collection under an object means the object is missing.
		{"/organizations/acme/clients/web1/keys", `{"error":["Cannot find clients web1"]}`,
			[]string{`we couldn't find client "web1"`, prog + " client list"}},
		{"/users/bob/keys", `{"error":["Cannot find users bob"]}`,
			[]string{`we couldn't find user "bob"`, prog + " user list"}},
	} {
		raw, target := serverError(t, "GET", tc.path, 404, tc.body)
		wantMentions(t, explainServer(t, raw, target), tc.want...)
	}
}

// A missing organization answers 404 to every request under it, so the
// node (or whatever was asked for) is not the thing to look for: the org in
// the profile's server URL is. erchef and cinc-server-ng word it differently.
func TestExplainNotFoundOrganization(t *testing.T) {
	for _, body := range []string{`{"error":["Cannot find org acme"]}`, `{"error":"organization 'acme' does not exist."}`} {
		raw, target := serverError(t, "GET", "/organizations/acme/nodes", 404, body)
		msg := explainServer(t, raw, target)
		wantMentions(t, msg, `organization "acme"`, "doesn't exist", target.ServerURL,
			`"default" profile`, target.ConfigPath, progname.Get()+" org list")
		if strings.Contains(msg, "node list") {
			t.Errorf("a missing org isn't a missing node:\n%s", msg)
		}
	}
}

func TestExplainUnauthorizedWrongKey(t *testing.T) {
	raw, target := serverError(t, "GET", "/organizations/acme/nodes", 401,
		`{"error":["Invalid signature for user or client 'tim'"]}`)
	msg := explainServer(t, raw, target)
	wantMentions(t, msg, `"tim"`, target.ServerURL, "client key", target.KeyPath,
		`"default" profile`, target.ConfigPath, "Invalid signature for user or client 'tim'", "401",
		progname.Get()+" client reregister tim")
}

func TestExplainUnauthorizedUnknownClient(t *testing.T) {
	raw, target := serverError(t, "GET", "/organizations/acme/nodes", 401,
		`{"error":["Failed to authenticate as 'tim'. Ensure that your node_name and client key are correct."]}`)
	msg := explainServer(t, raw, target)
	wantMentions(t, msg, `doesn't know a client or user called "tim"`, `organization "acme"`,
		target.ServerURL, "client_name", target.ConfigPath, "Failed to authenticate as 'tim'", "401")
}

func TestExplainUnauthorizedClockSkew(t *testing.T) {
	raw, target := serverError(t, "GET", "/organizations/acme/nodes", 401,
		`{"error":["Original request time stamp too far in the future or past."]}`)
	msg := explainServer(t, raw, target)
	wantMentions(t, msg, "clock", "time stamp too far", "401")
	if strings.Contains(msg, "client_name") {
		t.Errorf("a clock problem shouldn't send the user to check client_name:\n%s", msg)
	}
}

// An unrecognized 401 still gets every likely cause.
func TestExplainUnauthorizedOther(t *testing.T) {
	raw, target := serverError(t, "GET", "/organizations/acme/nodes", 401, `{"error":["nope"]}`)
	wantMentions(t, explainServer(t, raw, target), `"tim"`, "client key", "client_name", "clock", "nope")
}

func TestExplainForbidden(t *testing.T) {
	raw, target := serverError(t, "DELETE", "/organizations/acme/nodes/web1", 403,
		`{"error":["missing delete permission"]}`)
	msg := explainServer(t, raw, target)
	wantMentions(t, msg, `"tim" isn't allowed to delete node "web1" in organization "acme"`,
		"missing delete permission", "403", progname.Get()+" node acl show web1", "grant")
}

func TestExplainForbiddenNotAMember(t *testing.T) {
	raw, target := serverError(t, "GET", "/organizations/acme/nodes", 403,
		`{"error":["'tim' not associated with organization 'acme'"]}`)
	msg := explainServer(t, raw, target)
	wantMentions(t, msg, `"tim" isn't a member of organization "acme"`, progname.Get()+" org invite create tim",
		"not associated")
}

func TestExplainConflict(t *testing.T) {
	raw, target := serverError(t, "POST", "/organizations/acme/nodes", 409, `{"error":["Object already exists"]}`)
	msg := explainServer(t, raw, target)
	wantMentions(t, msg, `a node with that name already exists in organization "acme"`, "Object already exists", "409",
		progname.Get()+" node list")
}

func TestExplainConflictUnderAParent(t *testing.T) {
	raw, target := serverError(t, "POST", "/organizations/acme/clients/web1/keys", 409, `{"error":["Key already exists"]}`)
	wantMentions(t, explainServer(t, raw, target), `a key of client "web1" with that name already exists in organization "acme"`,
		progname.Get()+" client key list web1")
}

func TestExplainBadRequestLeadsWithTheServerMessage(t *testing.T) {
	raw, target := serverError(t, "POST", "/organizations/acme/clients", 400,
		`{"error":["Invalid public key"]}`)
	msg := explainServer(t, raw, target)
	if !strings.HasPrefix(msg, `the server refused the request: Invalid public key`) {
		t.Errorf("a 400 should lead with the server's own message:\n%s", msg)
	}
	wantMentions(t, msg, "400", "POST /organizations/acme/clients")
}

func TestExplainTooLarge(t *testing.T) {
	raw, target := serverError(t, "PUT", "/organizations/acme/nodes/web1", 413,
		`{"error":"JSON must be no more than 1000000 bytes."}`)
	wantMentions(t, explainServer(t, raw, target), "too big", "JSON must be no more than 1000000 bytes.", "413")
}

func TestExplainServerFailure(t *testing.T) {
	for _, status := range []int{500, 502, 503} {
		raw, target := serverError(t, "POST", "/organizations/acme/nodes", status, `{"error":["internal service error"]}`)
		msg := explainServer(t, raw, target)
		wantMentions(t, msg, target.ServerURL, "not a problem with your command", "try again", "logs",
			"internal service error", fmt.Sprint(status))
	}
}

// A caller that only prefixed the server's error ("explore: ...") keeps its
// prefix; the explanation replaces the raw text after it.
func TestExplainKeepsAPrefixedWrapper(t *testing.T) {
	raw, target := serverError(t, "GET", "/organizations/acme/roles/web", 404, `{"error":["Cannot load role web"]}`)
	wrapped := fmt.Errorf("explore: %w", raw)
	got := ExplainFor(wrapped, target)
	msg := got.Error()
	if !strings.HasPrefix(msg, "explore: ") || !strings.Contains(msg, `role "web"`) {
		t.Errorf("want the prefix kept and the 404 explained:\n%s", msg)
	}
	if !errors.Is(got, cinc.ErrNotFound) {
		t.Error("the explained error should still match cinc.ErrNotFound")
	}
}

// rewordedError is a command's own retelling of a server error, like the
// acl commands' aclError.
type rewordedError struct{ err error }

func (e *rewordedError) Error() string { return "you don't have grant permission on node \"web1\"" }
func (e *rewordedError) Unwrap() error { return e.err }

// A command that already explained the error in its own words isn't
// explained a second time.
func TestExplainLeavesAnAlreadyExplainedServerErrorAlone(t *testing.T) {
	raw, target := serverError(t, "GET", "/organizations/acme/nodes/web1/_acl", 403, `{"error":["missing grant permission"]}`)
	reworded := &rewordedError{err: raw}
	if got := ExplainFor(reworded, target); got != reworded {
		t.Errorf("ExplainFor rewrote an error a command already explained: %v", got)
	}
}

// Without a target (the command never built a client from a profile) the
// explanation still works; it just can't name the profile.
func TestExplainWithoutTarget(t *testing.T) {
	raw, _ := serverError(t, "GET", "/organizations/acme/nodes", 401,
		`{"error":["Invalid signature for user or client 'tim'"]}`)
	msg := explainServer(t, raw, Target{})
	wantMentions(t, msg, `"tim"`, "client key")
	if strings.Contains(msg, `"" profile`) {
		t.Errorf("an empty target shouldn't print an empty profile name:\n%s", msg)
	}
}

// Statuses with nothing to add, and Explain itself, leave server errors as
// they were.
func TestExplainForLeavesOtherStatusesAlone(t *testing.T) {
	raw, target := serverError(t, "GET", "/organizations/acme/nodes", 418, `{"error":["teapot"]}`)
	if got := ExplainFor(raw, target); got != raw {
		t.Errorf("ExplainFor(418) = %v, want it unchanged", got)
	}
	raw, _ = serverError(t, "GET", "/organizations/acme/nodes/web1", 404, `{"error":["Cannot load node web1"]}`)
	if got := Explain(raw); got != raw {
		t.Errorf("Explain(404) = %v, want it unchanged; ExplainFor is the one that explains server errors", got)
	}
}
