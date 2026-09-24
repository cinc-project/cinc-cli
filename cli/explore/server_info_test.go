package explore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The title bar's API version comes from the cheap /server_api_version
// probe, not from listing every node in the org.
func TestServerInfoProbesServerAPIVersion(t *testing.T) {
	mux := http.NewServeMux()
	jsonHandler(mux, "/server_api_version", `{"min_api_version":0,"max_api_version":2}`)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	msg, ok := serverInfoCmd(context.Background(), testClient(t, srv))().(serverInfoMsg)
	if !ok {
		t.Fatalf("serverInfoCmd returned %T, want serverInfoMsg", msg)
	}
	if msg.version != "API v2" {
		t.Errorf("version = %q, want %q", msg.version, "API v2")
	}
}

// A server that won't answer leaves the version blank rather than failing.
func TestServerInfoBlankOnError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)

	msg := serverInfoCmd(context.Background(), testClient(t, srv))().(serverInfoMsg)
	if msg.version != "" {
		t.Errorf("version = %q, want blank", msg.version)
	}
}
