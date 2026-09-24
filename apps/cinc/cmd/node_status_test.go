package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

// nodeStatusSearchServer serves a node search result with the given raw rows.
// total must match the number of rows so the client's pagination terminates.
func nodeStatusSearchServer(t *testing.T, total int, rows string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/search/node", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{"total":%d,"start":0,"rows":[%s]}`, total, rows))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
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
	rows := fmt.Sprintf(
		`{"name":"db01","automatic":{"ohai_time":%d,"platform":"ubuntu","platform_version":"22.04","fqdn":"db01.example.test"}},`+
			`{"name":"web01","automatic":{"ohai_time":%d,"platform":"rhel","platform_version":"9","fqdn":"web01.example.test"}}`,
		old, recent)
	srv := nodeStatusSearchServer(t, 2, rows)

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
	if !strings.Contains(out, "ubuntu 22.04") || !strings.Contains(out, "web01.example.test") {
		t.Errorf("missing platform/fqdn columns:\n%s", out)
	}
}

func TestNodeStatusCommandJSONOutput(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	withFixedNodeStatusClock(t, now)
	ts := now.Add(-time.Minute).Unix()
	srv := nodeStatusSearchServer(t, 1, fmt.Sprintf(`{"name":"web01","automatic":{"ohai_time":%d,"platform":"ubuntu"}}`, ts))

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
	srv := nodeStatusSearchServer(t, 1, `{"name":"fresh"}`)

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
// not a node fails the command instead of showing up as a blank line.
func TestNodeStatusCommandReportsUndecodableRow(t *testing.T) {
	srv := nodeStatusSearchServer(t, 1, `{"name":5}`)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"node", "status", "--config", writeCommandConfig(t, srv.URL)})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "decode node") {
		t.Errorf("node status error = %v, want the decode failure", err)
	}
}

// TestNodeStatusCommandFollowsAttributePrecedence checks host facts are read
// the way a recipe reads node[...], so one set outside automatic still shows,
// and that a fractional ohai_time survives into the JSON.
func TestNodeStatusCommandFollowsAttributePrecedence(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	withFixedNodeStatusClock(t, now)
	ts := float64(now.Add(-time.Minute).Unix()) + 0.5
	srv := nodeStatusSearchServer(t, 1, fmt.Sprintf(`{"name":"web01","automatic":{"ohai_time":%v},"normal":{"fqdn":"web01.normal.test"}}`, ts))

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
