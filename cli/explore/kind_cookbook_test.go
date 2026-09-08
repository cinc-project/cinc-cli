package explore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func TestLatestCookbookVersion(t *testing.T) {
	versions := []cinc.CookbookVersion{{Version: "1.0.0"}, {Version: "1.2.0"}, {Version: "1.1.5"}}
	if got := latestCookbookVersion(versions); got != "1.2.0" {
		t.Errorf("latestCookbookVersion = %q, want 1.2.0", got)
	}
	if got := latestCookbookVersion(nil); got != "" {
		t.Errorf("latestCookbookVersion(nil) = %q, want empty", got)
	}
}

// A cookbook shows its version count and newest version on hover, plus the
// latest version's identity metadata so you needn't drill in to see it.
func TestCookbookKindSummary(t *testing.T) {
	mux := http.NewServeMux()
	jsonHandler(mux, "/organizations/acme/cookbooks",
		`{"nginx":{"versions":[{"version":"1.0.0"},{"version":"1.2.0"}]}}`)
	jsonHandler(mux, "/organizations/acme/cookbooks/nginx/1.2.0",
		`{"cookbook_name":"nginx","version":"1.2.0",
		  "metadata":{"description":"Installs and configures nginx","maintainer":"Sous Chefs","license":"Apache-2.0",
		    "dependencies":{"apt":">= 7.0"}}}`)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	view, err := cookbookKind{}.Summary(context.Background(), testClient(t, srv), "nginx")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	got := map[string]string{}
	for _, f := range view.Fields {
		got[f.Label] = f.Value
	}
	want := map[string]string{
		"Versions":     "2",
		"Latest":       "1.2.0",
		"Description":  "Installs and configures nginx",
		"Maintainer":   "Sous Chefs",
		"License":      "Apache-2.0",
		"Dependencies": "apt >= 7.0",
	}
	for label, val := range want {
		if got[label] != val {
			t.Errorf("field %q = %q, want %q", label, got[label], val)
		}
	}
}

func TestCookbookVersionSummaryFields(t *testing.T) {
	cb := &cinc.Cookbook{
		Version:          "1.2.0",
		AllFilesManifest: []cinc.CookbookFileRef{{Name: "recipes/default.rb"}, {Name: "metadata.rb"}},
		Metadata: cinc.CookbookMetadata{
			Description:     "Installs and configures nginx",
			Maintainer:      "Sous Chefs",
			MaintainerEmail: "help@sous-chefs.org",
			License:         "Apache-2.0",
			SourceURL:       "https://github.com/sous-chefs/nginx",
			IssuesURL:       "https://github.com/sous-chefs/nginx/issues",
			Dependencies:    map[string]string{"ohai": ">= 0.0.0", "build-essential": ">= 8.0", "apt": ">= 7.0"},
		},
	}
	var order []string
	got := map[string]string{}
	for _, f := range cookbookVersionSummaryFields(cb) {
		got[f.Label] = f.Value
		order = append(order, f.Label)
	}
	want := map[string]string{
		"Version":          "1.2.0",
		"Description":      "Installs and configures nginx",
		"Maintainer":       "Sous Chefs",
		"Maintainer email": "help@sous-chefs.org",
		"License":          "Apache-2.0",
		"Source URL":       "https://github.com/sous-chefs/nginx",
		"Issues URL":       "https://github.com/sous-chefs/nginx/issues",
		// Sorted by name; the no-op ">= 0.0.0" constraint on ohai is dropped.
		"Dependencies": "apt >= 7.0, build-essential >= 8.0, ohai",
		"Files":        "2",
	}
	for label, val := range want {
		if got[label] != val {
			t.Errorf("field %q = %q, want %q", label, got[label], val)
		}
	}
	wantOrder := []string{
		"Version", "Description", "Maintainer", "Maintainer email",
		"License", "Source URL", "Issues URL", "Dependencies", "Files",
	}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Errorf("field order = %v, want %v", order, wantOrder)
	}
}

// A version with no metadata still renders, falling back to em dashes rather
// than blank or missing rows.
func TestCookbookVersionSummaryFieldsNoMetadata(t *testing.T) {
	got := map[string]string{}
	for _, f := range cookbookVersionSummaryFields(&cinc.Cookbook{Version: "1.0.0"}) {
		got[f.Label] = f.Value
	}
	for _, label := range []string{"Description", "Maintainer", "Maintainer email", "License", "Source URL", "Issues URL", "Dependencies"} {
		if got[label] != "—" {
			t.Errorf("field %q = %q, want em dash for absent metadata", label, got[label])
		}
	}
}

// Dependencies render sorted by name and capped so a sprawling dep set can't
// blow out the pane, trailing the overflow as a "+N more" count.
func TestCookbookDependenciesCapped(t *testing.T) {
	deps := map[string]string{
		"apt": ">= 7.0", "build-essential": ">= 8.0", "ohai": ">= 1.0",
		"seven_zip": ">= 0.0.0", "windows": ">= 5.0", "yum": ">= 6.0", "zypper": ">= 0.5",
	}
	got := cookbookDependencies(&cinc.Cookbook{Metadata: cinc.CookbookMetadata{Dependencies: deps}})
	want := "apt >= 7.0, build-essential >= 8.0, ohai >= 1.0, seven_zip, windows >= 5.0, yum >= 6.0, +1 more"
	if got != want {
		t.Errorf("cookbookDependencies = %q, want %q", got, want)
	}
}

// A cookbook version's summary draws its description, maintainer, and license
// from the metadata the server returns alongside the file manifest.
func TestCookbookVersionsKindSummary(t *testing.T) {
	mux := http.NewServeMux()
	jsonHandler(mux, "/organizations/acme/cookbooks/nginx/1.2.0",
		`{"cookbook_name":"nginx","name":"nginx-1.2.0","version":"1.2.0",
		  "all_files":[{"name":"recipes/default.rb"},{"name":"metadata.rb"}],
		  "metadata":{"description":"Installs and configures nginx","maintainer":"Sous Chefs","license":"Apache-2.0"}}`)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	k := cookbookVersionsKind{name: "nginx"}
	view, err := k.Summary(context.Background(), testClient(t, srv), "1.2.0")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	got := map[string]string{}
	for _, f := range view.Fields {
		got[f.Label] = f.Value
	}
	if got["Version"] != "1.2.0" {
		t.Errorf("Version = %q, want 1.2.0", got["Version"])
	}
	if got["Description"] != "Installs and configures nginx" {
		t.Errorf("Description = %q", got["Description"])
	}
	if got["Files"] != "2" {
		t.Errorf("Files = %q, want 2", got["Files"])
	}
}
