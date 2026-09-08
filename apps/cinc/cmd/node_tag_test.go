package cmd

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func TestNodeTagAddStoresTagsUnderNormal(t *testing.T) {
	var gotPut cinc.Node
	current := cinc.Node{Name: "web01", RunList: []string{}, Normal: cinc.Attributes{"tags": []any{"prod"}}}
	srv := nodeItemServer(t, "web01", current, &gotPut)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	// "prod" already present must not duplicate; new tags appended.
	root.SetArgs([]string{"node", "tag", "add", "web01", "prod,web", "canary", "--config", writeNodeConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("node tag add: %v", err)
	}
	got := gotPut.Tags()
	want := []string{"prod", "web", "canary"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("PUT normal.tags = %v, want %v", got, want)
	}
}

func TestNodeTagAddInitializesNormalWhenAbsent(t *testing.T) {
	var gotPut cinc.Node
	current := cinc.Node{Name: "web01", RunList: []string{}}
	srv := nodeItemServer(t, "web01", current, &gotPut)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"node", "tag", "add", "web01", "first", "--config", writeNodeConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("node tag add (no normal): %v", err)
	}
	if got := gotPut.Tags(); len(got) != 1 || got[0] != "first" {
		t.Errorf("PUT normal.tags = %v, want [first]", got)
	}
}

func TestNodeTagRemoveDropsTags(t *testing.T) {
	var gotPut cinc.Node
	current := cinc.Node{Name: "web01", RunList: []string{}, Normal: cinc.Attributes{"tags": []any{"prod", "web", "canary"}}}
	srv := nodeItemServer(t, "web01", current, &gotPut)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"node", "tag", "remove", "web01", "web", "--config", writeNodeConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("node tag remove: %v", err)
	}
	got := gotPut.Tags()
	want := []string{"prod", "canary"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("PUT normal.tags = %v, want %v", got, want)
	}
}

func TestNodeTagSetReplacesAllTags(t *testing.T) {
	var gotPut cinc.Node
	current := cinc.Node{Name: "web01", RunList: []string{}, Normal: cinc.Attributes{"tags": []any{"old1", "old2"}}}
	srv := nodeItemServer(t, "web01", current, &gotPut)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"node", "tag", "set", "web01", "fresh,only", "--config", writeNodeConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("node tag set: %v", err)
	}
	got := gotPut.Tags()
	want := []string{"fresh", "only"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("PUT normal.tags = %v, want %v (set must replace, not merge)", got, want)
	}
}

func TestNodeTagListReadsTagsWithoutPut(t *testing.T) {
	var gotPut cinc.Node
	current := cinc.Node{Name: "web01", RunList: []string{}, Normal: cinc.Attributes{"tags": []any{"prod", "web"}}}
	srv := nodeItemServer(t, "web01", current, &gotPut)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"node", "tag", "list", "web01", "--config", writeNodeConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("node tag list: %v", err)
	}
	// list must not PUT — gotPut stays zero-valued.
	if gotPut.Name != "" {
		t.Errorf("tag list issued a PUT (gotPut=%+v); it must be read-only", gotPut)
	}
	out := buf.String()
	if !strings.Contains(out, "prod") || !strings.Contains(out, "web") {
		t.Errorf("tag list output = %q, want both tags", out)
	}
}

func TestNodeTagListJSONFormatEmitsArray(t *testing.T) {
	var gotPut cinc.Node
	current := cinc.Node{Name: "web01", RunList: []string{}, Normal: cinc.Attributes{"tags": []any{"prod"}}}
	srv := nodeItemServer(t, "web01", current, &gotPut)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"node", "tag", "list", "web01", "--format", "json", "--config", writeNodeConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("node tag list --format json: %v", err)
	}
	var got []string
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not a JSON array: %v\n%s", err, buf.String())
	}
	if !slices.Equal(got, []string{"prod"}) {
		t.Errorf("json output = %v, want [prod]", got)
	}
}
