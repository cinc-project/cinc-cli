package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fileNameRecorder is an httptest server that answers every request with an
// empty JSON object and keeps the bodies it saw, so a test can check both
// that a refused --file sent nothing and what an accepted one sent.
type fileNameRecorder struct {
	mu     sync.Mutex
	bodies []map[string]any
	paths  []string
}

func newFileNameRecorder(t *testing.T) (*fileNameRecorder, *httptest.Server) {
	t.Helper()
	rec := &fileNameRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.mu.Lock()
		rec.bodies = append(rec.bodies, body)
		rec.paths = append(rec.paths, r.Method+" "+r.URL.Path)
		rec.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return rec, srv
}

// TestFileNameMustMatchPositionalName covers every create/edit --file path
// whose object is named on the command line. A file naming a different
// object is refused before any request is sent; a file with no name, or the
// same name, is accepted and sent under the positional name.
func TestFileNameMustMatchPositionalName(t *testing.T) {
	cases := []struct {
		label string
		args  []string // command and positional name; the name is the last element
		kind  string   // the noun as the error names it
		field string   // the file's identity key
		sent  string   // the request body's identity key
	}{
		{"node create", []string{"node", "create", "web"}, "node", "name", "name"},
		{"node edit", []string{"node", "edit", "web"}, "node", "name", "name"},
		{"role create", []string{"role", "create", "web"}, "role", "name", "name"},
		{"role edit", []string{"role", "edit", "web"}, "role", "name", "name"},
		{"environment create", []string{"environment", "create", "web"}, "environment", "name", "name"},
		{"environment edit", []string{"environment", "edit", "web"}, "environment", "name", "name"},
		{"client edit", []string{"client", "edit", "web"}, "client", "name", "name"},
		{"user edit", []string{"user", "edit", "web"}, "user", "username", "username"},
		{"group edit groupname", []string{"group", "edit", "web"}, "group", "groupname", "groupname"},
		{"group edit name", []string{"group", "edit", "web"}, "group", "name", "groupname"},
		{"org edit", []string{"org", "edit", "web"}, "organization", "name", "name"},
	}
	for _, tc := range cases {
		t.Run(tc.label+"/mismatch refused", func(t *testing.T) {
			rec, srv := newFileNameRecorder(t)
			path := writeFileNameJSON(t, fmt.Sprintf(`{%q: "db"}`, tc.field))
			_, _, err := runRoot(t, append(tc.args, "--file", path, "--config", writeOrgConfig(t, srv.URL))...)
			if err == nil {
				t.Fatal("expected a mismatched --file to be refused")
			}
			want := fmt.Sprintf(`%s is the %s "db", but you asked for "web". Change the file's %s to "web", or name "db" on the command line.`, path, tc.kind, tc.field)
			if err.Error() != want {
				t.Errorf("error =\n  %s\nwant\n  %s", err, want)
			}
			if len(rec.paths) != 0 {
				t.Errorf("server saw %v for a refused --file", rec.paths)
			}
		})
		for _, accepted := range []struct{ label, body string }{
			{"no name accepted", `{}`},
			{"empty name accepted", fmt.Sprintf(`{%q: ""}`, tc.field)},
			{"matching name accepted", fmt.Sprintf(`{%q: "web"}`, tc.field)},
		} {
			t.Run(tc.label+"/"+accepted.label, func(t *testing.T) {
				rec, srv := newFileNameRecorder(t)
				path := writeFileNameJSON(t, accepted.body)
				if _, _, err := runRoot(t, append(tc.args, "--file", path, "--config", writeOrgConfig(t, srv.URL))...); err != nil {
					t.Fatalf("%s --file: %v", strings.Join(tc.args, " "), err)
				}
				if len(rec.bodies) != 1 {
					t.Fatalf("server saw %v, want exactly one write", rec.paths)
				}
				if got := rec.bodies[0][tc.sent]; got != "web" {
					t.Errorf("request %s = %v, want the positional name %q", tc.sent, got, "web")
				}
			})
		}
	}
}

// A group file carrying both identity fields is refused when either names
// another group, even if the other one matches.
func TestGroupEditFileWithConflictingNamesRefused(t *testing.T) {
	for _, body := range []string{
		`{"groupname": "web", "name": "db"}`,
		`{"groupname": "db", "name": "web"}`,
	} {
		rec, srv := newFileNameRecorder(t)
		path := writeFileNameJSON(t, body)
		_, _, err := runRoot(t, "group", "edit", "web", "--file", path, "--config", writeOrgConfig(t, srv.URL))
		if err == nil || !strings.Contains(err.Error(), `group "db"`) {
			t.Errorf("file %s: error = %v, want a refusal naming group \"db\"", body, err)
		}
		if len(rec.paths) != 0 {
			t.Errorf("file %s: server saw %v for a refused --file", body, rec.paths)
		}
	}
}

func writeFileNameJSON(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "object.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
