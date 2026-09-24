package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// nodeModifyServer serves GET and PUT for one node and records every PUT
// body. reply, when non-nil, turns a PUT body into the node the server
// answers with, standing in for a server that stores something other than
// what was sent.
func nodeModifyServer(t *testing.T, current cinc.Node, reply func(cinc.Node) cinc.Node) (*httptest.Server, *[]cinc.Node) {
	t.Helper()
	var puts []cinc.Node
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/nodes/"+current.Name, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(current)
		case http.MethodPut:
			var put cinc.Node
			if err := json.NewDecoder(r.Body).Decode(&put); err != nil {
				t.Errorf("decode PUT body: %v", err)
			}
			puts = append(puts, put)
			if reply != nil {
				put = reply(put)
			}
			_ = json.NewEncoder(w).Encode(put)
		default:
			t.Errorf("unexpected method %q on %s", r.Method, r.URL.Path)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &puts
}

func runNodeCmd(t *testing.T, serverURL string, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append(append([]string{"node"}, args...), "--config", writeNodeConfig(t, serverURL)))
	err := root.Execute()
	return buf.String(), err
}

// TestNodeRunListChangesNormalizeEntries checks that bare recipe names are
// stored and matched as recipe[...], the way the Chef Server stores them.
func TestNodeRunListChangesNormalizeEntries(t *testing.T) {
	for _, tc := range []struct {
		name    string
		current []string
		args    []string
		want    []string
	}{
		{"add skips a bare name already present", []string{"recipe[base]"}, []string{"add", "web01", "base,apache"}, []string{"recipe[base]", "recipe[apache]"}},
		{"remove matches a bare name", []string{"recipe[base]", "role[web]"}, []string{"remove", "web01", "base"}, []string{"role[web]"}},
		{"set qualifies bare names", []string{"recipe[old]"}, []string{"set", "web01", "a,role[b]"}, []string{"recipe[a]", "role[b]"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, puts := nodeModifyServer(t, cinc.Node{Name: "web01", RunList: tc.current}, nil)
			if out, err := runNodeCmd(t, srv.URL, append([]string{"run-list"}, tc.args...)...); err != nil {
				t.Fatalf("node run-list %v: %v\n%s", tc.args, err, out)
			}
			if len(*puts) != 1 || !slices.Equal((*puts)[0].RunList, tc.want) {
				t.Errorf("PUTs = %+v, want one with run_list %v", *puts, tc.want)
			}
		})
	}
}

// TestNodeRunListUnchangedSkipsPut checks that a change that leaves the run
// list as it was sends no PUT and says so.
func TestNodeRunListUnchangedSkipsPut(t *testing.T) {
	for _, args := range [][]string{
		{"add", "web01", "base"},
		{"remove", "web01", "recipe[ntp]"},
		{"set", "web01", "recipe[base],role[web]"},
	} {
		srv, puts := nodeModifyServer(t, cinc.Node{Name: "web01", RunList: []string{"recipe[base]", "role[web]"}}, nil)
		out, err := runNodeCmd(t, srv.URL, append([]string{"run-list"}, args...)...)
		if err != nil {
			t.Fatalf("node run-list %v: %v\n%s", args, err, out)
		}
		if len(*puts) != 0 {
			t.Errorf("node run-list %v sent %d PUT(s), want none", args, len(*puts))
		}
		if want := "Node \"web01\" already has that run list, so there's nothing to change: recipe[base], role[web]\n"; out != want {
			t.Errorf("node run-list %v output = %q, want %q", args, out, want)
		}
	}
}

// TestNodeRunListReportsServerState checks the reported run list is the one
// the server answered with, not the one the CLI sent.
func TestNodeRunListReportsServerState(t *testing.T) {
	srv, _ := nodeModifyServer(t, cinc.Node{Name: "web01", RunList: []string{}}, func(n cinc.Node) cinc.Node {
		n.RunList = append(n.RunList, "recipe[server-side]")
		return n
	})
	out, err := runNodeCmd(t, srv.URL, "run-list", "add", "web01", "recipe[a]")
	if err != nil {
		t.Fatalf("node run-list add: %v\n%s", err, out)
	}
	if want := "Run list for node \"web01\" is now: recipe[a], recipe[server-side]\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}
