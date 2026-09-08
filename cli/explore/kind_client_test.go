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
	if got["Public Key"] != "not set" {
		t.Errorf("Public Key = %q, want not set", got["Public Key"])
	}

	regular := &cinc.APIClient{Name: "web01", ChefKey: cinc.ChefKey{PublicKey: "-----BEGIN PUBLIC KEY-----"}}
	got = map[string]string{}
	for _, f := range clientSummaryFields(regular) {
		got[f.Label] = f.Value
	}
	if got["Type"] != "Regular" {
		t.Errorf("non-validator Type = %q, want Regular", got["Type"])
	}
	if got["Public Key"] != "set" {
		t.Errorf("Public Key = %q, want set", got["Public Key"])
	}
}

// The Clients kind renders a curated facts panel — the fix for the original
// report that hovering a client dumped raw JSON — while still carrying the
// object's JSON for the detail/edit views to reuse.
func TestClientKindSummaryShowsFields(t *testing.T) {
	mux := http.NewServeMux()
	jsonHandler(mux, "/organizations/acme/clients/web01",
		`{"name":"web01","validator":false,"chef_key":{"public_key":"-----BEGIN PUBLIC KEY-----"}}`)
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
