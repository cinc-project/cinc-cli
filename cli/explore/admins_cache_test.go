package explore

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// adminsServer serves users alice and carol plus an admins group whose GETs
// are counted. status, when set, is the code each admins GET answers with;
// a PUT replaces the group's body, standing in for a membership edit.
type adminsServer struct {
	srv    *httptest.Server
	gets   atomic.Int32
	mu     sync.Mutex
	body   string
	status int
}

func newAdminsServer(t *testing.T, body string) *adminsServer {
	t.Helper()
	a := &adminsServer{body: body}
	mux := http.NewServeMux()
	jsonHandler(mux, "/users/alice", `{"username":"alice"}`)
	jsonHandler(mux, "/users/carol", `{"username":"carol"}`)
	mux.HandleFunc("/organizations/acme/groups/admins", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			a.gets.Add(1)
			if a.status != 0 {
				w.WriteHeader(a.status)
				_, _ = w.Write([]byte(`{"error":["nope"]}`))
				return
			}
			_, _ = w.Write([]byte(a.body))
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			a.body = string(b)
			_, _ = w.Write(b)
		}
	})
	mux.HandleFunc("/organizations/acme/groups/devs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"groupname":"devs"}`))
	})
	a.srv = httptest.NewServer(mux)
	t.Cleanup(a.srv.Close)
	return a
}

func (a *adminsServer) setStatus(code int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status = code
}

// summaryTypeOf runs the kind's Summary for username and returns its Type.
func summaryTypeOf(t *testing.T, k Kind, c *cinc.Client, username string) string {
	t.Helper()
	view, err := k.(Summarizable).Summary(context.Background(), c, username)
	if err != nil {
		t.Fatalf("Summary(%s): %v", username, err)
	}
	for _, f := range view.Fields {
		if f.Label == "Type" {
			return f.Value
		}
	}
	t.Fatalf("no Type field in summary: %+v", view.Fields)
	return ""
}

// Summarizing several users fetches the admins group once, even when the
// summaries run concurrently the way the explorer's tea.Cmds do.
func TestUserSummariesFetchAdminsOnce(t *testing.T) {
	a := newAdminsServer(t, `{"groupname":"admins","users":["alice"]}`)
	c := testClient(t, a.srv)
	k := newUserKind(newAdminsCache())

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			_, _ = k.(Summarizable).Summary(context.Background(), c, name)
		}([]string{"alice", "carol"}[i%2])
	}
	wg.Wait()

	if got := summaryTypeOf(t, k, c, "alice"); got != "Administrator" {
		t.Errorf("alice Type = %q, want Administrator", got)
	}
	if got := summaryTypeOf(t, k, c, "carol"); got != "User" {
		t.Errorf("carol Type = %q, want User", got)
	}
	if got := a.gets.Load(); got != 1 {
		t.Errorf("admins GETs = %d, want 1", got)
	}
}

// Editing the admins group through the explorer drops the cached
// membership, so the next user summary reflects the edit. Editing any
// other group leaves the cache alone.
func TestAdminsGroupEditInvalidatesCache(t *testing.T) {
	a := newAdminsServer(t, `{"groupname":"admins","users":["alice"]}`)
	c := testClient(t, a.srv)
	cache := newAdminsCache()
	users := newUserKind(cache)
	groups := newGroupKind(cache).(Editable)

	if got := summaryTypeOf(t, users, c, "carol"); got != "User" {
		t.Fatalf("carol Type = %q, want User", got)
	}

	if err := groups.Save(context.Background(), c, "devs", []byte(`{"groupname":"devs"}`)); err != nil {
		t.Fatalf("Save devs: %v", err)
	}
	_ = summaryTypeOf(t, users, c, "carol")
	if got := a.gets.Load(); got != 1 {
		t.Fatalf("after editing devs, admins GETs = %d, want 1", got)
	}

	if err := groups.Save(context.Background(), c, "admins",
		[]byte(`{"groupname":"admins","users":["alice","carol"]}`)); err != nil {
		t.Fatalf("Save admins: %v", err)
	}
	if got := summaryTypeOf(t, users, c, "carol"); got != "Administrator" {
		t.Errorf("after edit, carol Type = %q, want Administrator", got)
	}
	if got := a.gets.Load(); got != 2 {
		t.Errorf("admins GETs = %d, want 2", got)
	}
}

// Deleting the admins group also invalidates the cache.
func TestAdminsGroupDeleteInvalidatesCache(t *testing.T) {
	cache := newAdminsCache()
	a := newAdminsServer(t, `{"groupname":"admins","users":["alice"]}`)
	c := testClient(t, a.srv)
	_ = summaryTypeOf(t, newUserKind(cache), c, "alice")

	_ = newGroupKind(cache).(Deletable).Delete(context.Background(), c, "admins")
	_ = summaryTypeOf(t, newUserKind(cache), c, "alice")
	if got := a.gets.Load(); got != 2 {
		t.Errorf("admins GETs = %d, want 2", got)
	}
}

// Deleting a user removes them from the admins group server-side, so the
// Users kind drops the memo too.
func TestUserDeleteInvalidatesAdminsCache(t *testing.T) {
	cache := newAdminsCache()
	a := newAdminsServer(t, `{"groupname":"admins","users":["alice"]}`)
	c := testClient(t, a.srv)
	k := newUserKind(cache)
	_ = summaryTypeOf(t, k, c, "alice")

	_ = k.(Deletable).Delete(context.Background(), c, "carol")
	_ = summaryTypeOf(t, k, c, "alice")
	if got := a.gets.Load(); got != 2 {
		t.Errorf("admins GETs = %d, want 2", got)
	}
}

// A transient failure (here a 500) isn't cached: the next summary tries
// again and picks up the real answer once the server recovers.
func TestAdminsTransientFailureIsRetried(t *testing.T) {
	a := newAdminsServer(t, `{"groupname":"admins","users":["alice"]}`)
	a.setStatus(http.StatusInternalServerError)
	c := testClient(t, a.srv)
	k := newUserKind(newAdminsCache())

	if got := summaryTypeOf(t, k, c, "alice"); got != "Unknown" {
		t.Fatalf("Type = %q, want Unknown", got)
	}
	a.setStatus(0)
	if got := summaryTypeOf(t, k, c, "alice"); got != "Administrator" {
		t.Errorf("after recovery, Type = %q, want Administrator", got)
	}
	// The API client retries a 500 itself, so count only from here: the
	// recovered answer is now cached like any other.
	before := a.gets.Load()
	_ = summaryTypeOf(t, k, c, "carol")
	if got := a.gets.Load(); got != before {
		t.Errorf("after a successful fetch, admins GETs went %d -> %d, want no refetch", before, got)
	}
}

// A 403 or 404 won't change by asking again, so it's cached as Unknown.
func TestAdminsPermanentFailureIsCached(t *testing.T) {
	for _, code := range []int{http.StatusForbidden, http.StatusNotFound} {
		a := newAdminsServer(t, `{}`)
		a.setStatus(code)
		c := testClient(t, a.srv)
		k := newUserKind(newAdminsCache())

		for _, u := range []string{"alice", "carol"} {
			if got := summaryTypeOf(t, k, c, u); got != "Unknown" {
				t.Errorf("%d: %s Type = %q, want Unknown", code, u, got)
			}
		}
		if got := a.gets.Load(); got != 1 {
			t.Errorf("%d: admins GETs = %d, want 1", code, got)
		}
	}
}

// Switching profiles hands the kinds a different client; the cache must
// not answer for one org with another org's admins.
func TestAdminsCacheIsPerClient(t *testing.T) {
	cache := newAdminsCache()
	k := newUserKind(cache)
	first := newAdminsServer(t, `{"groupname":"admins","users":["alice"]}`)
	second := newAdminsServer(t, `{"groupname":"admins","users":[]}`)

	if got := summaryTypeOf(t, k, testClient(t, first.srv), "alice"); got != "Administrator" {
		t.Errorf("first org: Type = %q, want Administrator", got)
	}
	if got := summaryTypeOf(t, k, testClient(t, second.srv), "alice"); got != "User" {
		t.Errorf("second org: Type = %q, want User", got)
	}
}

// The explorer's registry wires the user and group kinds to one shared
// cache, so an admins edit made from the Groups list shows up in the
// Users list's summaries.
func TestRegistrySharesAdminsCache(t *testing.T) {
	var users, groups Kind
	for _, k := range registry() {
		switch k.Title() {
		case "Users":
			users = k
		case "Groups":
			groups = k
		}
	}
	a := newAdminsServer(t, `{"groupname":"admins","users":["alice"]}`)
	c := testClient(t, a.srv)

	_ = summaryTypeOf(t, users, c, "carol")
	_ = summaryTypeOf(t, users, c, "alice")
	if got := a.gets.Load(); got != 1 {
		t.Fatalf("admins GETs = %d, want 1", got)
	}
	if err := groups.(Editable).Save(context.Background(), c, "admins",
		[]byte(`{"groupname":"admins","users":["carol"]}`)); err != nil {
		t.Fatalf("Save admins: %v", err)
	}
	if got := summaryTypeOf(t, users, c, "carol"); got != "Administrator" {
		t.Errorf("after edit, carol Type = %q, want Administrator", got)
	}
}
