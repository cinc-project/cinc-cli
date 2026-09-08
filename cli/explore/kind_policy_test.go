package explore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func TestPolicyRevisionSummaryFields(t *testing.T) {
	rev := &cinc.PolicyRevision{
		RevisionID: "abc123",
		RunList:    []string{"recipe[nginx]", "recipe[app]"},
		CookbookLocks: map[string]cinc.CookbookLock{
			"nginx": {},
			"app":   {},
		},
	}
	got := map[string]string{}
	for _, f := range policyRevisionSummaryFields(context.Background(), nil, rev) {
		got[f.Label] = f.Value
	}
	if got["Run List"] != "recipe[nginx], recipe[app]" {
		t.Errorf("Run List = %q", got["Run List"])
	}
	if got["Cookbook Locks"] != "2" {
		t.Errorf("Cookbook Locks = %q, want 2", got["Cookbook Locks"])
	}
}

// A policy shows how many revisions it has on hover.
func TestPolicyKindSummary(t *testing.T) {
	mux := http.NewServeMux()
	jsonHandler(mux, "/organizations/acme/policies/base",
		`{"revisions":{"abc":{},"def":{}}}`)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	view, err := policyKind{}.Summary(context.Background(), testClient(t, srv), "base")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	got := map[string]string{}
	for _, f := range view.Fields {
		got[f.Label] = f.Value
	}
	if got["Revisions"] != "2" {
		t.Errorf("Revisions = %q, want 2", got["Revisions"])
	}
}

// A policy revision shows what it runs and how many cookbooks it pins.
func TestPolicyRevisionsKindSummary(t *testing.T) {
	mux := http.NewServeMux()
	jsonHandler(mux, "/organizations/acme/policies/base/revisions/abc123",
		`{"revision_id":"abc123","run_list":["recipe[nginx]"],"cookbook_locks":{"nginx":{}}}`)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	k := policyRevisionsKind{policy: "base"}
	view, err := k.Summary(context.Background(), testClient(t, srv), "abc123")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	got := map[string]string{}
	for _, f := range view.Fields {
		got[f.Label] = f.Value
	}
	if got["Run List"] != "recipe[nginx]" || got["Cookbook Locks"] != "1" {
		t.Errorf("fields = %+v", view.Fields)
	}
}
