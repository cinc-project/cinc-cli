package cmd

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func TestNodeRunListAddAppendsNewEntries(t *testing.T) {
	var gotPut cinc.Node
	current := cinc.Node{Name: "web01", RunList: []string{"recipe[base]"}}
	srv := nodeItemServer(t, "web01", current, &gotPut)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	// recipe[base] already present must not be duplicated; new entries appended.
	root.SetArgs([]string{"node", "run-list", "add", "web01", "recipe[base],recipe[apache]", "role[web]", "--config", writeNodeConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("node run-list add: %v", err)
	}
	want := []string{"recipe[base]", "recipe[apache]", "role[web]"}
	if !slices.Equal(gotPut.RunList, want) {
		t.Errorf("PUT run_list = %v, want %v", gotPut.RunList, want)
	}
	if out := buf.String(); !strings.Contains(out, "recipe[apache]") || !strings.Contains(out, "role[web]") {
		t.Errorf("output = %q, want the new run list", out)
	}
}

func TestNodeRunListRemoveDropsEntries(t *testing.T) {
	var gotPut cinc.Node
	current := cinc.Node{Name: "web01", RunList: []string{"recipe[base]", "recipe[apache]", "role[web]"}}
	srv := nodeItemServer(t, "web01", current, &gotPut)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"node", "run-list", "remove", "web01", "recipe[apache]", "--config", writeNodeConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("node run-list remove: %v", err)
	}
	want := []string{"recipe[base]", "role[web]"}
	if !slices.Equal(gotPut.RunList, want) {
		t.Errorf("PUT run_list = %v, want %v", gotPut.RunList, want)
	}
}

func TestNodeRunListSetReplacesEntireList(t *testing.T) {
	var gotPut cinc.Node
	current := cinc.Node{Name: "web01", RunList: []string{"recipe[old]"}}
	srv := nodeItemServer(t, "web01", current, &gotPut)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"node", "run-list", "set", "web01", "recipe[a],recipe[b]", "--config", writeNodeConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("node run-list set: %v", err)
	}
	want := []string{"recipe[a]", "recipe[b]"}
	if !slices.Equal(gotPut.RunList, want) {
		t.Errorf("PUT run_list = %v, want %v", gotPut.RunList, want)
	}
}

func TestNodeRunListListReadsWithoutPut(t *testing.T) {
	var gotPut cinc.Node
	current := cinc.Node{Name: "web01", RunList: []string{"recipe[base]", "role[web]"}}
	srv := nodeItemServer(t, "web01", current, &gotPut)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"node", "run-list", "list", "web01", "--config", writeNodeConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("node run-list list: %v", err)
	}
	// A read verb must not PUT.
	if gotPut.Name != "" {
		t.Errorf("run-list list issued a PUT (gotPut=%+v); it must be read-only", gotPut)
	}
	out := buf.String()
	if !strings.Contains(out, "recipe[base]") || !strings.Contains(out, "role[web]") {
		t.Errorf("run-list list output = %q, want both entries", out)
	}
}

func TestNodeRunListJSONFormatEmitsArray(t *testing.T) {
	var gotPut cinc.Node
	current := cinc.Node{Name: "web01", RunList: []string{}}
	srv := nodeItemServer(t, "web01", current, &gotPut)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"node", "run-list", "add", "web01", "recipe[a]", "--format", "json", "--config", writeNodeConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("node run-list add --format json: %v", err)
	}
	var got []string
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not a JSON array: %v\n%s", err, buf.String())
	}
	if !slices.Equal(got, []string{"recipe[a]"}) {
		t.Errorf("json output = %v, want [recipe[a]]", got)
	}
}
