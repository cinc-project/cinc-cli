package cmd

import (
	"bytes"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func TestNodeEnvironmentSetCommand(t *testing.T) {
	var gotPut cinc.Node
	current := cinc.Node{Name: "web01", Environment: "_default", RunList: []string{"recipe[base]"}}
	srv := nodeItemServer(t, "web01", current, &gotPut)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"node", "environment-set", "web01", "prod", "--config", writeNodeConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc node environment-set: %v", err)
	}
	if gotPut.Environment != "prod" || gotPut.Name != "web01" {
		t.Errorf("PUT body = %+v, want environment=prod name=web01", gotPut)
	}
	// The existing run list must be preserved through the update.
	if len(gotPut.RunList) != 1 || gotPut.RunList[0] != "recipe[base]" {
		t.Errorf("PUT body run_list = %v, want [recipe[base]] preserved", gotPut.RunList)
	}
	if out := buf.String(); !strings.Contains(out, `node "web01" environment to "prod"`) {
		t.Errorf("output = %q", out)
	}
}

func TestNodePolicySetCommand(t *testing.T) {
	var gotPut cinc.Node
	current := cinc.Node{Name: "web01", RunList: []string{}}
	srv := nodeItemServer(t, "web01", current, &gotPut)

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"node", "policy-set", "web01", "prod", "base", "--config", writeNodeConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("cinc node policy-set: %v", err)
	}
	if gotPut.PolicyGroup != "prod" || gotPut.PolicyName != "base" {
		t.Errorf("PUT body = %+v, want policy_group=prod policy_name=base", gotPut)
	}
	if out := buf.String(); !strings.Contains(out, `node "web01" to policy "base" in group "prod"`) {
		t.Errorf("output = %q", out)
	}
}

// TestNodeAssignUnchangedSkipsPut checks that setting what the node already
// has sends no PUT and says so.
func TestNodeAssignUnchangedSkipsPut(t *testing.T) {
	current := cinc.Node{Name: "web01", Environment: "prod", PolicyName: "base", PolicyGroup: "prod"}
	for args, want := range map[string]string{
		"environment-set web01 prod": "Node \"web01\" is already in environment \"prod\", so there's nothing to change\n",
		"policy-set web01 prod base": "Node \"web01\" already uses policy \"base\" in group \"prod\", so there's nothing to change\n",
	} {
		srv, puts := nodeModifyServer(t, current, nil)
		out, err := runNodeCmd(t, srv.URL, strings.Fields(args)...)
		if err != nil {
			t.Fatalf("node %s: %v\n%s", args, err, out)
		}
		if len(*puts) != 0 {
			t.Errorf("node %s sent %d PUT(s), want none", args, len(*puts))
		}
		if out != want {
			t.Errorf("node %s output = %q, want %q", args, out, want)
		}
	}
}

// TestNodeEnvironmentSetReportsServerState checks the reported environment
// is the one the server stored.
func TestNodeEnvironmentSetReportsServerState(t *testing.T) {
	srv, _ := nodeModifyServer(t, cinc.Node{Name: "web01", Environment: "prod"}, func(n cinc.Node) cinc.Node {
		n.Environment = ""
		return n
	})
	out, err := runNodeCmd(t, srv.URL, "environment-set", "web01", "")
	if err != nil {
		t.Fatalf("node environment-set: %v\n%s", err, out)
	}
	if want := "Set node \"web01\" environment to \"_default\"\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}
