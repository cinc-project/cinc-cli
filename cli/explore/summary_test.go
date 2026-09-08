package explore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	cinc "github.com/cinc-project/cinc-api"
)

func TestRelativeTime(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	epoch := func(d time.Duration) float64 {
		return float64(now.Add(-d).Unix())
	}
	cases := []struct {
		name  string
		input float64
		want  string
	}{
		{"never zero", 0, "never"},
		{"never negative", -5, "never"},
		{"seconds", epoch(30 * time.Second), "30s ago"},
		{"minutes", epoch(5 * time.Minute), "5m ago"},
		{"hours", epoch(2 * time.Hour), "2h ago"},
		{"days", epoch(3 * 24 * time.Hour), "3d ago"},
		{"future", float64(now.Add(time.Hour).Unix()), "just now"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := relativeTime(tc.input, now); got != tc.want {
				t.Errorf("relativeTime(%v) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestCount(t *testing.T) {
	if got := count(0); got != "0" {
		t.Errorf("count(0) = %q, want 0", got)
	}
	if got := count(42); got != "42" {
		t.Errorf("count(42) = %q, want 42", got)
	}
}

func TestYesNo(t *testing.T) {
	if got := yesNo(true); got != "Yes" {
		t.Errorf("yesNo(true) = %q, want Yes", got)
	}
	if got := yesNo(false); got != "No" {
		t.Errorf("yesNo(false) = %q, want No", got)
	}
}

func TestPresence(t *testing.T) {
	if got := presence("anything"); got != "set" {
		t.Errorf("presence(non-empty) = %q, want set", got)
	}
	if got := presence("  "); got != "not set" {
		t.Errorf("presence(blank) = %q, want not set", got)
	}
}

func TestList(t *testing.T) {
	cases := []struct {
		name  string
		items []string
		max   int
		want  string
	}{
		{"empty", nil, 0, "—"},
		{"all when no cap", []string{"a", "b", "c"}, 0, "a, b, c"},
		{"under cap", []string{"a", "b"}, 5, "a, b"},
		{"over cap trails with more", []string{"a", "b", "c", "d"}, 2, "a, b, +2 more"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := list(tc.items, tc.max); got != tc.want {
				t.Errorf("list(%v, %d) = %q, want %q", tc.items, tc.max, got, tc.want)
			}
		})
	}
}

// summarize always carries the fetched object's JSON alongside the curated
// fields, so the detail and edit views can reuse the one fetch.
func TestSummarizeCarriesJSON(t *testing.T) {
	mux := http.NewServeMux()
	jsonHandler(mux, "/organizations/acme/roles/web", `{"name":"web","description":"web tier"}`)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	view, err := summarize(context.Background(), testClient(t, srv), "web",
		func(ctx context.Context, c *cinc.Client, n string) (*cinc.Role, error) {
			r, _, err := c.Roles.Get(ctx, n)
			return r, err
		},
		nil,
		func(_ context.Context, _ *cinc.Client, r *cinc.Role) []summaryField {
			return []summaryField{{"Description", orDash(r.Description)}}
		})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if len(view.Fields) != 1 || view.Fields[0].Value != "web tier" {
		t.Errorf("fields = %+v, want one Description=web tier", view.Fields)
	}
	if !strings.Contains(view.JSON, "web tier") {
		t.Errorf("summarize did not carry the object JSON: %q", view.JSON)
	}
}

// A nil fields builder is tolerated: the view still carries the JSON for
// reuse, it just has no curated panel.
func TestSummarizeNilFields(t *testing.T) {
	mux := http.NewServeMux()
	jsonHandler(mux, "/organizations/acme/roles/web", `{"name":"web"}`)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	view, err := summarize(context.Background(), testClient(t, srv), "web",
		func(ctx context.Context, c *cinc.Client, n string) (*cinc.Role, error) {
			r, _, err := c.Roles.Get(ctx, n)
			return r, err
		}, nil, nil)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if len(view.Fields) != 0 {
		t.Errorf("fields = %+v, want none", view.Fields)
	}
	if !strings.Contains(view.JSON, "web") {
		t.Errorf("summarize dropped the object JSON: %q", view.JSON)
	}
}
