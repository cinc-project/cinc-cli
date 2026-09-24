package explore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestKindCapabilities pins the capability matrix: each kind must
// advertise exactly the actions the design calls for, since the action
// bar is driven by these interface assertions.
func TestKindCapabilities(t *testing.T) {
	type caps struct {
		view, edit, create, named, del, download, drill bool
	}
	cases := []struct {
		name string
		k    Kind
		want caps
	}{
		{"node", newNodeKind(), caps{view: true, edit: true, create: true, del: true}},
		{"role", newRoleKind(), caps{view: true, edit: true, create: true, del: true}},
		{"environment", newEnvironmentKind(), caps{view: true, edit: true, create: true, del: true}},
		{"client", newClientKind(), caps{view: true, edit: true, create: true, del: true}},
		{"user", newUserKind(), caps{view: true, edit: true, create: true, del: true}},
		{"group", newGroupKind(), caps{view: true, edit: true, named: true, del: true}},
		{"databag", dataBagKind{}, caps{named: true, del: true, drill: true}},
		{"databag item", dataBagItemsKind{bag: "b"}, caps{view: true, edit: true, create: true, del: true}},
		{"cookbook", cookbookKind{}, caps{drill: true}},
		{"cookbook version", cookbookVersionsKind{name: "c"}, caps{view: true, del: true, download: true}},
		{"policy", policyKind{}, caps{del: true, drill: true}},
		{"policy revision", policyRevisionsKind{policy: "p"}, caps{view: true, del: true}},
		{"policygroup", policyGroupKind{}, caps{del: true, drill: true}},
		{"pg policy", pgPoliciesKind{group: "g"}, caps{view: true, del: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := caps{}
			_, got.view = tc.k.(Viewable)
			_, got.edit = tc.k.(Editable)
			_, got.create = tc.k.(Creatable)
			_, got.named = tc.k.(NamedCreatable)
			_, got.del = tc.k.(Deletable)
			_, got.download = tc.k.(Downloadable)
			_, got.drill = tc.k.(DrillDown)
			if got != tc.want {
				t.Errorf("%s capabilities = %+v, want %+v", tc.name, got, tc.want)
			}
		})
	}
}

// TestCookbookKindListReportsVersionCount counts every version. erchef lists
// only a cookbook's latest version unless asked for num_versions=all, which
// this server mimics.
func TestCookbookKindListReportsVersionCount(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/cookbooks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("num_versions") != "all" {
			_, _ = w.Write([]byte(`{"apache":{"url":"u","versions":[{"version":"2.0.0","url":"u"}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"apache":{"url":"u","versions":[{"version":"1.0.0","url":"u"},{"version":"2.0.0","url":"u"}]}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	rows, err := cookbookKind{}.List(context.Background(), testClient(t, srv))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "apache" {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Cells[1] != "2" {
		t.Errorf("version count = %q, want 2", rows[0].Cells[1])
	}
}

// TestCookbookVersionsSortNewestFirst drills into one cookbook. Versions
// order numerically, as Chef orders them: 1.10.0 is newer than 1.9.0.
func TestCookbookVersionsSortNewestFirst(t *testing.T) {
	mux := http.NewServeMux()
	jsonHandler(mux, "/organizations/acme/cookbooks/apache",
		`{"apache":{"url":"u","versions":[{"version":"1.9.0","url":"u"},{"version":"2.0.0","url":"u"},{"version":"1.10.0","url":"u"}]}}`)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	rows, err := cookbookVersionsKind{name: "apache"}.List(context.Background(), testClient(t, srv))
	if err != nil {
		t.Fatal(err)
	}
	if got := names(rows); !equal(got, []string{"2.0.0", "1.10.0", "1.9.0"}) {
		t.Errorf("versions = %v, want newest first", got)
	}
}

func TestDataBagItemsDescribeReturnsItemJSON(t *testing.T) {
	mux := http.NewServeMux()
	jsonHandler(mux, "/organizations/acme/data/creds/aws", `{"id":"aws","secret":"s3kr3t"}`)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	body, err := dataBagItemsKind{bag: "creds"}.Describe(context.Background(), testClient(t, srv), "aws")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(body, "s3kr3t") {
		t.Errorf("describe body = %q, want item contents", body)
	}
}
