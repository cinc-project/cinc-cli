package policyfile

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func TestArtifactsToUpload(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/cookbook_artifacts", func(w http.ResponseWriter, _ *http.Request) {
		// The server holds base@aaa and an older app@old, but not app@new.
		_, _ = io.WriteString(w, `{
			"base": {"url": "u", "versions": [{"identifier": "aaa", "url": "u"}]},
			"app":  {"url": "u", "versions": [{"identifier": "old", "url": "u"}]}
		}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	c, err := cinc.NewClient(cinc.Config{ServerURL: srv.URL, Org: "acme", ClientName: "tim", Key: key})
	if err != nil {
		t.Fatal(err)
	}

	lock := &cinc.PolicyRevision{CookbookLocks: map[string]cinc.CookbookLock{
		"base": {Identifier: "aaa"},
		"app":  {Identifier: "new"},
		"db":   {Identifier: "ddd"},
	}}
	got, err := ArtifactsToUpload(context.Background(), c, lock)
	if err != nil {
		t.Fatalf("ArtifactsToUpload: %v", err)
	}
	if got != 2 {
		t.Errorf("ArtifactsToUpload = %d, want 2 (app@new and db@ddd)", got)
	}

	// A lock with no cookbooks needs no listing at all.
	if got, err := ArtifactsToUpload(context.Background(), c, &cinc.PolicyRevision{}); err != nil || got != 0 {
		t.Errorf("ArtifactsToUpload(no cookbooks) = %d, %v; want 0, nil", got, err)
	}
}
