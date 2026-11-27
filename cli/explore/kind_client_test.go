package explore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func TestClientSummaryFields(t *testing.T) {
	validator := &cinc.APIClient{Name: "bootstrap", Validator: true}
	got := map[string]string{}
	for _, f := range clientSummaryFields(validator) {
		got[f.Label] = f.Value
	}
	if got["Type"] != "Validator" {
		t.Errorf("validator Type = %q, want Validator", got["Type"])
	}

	regular := &cinc.APIClient{Name: "web01"}
	got = map[string]string{}
	for _, f := range clientSummaryFields(regular) {
		got[f.Label] = f.Value
	}
	if got["Type"] != "Regular" {
		t.Errorf("non-validator Type = %q, want Regular", got["Type"])
	}
	// GET /clients/<name> carries no key under server API v1, so a key row
	// could only ever claim the client has none.
	if _, ok := got["Public Key"]; ok {
		t.Errorf("client summary has a Public Key row, which the client object can't fill: %v", got)
	}
}

// The Clients kind renders a curated facts panel — the fix for the original
// report that hovering a client dumped raw JSON — while still carrying the
// object's JSON for the detail/edit views to reuse.
func TestClientKindSummaryShowsFields(t *testing.T) {
	mux := http.NewServeMux()
	// erchef's GET /clients/<name> body at server API v1.
	jsonHandler(mux, "/organizations/acme/clients/web01",
		`{"name":"web01","clientname":"web01","validator":false,"orgname":"acme","json_class":"Chef::ApiClient","chef_type":"client"}`)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	k, ok := newClientKind().(Summarizable)
	if !ok {
		t.Fatal("client kind is not Summarizable")
	}
	view, err := k.Summary(context.Background(), testClient(t, srv), "web01")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if len(view.Fields) == 0 {
		t.Error("client summary has no curated fields; would fall back to JSON")
	}
	if !strings.Contains(view.JSON, "web01") {
		t.Errorf("summary did not carry the client JSON: %q", view.JSON)
	}
}
