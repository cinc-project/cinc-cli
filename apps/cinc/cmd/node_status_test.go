package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRelativeCheckin(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		last time.Time
		want string
	}{
		{"never", time.Time{}, "never"},
		{"seconds", now.Add(-30 * time.Second), "30s ago"},
		{"minutes", now.Add(-5 * time.Minute), "5m ago"},
		{"hours", now.Add(-3 * time.Hour), "3h ago"},
		{"days", now.Add(-48 * time.Hour), "2d ago"},
		{"future clamps to zero", now.Add(time.Hour), "0s ago"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := relativeCheckin(tc.last, now); got != tc.want {
				t.Errorf("relativeCheckin(%v) = %q, want %q", tc.last, got, tc.want)
			}
		})
	}
}

// partialNodeSearchServer is a fake node search endpoint that only answers
// partial searches. Each row is the projected data for one node, which the
// server wraps in the {url, data} envelope a real partial search returns,
// with the node's name (the row's "name" when it's a string, else n<i>) as
// the last URL segment, as the server does. A full (GET) search is rejected,
// so a command that fetches whole nodes fails. The returned func reports the
// projection of the last request.
func partialNodeSearchServer(t *testing.T, rows ...string) (*httptest.Server, func() map[string][]string) {
	t.Helper()
	wrapped := make([]string, len(rows))
	for i, row := range rows {
		name := fmt.Sprintf("n%d", i)
		var named struct {
			Name any `json:"name"`
		}
		if json.Unmarshal([]byte(row), &named) == nil {
			if s, ok := named.Name.(string); ok {
				name = s
			}
		}
		wrapped[i] = fmt.Sprintf(`{"url":"https://cinc.example.test/organizations/acme/nodes/%s","data":%s}`, name, row)
	}
	return nodeSearchServer(t, wrapped...)
}

// nodeSearchServer is partialNodeSearchServer without the wrapping: each row
// is sent exactly as given, so a test can send rows that aren't partial
// search results.
func nodeSearchServer(t *testing.T, rows ...string) (*httptest.Server, func() map[string][]string) {
	t.Helper()
	var mu sync.Mutex
	var projection map[string][]string
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/search/node", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":["want a partial search (POST)"]}`, http.StatusBadRequest)
			return
		}
		var body map[string][]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":["bad partial search body"]}`, http.StatusBadRequest)
			return
		}
		mu.Lock()
		projection = body
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"total":%d,"start":0,"rows":[%s]}`, len(rows), strings.Join(rows, ","))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, func() map[string][]string {
		mu.Lock()
		defer mu.Unlock()
		return projection
	}
}

func withFixedNodeStatusClock(t *testing.T, now time.Time) {
	t.Helper()
	orig := nodeStatusClock
	nodeStatusClock = func() time.Time { return now }
	t.Cleanup(func() { nodeStatusClock = orig })
}

func TestNodeStatusCommandHumanOutput(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	withFixedNodeStatusClock(t, now)
	recent := now.Add(-2 * time.Minute).Unix()
	old := now.Add(-3 * time.Hour).Unix()
	srv, projection := partialNodeSearchServer(t,
		fmt.Sprintf(`{"name":"db01","ohai_time":%d,"platform":"ubuntu","platform_version":"22.04","fqdn":"db01.example.test","ipaddress":"10.0.0.1"}`, old),
		fmt.Sprintf(`{"name":"web01","ohai_time":%d,"platform":"rhel","platform_version":"9","fqdn":"web01.example.test","ipaddress":null}`, recent),
	)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"node", "status", "--config", writeCommandConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc node status: %v", err)
	}
	out := buf.String()
	// Most recent check-in first: web01 (2m) before db01 (3h).
	if strings.Index(out, "web01") > strings.Index(out, "db01") {
		t.Errorf("expected web01 (more recent) before db01:\n%s", out)
	}
	if !strings.Contains(out, "2m ago") || !strings.Contains(out, "3h ago") {
		t.Errorf("missing relative check-in times:\n%s", out)
	}
	if !strings.Contains(out, "ubuntu 22.04") || !strings.Contains(out, "web01.example.test") || !strings.Contains(out, "10.0.0.1") {
		t.Errorf("missing platform/fqdn/ipaddress columns:\n%s", out)
	}
	want := map[string][]string{
		"name":             {"name"},
		"ohai_time":        {"ohai_time"},
		"platform":         {"platform"},
		"platform_version": {"platform_version"},
		"fqdn":             {"fqdn"},
		"ipaddress":        {"ipaddress"},
	}
	if got := projection(); !reflect.DeepEqual(got, want) {
		t.Errorf("partial search projection = %v, want %v", got, want)
	}
}

func TestNodeStatusCommandJSONOutput(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	withFixedNodeStatusClock(t, now)
	ts := now.Add(-time.Minute).Unix()
	srv, _ := partialNodeSearchServer(t, fmt.Sprintf(`{"name":"web01","ohai_time":%d,"platform":"ubuntu"}`, ts))

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"node", "status", "--config", writeCommandConfig(t, srv.URL), "--format", "json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc node status --format json: %v", err)
	}
	var got []nodeStatus
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if len(got) != 1 || got[0].Name != "web01" || got[0].Platform != "ubuntu" || got[0].CheckinAgo != "1m ago" {
		t.Errorf("status[0] = %+v", got)
	}
}

func TestNodeStatusCommandReportsNeverCheckedIn(t *testing.T) {
	withFixedNodeStatusClock(t, time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC))
	srv, _ := partialNodeSearchServer(t, `{"name":"fresh","ohai_time":null,"platform":null}`)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"node", "status", "--config", writeCommandConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc node status: %v", err)
	}
	if !strings.Contains(buf.String(), "never") {
		t.Errorf("a node with no ohai_time should report 'never':\n%s", buf.String())
	}
}

// TestNodeStatusCommandReportsUndecodableRow checks that a search row that is
// not a partial search result (here, a whole node) fails the command instead
// of showing up as a blank line.
func TestNodeStatusCommandReportsUndecodableRow(t *testing.T) {
	srv, _ := nodeSearchServer(t, `{"name":"web01","chef_type":"node","automatic":{}}`)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"node", "status", "--config", writeCommandConfig(t, srv.URL)})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "not a partial-search row") {
		t.Errorf("node status error = %v, want the decode failure", err)
	}
}

// TestNodeStatusCommandNamesNodeFromRowURL checks the node name comes from
// the row's URL, so a row whose projected name is missing still names its
// node.
func TestNodeStatusCommandNamesNodeFromRowURL(t *testing.T) {
	srv, _ := nodeSearchServer(t,
		`{"url":"https://cinc.example.test/organizations/acme/nodes/web01","data":{"name":null,"platform":"ubuntu"}}`)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"node", "status", "--config", writeCommandConfig(t, srv.URL), "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc node status: %v", err)
	}
	var got []nodeStatus
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if len(got) != 1 || got[0].Name != "web01" || got[0].Platform != "ubuntu" {
		t.Errorf("statuses = %+v, want web01 on ubuntu", got)
	}
}

// TestNodeStatusCommandKeepsFractionalOhaiTime checks that a fractional
// ohai_time from the partial search survives into the JSON.
func TestNodeStatusCommandKeepsFractionalOhaiTime(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	withFixedNodeStatusClock(t, now)
	ts := float64(now.Add(-time.Minute).Unix()) + 0.5
	srv, _ := partialNodeSearchServer(t, fmt.Sprintf(`{"name":"web01","ohai_time":%v,"fqdn":"web01.normal.test"}`, ts))

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"node", "status", "--config", writeCommandConfig(t, srv.URL), "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("cinc node status --format json: %v", err)
	}
	var got []nodeStatus
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if len(got) != 1 || got[0].FQDN != "web01.normal.test" || got[0].OhaiTime != ts {
		t.Errorf("status = %+v, want fqdn web01.normal.test and ohai_time %v", got, ts)
	}
}
