package explore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func TestGroupSummaryFields(t *testing.T) {
	g := &cinc.Group{
		Name:    "admins",
		Users:   []string{"alice", "bob"},
		Clients: []string{"web01"},
		Groups:  nil,
	}
	got := map[string]string{}
	for _, f := range groupSummaryFields(g) {
		got[f.Label] = f.Value
	}
	want := map[string]string{"Users": "2", "Clients": "1", "Nested Groups": "0"}
	for label, val := range want {
		if got[label] != val {
			t.Errorf("field %q = %q, want %q", label, got[label], val)
		}
	}
}

// The Groups kind is Summarizable and shows membership counts rather than
// the nothing it used to show on hover.
func TestGroupKindSummaryShowsFields(t *testing.T) {
	mux := http.NewServeMux()
	jsonHandler(mux, "/organizations/acme/groups/admins",
		`{"groupname":"admins","users":["alice","bob"],"clients":["web01"]}`)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	k, ok := newGroupKind().(Summarizable)
	if !ok {
		t.Fatal("group kind is not Summarizable")
	}
	view, err := k.Summary(context.Background(), testClient(t, srv), "admins")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	got := map[string]string{}
	for _, f := range view.Fields {
		got[f.Label] = f.Value
	}
	if got["Users"] != "2" || got["Clients"] != "1" {
		t.Errorf("fields = %+v, want Users=2 Clients=1", view.Fields)
	}
}
