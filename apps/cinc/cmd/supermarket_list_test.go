package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSupermarketListCommandRegistered(t *testing.T) {
	root := newRootCmd()
	sub, _, err := root.Find([]string{"supermarket", "list"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if sub.Use != "list" {
		t.Fatalf("Use = %q, want list", sub.Use)
	}
	for _, name := range []string{"supermarket-site", "order", "user", "limit", "verbose"} {
		if sub.Flags().Lookup(name) == nil {
			t.Fatalf("--%s flag missing", name)
		}
	}
}

func TestSupermarketListPrintsNamesOnePerLine(t *testing.T) {
	srv := newSupermarketIndexServer(t, []indexCookbook{
		{Name: "apache2", Maintainer: "sous-chefs"},
		{Name: "nginx", Maintainer: "sous-chefs"},
	}, nil)
	defer srv.Close()

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"supermarket", "list", "--supermarket-site", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	want := "apache2\nnginx\n"
	if got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestSupermarketListVerboseRendersTableWithLatestVersion(t *testing.T) {
	srv := newSupermarketIndexServer(t,
		[]indexCookbook{
			{Name: "apache2", Maintainer: "sous-chefs"},
			{Name: "nginx", Maintainer: "sous-chefs"},
		},
		map[string][]string{
			"apache2": {"9.0.3", "9.0.4"},
			"nginx":   {"12.0.4"},
		},
	)
	defer srv.Close()

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"supermarket", "list", "--verbose", "--supermarket-site", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"NAME", "MAINTAINER", "LATEST", "apache2", "9.0.4", "nginx", "12.0.4"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q\noutput:\n%s", want, got)
		}
	}
}

func TestSupermarketListJSONEmitsFullResult(t *testing.T) {
	srv := newSupermarketIndexServer(t, []indexCookbook{
		{Name: "apache2", Maintainer: "sous-chefs", Description: "Web server"},
	}, nil)
	defer srv.Close()

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"supermarket", "list", "--format", "json", "--supermarket-site", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var decoded struct {
		Entries []struct {
			Name        string `json:"name"`
			Maintainer  string `json:"maintainer"`
			Description string `json:"description"`
		} `json:"entries"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("json: %v\noutput: %s", err, buf.String())
	}
	if decoded.Total != 1 || len(decoded.Entries) != 1 {
		t.Fatalf("decoded = %+v", decoded)
	}
	if decoded.Entries[0].Name != "apache2" || decoded.Entries[0].Description != "Web server" {
		t.Fatalf("entry = %+v", decoded.Entries[0])
	}
}

// indexCookbook is the shape the test fixture server returns.
type indexCookbook struct {
	Name        string
	Maintainer  string
	Description string
}

// newSupermarketIndexServer stands up an httptest.Server that responds
// to GET /api/v1/cookbooks (the index) and GET /universe (versions),
// used by list and search command tests.
func newSupermarketIndexServer(t *testing.T, cookbooks []indexCookbook, universe map[string][]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/cookbooks", "/api/v1/search":
			items := make([]map[string]string, 0, len(cookbooks))
			for _, c := range cookbooks {
				items = append(items, map[string]string{
					"cookbook_name":        c.Name,
					"cookbook_maintainer":  c.Maintainer,
					"cookbook_description": c.Description,
					"cookbook":             "/api/v1/cookbooks/" + c.Name,
				})
			}
			body, _ := json.Marshal(map[string]any{
				"start": 0,
				"total": len(cookbooks),
				"items": items,
			})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
		case "/universe":
			out := map[string]map[string]map[string]any{}
			for name, versions := range universe {
				out[name] = map[string]map[string]any{}
				for _, v := range versions {
					out[name][v] = map[string]any{
						"location_type": "supermarket",
						"location_path": "https://example.test",
					}
				}
			}
			body, _ := json.Marshal(out)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

func TestSupermarketListRejectsUnknownOrderBeforeAnyRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s: --order should be rejected first", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"supermarket", "list", "--supermarket-site", srv.URL, "--order", "most_popular"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error for an unknown --order")
	}
	if !strings.Contains(err.Error(), "most_popular") || !strings.Contains(err.Error(), "by_name") {
		t.Fatalf("error = %q, want it to name the bad value and list the valid ones", err)
	}
}

func TestSupermarketListSendsByNameOrder(t *testing.T) {
	var gotOrder string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOrder = r.URL.Query().Get("order")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"start":0,"total":0,"items":[]}`))
	}))
	defer srv.Close()

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"supermarket", "list", "--supermarket-site", srv.URL, "--order", "by_name"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotOrder != "by_name" {
		t.Fatalf("order = %q, want by_name", gotOrder)
	}
}
