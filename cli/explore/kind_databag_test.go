package explore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func TestDataBagItemSummaryFields(t *testing.T) {
	item := cinc.DataBagItem{
		"id":       "redis",
		"port":     6379.0,
		"password": "secret",
	}
	got := map[string]string{}
	for _, f := range dataBagItemSummaryFields(item) {
		got[f.Label] = f.Value
	}
	if got["ID"] != "redis" {
		t.Errorf("ID = %q, want redis", got["ID"])
	}
	if got["Keys"] != "3" {
		t.Errorf("Keys = %q, want 3", got["Keys"])
	}
	// Top Keys are sorted for a stable, predictable listing.
	if got["Top Keys"] != "id, password, port" {
		t.Errorf("Top Keys = %q, want sorted id, password, port", got["Top Keys"])
	}
}

// A data bag shows how many items it holds before you drill in.
func TestDataBagKindSummary(t *testing.T) {
	mux := http.NewServeMux()
	jsonHandler(mux, "/organizations/acme/data/apps", `{"redis":"u1","web":"u2"}`)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	view, err := dataBagKind{}.Summary(context.Background(), testClient(t, srv), "apps")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	got := map[string]string{}
	for _, f := range view.Fields {
		got[f.Label] = f.Value
	}
	if got["Items"] != "2" {
		t.Errorf("Items = %q, want 2", got["Items"])
	}
}

// A data bag item shows its shape (id, key count, key names) instead of raw
// JSON on hover.
func TestDataBagItemsKindSummary(t *testing.T) {
	mux := http.NewServeMux()
	jsonHandler(mux, "/organizations/acme/data/apps/redis",
		`{"id":"redis","port":6379,"password":"secret"}`)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	k := dataBagItemsKind{bag: "apps"}
	view, err := k.Summary(context.Background(), testClient(t, srv), "redis")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	got := map[string]string{}
	for _, f := range view.Fields {
		got[f.Label] = f.Value
	}
	if got["ID"] != "redis" || got["Keys"] != "3" {
		t.Errorf("fields = %+v, want ID=redis Keys=3", view.Fields)
	}
}
