package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// searchServer serves /search/<index> for org "acme". It answers GET (and the
// POST used by partial search) with the supplied rows, honoring the rows/start
// query params so pagination tests can assert on a single page. recordPartial,
// if non-nil, captures the partial-projection POST body.
func searchServer(t *testing.T, index string, total int, rows []any, recordPartial *map[string][]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/search/"+index, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && recordPartial != nil {
			_ = json.NewDecoder(r.Body).Decode(recordPartial)
		}
		start, _ := strconv.Atoi(r.URL.Query().Get("start"))
		page := rows[min(start, len(rows)):]
		if rstr := r.URL.Query().Get("rows"); rstr != "" {
			if n, _ := strconv.Atoi(rstr); n >= 0 && n < len(page) {
				page = page[:n]
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"total": total, "start": start, "rows": page})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func runSearchCmd(t *testing.T, serverURL string, args ...string) (string, error) {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "credentials")
	cfg := fmt.Sprintf(`[default]
cinc_server_url = "%s/organizations/acme"
client_name     = "tim"
client_key      = %q
`, serverURL, writeTestKey(t))
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{"search"}, append(args, "--config", cfgPath)...))
	err := root.Execute()
	return buf.String(), err
}

func TestSearchNodeDefaultTable(t *testing.T) {
	rows := []any{
		map[string]any{
			"name": "web01", "chef_environment": "prod",
			"run_list":  []string{"recipe[web]", "recipe[base]"},
			"automatic": map[string]any{"platform": "ubuntu", "platform_version": "22.04"},
		},
		map[string]any{
			"name": "db01", "chef_environment": "staging",
			"run_list": []string{"role[database]"},
		},
	}
	srv := searchServer(t, "node", 2, rows, nil)

	out, err := runSearchCmd(t, srv.URL, "node", "*:*")
	if err != nil {
		t.Fatalf("cinc search node: %v\n%s", err, out)
	}
	for _, want := range []string{"NAME", "ENVIRONMENT", "PLATFORM", "RUN LIST", "web01", "db01", "ubuntu 22.04", "recipe[web], recipe[base]", "role[database]"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
	// db01 has no automatic platform -> "-"; sorted so db01 precedes web01.
	if strings.Index(out, "db01") > strings.Index(out, "web01") {
		t.Errorf("rows not sorted by name:\n%s", out)
	}
	if !strings.Contains(out, "2 nodes matched") {
		t.Errorf("missing count footer:\n%s", out)
	}
}

func TestSearchIDOnly(t *testing.T) {
	rows := []any{
		map[string]any{"name": "web01"},
		map[string]any{"name": "db01"},
	}
	srv := searchServer(t, "node", 2, rows, nil)

	out, err := runSearchCmd(t, srv.URL, "node", "*:*", "-i")
	if err != nil {
		t.Fatalf("cinc search -i: %v\n%s", err, out)
	}
	if out != "db01\nweb01\n" {
		t.Errorf("id-only output = %q, want sorted bare names", out)
	}
}

func TestSearchPartialAttributesBecomeColumns(t *testing.T) {
	var gotPartial map[string][]string
	rows := []any{
		map[string]any{"url": "https://x/nodes/web01", "data": map[string]any{"name": "web01", "ipaddress": "10.0.0.5", "fqdn": "web01.example.com"}},
	}
	srv := searchServer(t, "node", 1, rows, &gotPartial)

	out, err := runSearchCmd(t, srv.URL, "node", "*:*", "-a", "ipaddress", "-a", "fqdn")
	if err != nil {
		t.Fatalf("cinc search -a: %v\n%s", err, out)
	}
	if _, ok := gotPartial["ipaddress"]; !ok {
		t.Errorf("partial projection missing ipaddress: %v", gotPartial)
	}
	if _, ok := gotPartial["name"]; !ok {
		t.Errorf("partial projection should always include name: %v", gotPartial)
	}
	for _, want := range []string{"NAME", "IPADDRESS", "FQDN", "web01", "10.0.0.5", "web01.example.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("partial table missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "ENVIRONMENT") {
		t.Errorf("partial search should not show default columns:\n%s", out)
	}
}

func TestSearchJSONFormat(t *testing.T) {
	rows := []any{
		map[string]any{"name": "web01", "chef_environment": "prod"},
	}
	srv := searchServer(t, "node", 1, rows, nil)

	out, err := runSearchCmd(t, srv.URL, "node", "name:web01", "--format", "json")
	if err != nil {
		t.Fatalf("cinc search --format json: %v\n%s", err, out)
	}
	var got struct {
		Total int               `json:"total"`
		Rows  []json.RawMessage `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json output not valid: %v\n%s", err, out)
	}
	if got.Total != 1 || len(got.Rows) != 1 {
		t.Errorf("json result = %+v, want total=1 with one row", got)
	}
}

func TestSearchPaginationSinglePage(t *testing.T) {
	rows := []any{
		map[string]any{"name": "a"}, map[string]any{"name": "b"}, map[string]any{"name": "c"},
	}
	srv := searchServer(t, "node", 3, rows, nil)

	out, err := runSearchCmd(t, srv.URL, "node", "*:*", "--rows", "1", "-i")
	if err != nil {
		t.Fatalf("cinc search --rows: %v\n%s", err, out)
	}
	if out != "a\n" {
		t.Errorf("--rows 1 -i output = %q, want only the first row", out)
	}
}

// TestSearchStartWithoutRowsReturnsEveryRemainingMatch covers --start on its
// own: --rows defaults to 0 ("return all matches"), so an offset must still
// page through the rest of the result set rather than stop after the first
// page the server hands back.
func TestSearchStartWithoutRowsReturnsEveryRemainingMatch(t *testing.T) {
	rows := []any{
		map[string]any{"name": "a"}, map[string]any{"name": "b"}, map[string]any{"name": "c"},
		map[string]any{"name": "d"}, map[string]any{"name": "e"},
	}
	// The server answers at most two rows per page, whatever was asked for.
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/search/node", func(w http.ResponseWriter, r *http.Request) {
		start, _ := strconv.Atoi(r.URL.Query().Get("start"))
		page := rows[min(start, len(rows)):]
		page = page[:min(2, len(page))]
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"total": len(rows), "start": start, "rows": page})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	out, err := runSearchCmd(t, srv.URL, "node", "*:*", "--start", "1", "--format", "json")
	if err != nil {
		t.Fatalf("cinc search --start: %v\n%s", err, out)
	}
	var got struct {
		Total int `json:"total"`
		Start int `json:"start"`
		Rows  []struct {
			Name string `json:"name"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json output not valid: %v\n%s", err, out)
	}
	var names []string
	for _, r := range got.Rows {
		names = append(names, r.Name)
	}
	if strings.Join(names, ",") != "b,c,d,e" {
		t.Errorf("--start 1 rows = %v, want b,c,d,e", names)
	}
	if got.Total != 5 || got.Start != 1 {
		t.Errorf("total/start = %d/%d, want 5/1", got.Total, got.Start)
	}
}

func TestSearchEmptyResults(t *testing.T) {
	srv := searchServer(t, "node", 0, []any{}, nil)

	out, err := runSearchCmd(t, srv.URL, "node", "name:nope")
	if err != nil {
		t.Fatalf("cinc search (empty): %v\n%s", err, out)
	}
	if !strings.Contains(out, "No nodes matched") {
		t.Errorf("empty search output = %q, want a no-match message", out)
	}
}

func TestSearchRoleColumns(t *testing.T) {
	rows := []any{
		map[string]any{"name": "web", "description": "Web tier", "run_list": []string{"recipe[apache]"}},
	}
	srv := searchServer(t, "role", 1, rows, nil)

	out, err := runSearchCmd(t, srv.URL, "role", "*:*")
	if err != nil {
		t.Fatalf("cinc search role: %v\n%s", err, out)
	}
	for _, want := range []string{"NAME", "DESCRIPTION", "RUN LIST", "web", "Web tier", "recipe[apache]"} {
		if !strings.Contains(out, want) {
			t.Errorf("role table missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "1 role matched") {
		t.Errorf("role footer should be singular:\n%s", out)
	}
}
