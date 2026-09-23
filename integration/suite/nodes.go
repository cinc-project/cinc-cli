package suite

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	cinc "github.com/cinc-project/cinc-api"
)

var nodeFamily = family{cases: []testCase{
	{"nodes/lifecycle", []string{"node create", "node show", "node list", "node edit", "node delete"}, testNodeLifecycle},
	{"nodes/create-with-policy", []string{"node create"}, testNodeCreateWithPolicy},
	{"nodes/create-from-file", []string{"node create"}, testNodeCreateFromFile},
	{"nodes/not-found", []string{"node show", "node delete", "node run-list list", "node tag list"}, testNodeNotFound},
	{"nodes/edit-missing", []string{"node edit"}, testNodeEditMissing},
	{"nodes/already-exists", []string{"node create"}, testNodeAlreadyExists},
	{"nodes/run-list", []string{"node run-list set", "node run-list add", "node run-list remove", "node run-list list"}, testNodeRunList},
	{"nodes/tags", []string{"node tag add", "node tag list", "node tag remove", "node tag set"}, testNodeTags},
	{"nodes/status", []string{"node status"}, testNodeStatus},
	{"nodes/environment-set", []string{"node environment-set"}, testNodeEnvironmentSet},
	{"nodes/policy-set", []string{"node policy-set"}, testNodePolicySet},
	{"nodes/ssh-skip-search", []string{"node ssh"}, testNodeSSHSkipSearch},
	{"nodes/ssh-search", []string{"node ssh"}, testNodeSSHSearch},
	{"nodes/bootstrap", []string{"node bootstrap"}, testNodeBootstrap},
	{"nodes/bootstrap-policy", []string{"node bootstrap"}, testNodeBootstrapPolicy},
}}

// createNode creates a node with the given extra flags and registers its
// cleanup.
func createNode(c *cli, name string, flags ...string) {
	c.t.Helper()
	c.run(append([]string{"node", "create", name}, flags...)...)
	c.cleanup("node", "delete", name)
}

// createEnvironment creates an environment and registers its cleanup.
func createEnvironment(c *cli, name string) {
	c.t.Helper()
	c.run("environment", "create", name)
	c.cleanup("environment", "delete", name)
}

func showNode(c *cli, name string) cinc.Node {
	c.t.Helper()
	var n cinc.Node
	c.json(&n, "node", "show", name)
	return n
}

func testNodeLifecycle(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "node")
	out := c.run("node", "create", name, "--environment", "_default", "--run-list", "recipe[base],recipe[app]")
	c.cleanup("node", "delete", name)
	wantEqual(t, "create output", out, fmt.Sprintf("Created node %q\n", name))

	n := showNode(c, name)
	wantEqual(t, "name", n.Name, name)
	wantEqual(t, "environment", n.Environment, "_default")
	wantSlice(t, "run list", n.RunList, []string{"recipe[base]", "recipe[app]"})

	human := c.run("node", "show", name)
	if first, _, _ := strings.Cut(human, "\n"); first != name {
		t.Errorf("node show first line = %q, want the node name:\n%s", first, human)
	}
	for _, want := range []string{"Run List", "Environment", "_default", "recipe[base]"} {
		if !strings.Contains(human, want) {
			t.Errorf("node show missing %q:\n%s", want, human)
		}
	}

	if list := strings.Fields(c.run("node", "list")); !slices.Contains(list, name) {
		t.Errorf("node list does not include %s: %v", name, list)
	}
	var names []string
	c.json(&names, "node", "list")
	if !slices.Contains(names, name) {
		t.Errorf("node list --format json does not include %s: %v", name, names)
	}

	env := uniqueName(t, "env")
	createEnvironment(c, env)
	file := writeJSON(t, cinc.Node{Name: name, Environment: env, RunList: []string{"role[web]"}})
	wantEqual(t, "edit output", c.run("node", "edit", name, "--file", file), fmt.Sprintf("Updated node %q\n", name))
	n = showNode(c, name)
	wantEqual(t, "environment after edit", n.Environment, env)
	wantSlice(t, "run list after edit", n.RunList, []string{"role[web]"})

	wantEqual(t, "delete output", c.run("node", "delete", name), fmt.Sprintf("Deleted node %q\n", name))
	wantNotFound(t, c.fail("node", "show", name))
	if list := strings.Fields(c.run("node", "list")); slices.Contains(list, name) {
		t.Errorf("node list still includes deleted %s", name)
	}
}

func testNodeCreateWithPolicy(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "node")
	createNode(c, name, "--policy-group", "prod", "--policy-name", "appserver")
	n := showNode(c, name)
	wantEqual(t, "policy_name", n.PolicyName, "appserver")
	wantEqual(t, "policy_group", n.PolicyGroup, "prod")
}

// testNodeCreateFromFile creates a node from a full JSON document, including
// attributes the flags cannot set, and checks the server kept them.
func testNodeCreateFromFile(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "node")
	file := writeJSON(t, map[string]any{
		"name":             name,
		"chef_environment": "_default",
		"run_list":         []string{"recipe[base]"},
		"normal":           map[string]any{"app": map[string]any{"port": 8080}},
		"default":          map[string]any{"tz": "UTC"},
	})
	c.run("node", "create", name, "--file", file)
	c.cleanup("node", "delete", name)

	var n map[string]any
	c.json(&n, "node", "show", name)
	normal, _ := n["normal"].(map[string]any)
	app, _ := normal["app"].(map[string]any)
	if app["port"] != float64(8080) {
		t.Errorf("normal.app.port = %v, want 8080 (node: %v)", app["port"], n)
	}
	def, _ := n["default"].(map[string]any)
	if def["tz"] != "UTC" {
		t.Errorf("default.tz = %v, want UTC (node: %v)", def["tz"], n)
	}
}

func testNodeNotFound(t *testing.T, _ Target, c *cli) {
	ghost := uniqueName(t, "ghost")
	wantNotFound(t, c.fail("node", "show", ghost))
	wantNotFound(t, c.fail("node", "delete", ghost))
	wantNotFound(t, c.fail("node", "run-list", "list", ghost))
	wantNotFound(t, c.fail("node", "tag", "list", ghost))
}

// testNodeEditMissing checks that editing a node that does not exist fails
// and does not create it.
func testNodeEditMissing(t *testing.T, _ Target, c *cli) {
	ghost := uniqueName(t, "ghost")
	c.cleanup("node", "delete", ghost)
	file := writeJSON(t, cinc.Node{Name: ghost, Environment: "_default"})
	wantNotFound(t, c.fail("node", "edit", ghost, "--file", file))
	wantNotFound(t, c.fail("node", "show", ghost))
}

func testNodeAlreadyExists(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "node")
	createNode(c, name)
	r := c.fail("node", "create", name)
	if !strings.Contains(strings.ToLower(r.stderr), "already exists") {
		t.Errorf("creating an existing node should say it already exists: %s", r)
	}
}

func testNodeRunList(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "node")
	createNode(c, name)

	c.run("node", "run-list", "set", name, "recipe[base],role[web]")
	wantSlice(t, "after set", showNode(c, name).RunList, []string{"recipe[base]", "role[web]"})

	c.run("node", "run-list", "add", name, "recipe[ntp]")
	wantSlice(t, "after add", showNode(c, name).RunList, []string{"recipe[base]", "role[web]", "recipe[ntp]"})

	out := c.run("node", "run-list", "remove", name, "role[web]")
	if !strings.Contains(out, "recipe[base]") || strings.Contains(out, "role[web]") {
		t.Errorf("remove output = %q, want role[web] gone", out)
	}
	wantSlice(t, "after remove", showNode(c, name).RunList, []string{"recipe[base]", "recipe[ntp]"})

	listed := c.run("node", "run-list", "list", name)
	if !strings.Contains(listed, "recipe[base]") || !strings.Contains(listed, "recipe[ntp]") {
		t.Errorf("run-list list = %q, want both remaining entries", listed)
	}
	var entries []string
	c.json(&entries, "node", "run-list", "list", name)
	wantSlice(t, "run-list list --format json", entries, []string{"recipe[base]", "recipe[ntp]"})
}

func testNodeTags(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "node")
	createNode(c, name)
	tags := func() []string {
		t.Helper()
		var got []string
		c.json(&got, "node", "tag", "list", name)
		slices.Sort(got)
		return got
	}

	c.run("node", "tag", "add", name, "prod,primary")
	wantSlice(t, "after add", tags(), []string{"primary", "prod"})
	if human := c.run("node", "tag", "list", name); !strings.Contains(human, "prod") || !strings.Contains(human, "primary") {
		t.Errorf("tag list = %q, want both tags", human)
	}

	c.run("node", "tag", "remove", name, "primary")
	wantSlice(t, "after remove", tags(), []string{"prod"})

	c.run("node", "tag", "set", name, "alpha,beta")
	wantSlice(t, "after set", tags(), []string{"alpha", "beta"})

	// Tags live in the node's normal attributes, where chef-client reads them.
	var n map[string]any
	c.json(&n, "node", "show", name)
	normal, _ := n["normal"].(map[string]any)
	if got := fmt.Sprint(normal["tags"]); got != "[alpha beta]" {
		t.Errorf("normal.tags = %s, want [alpha beta]", got)
	}
}

// testNodeStatus checks one node that has never checked in and one that
// checked in recently.
func testNodeStatus(t *testing.T, _ Target, c *cli) {
	never := uniqueName(t, "node")
	createNode(c, never)
	recent := uniqueName(t, "node")
	file := writeJSON(t, map[string]any{
		"name":             recent,
		"chef_environment": "_default",
		"automatic": map[string]any{
			"ohai_time": float64(time.Now().Add(-5 * time.Minute).Unix()),
			"fqdn":      recent + ".example.test",
			"platform":  "ubuntu",
		},
	})
	c.run("node", "create", recent, "--file", file)
	c.cleanup("node", "delete", recent)

	// node status reads every node through search, which erchef indexes
	// asynchronously.
	eventually(t, searchTimeout, func() error {
		out := c.run("node", "status")
		for _, line := range strings.Split(out, "\n") {
			switch {
			case strings.Contains(line, never) && !strings.Contains(line, "never"):
				return fmt.Errorf("%s should show as never checked in: %q", never, line)
			case strings.Contains(line, recent) && !strings.Contains(line, "m ago"):
				return fmt.Errorf("%s should show a check-in minutes ago: %q", recent, line)
			}
		}
		if !strings.Contains(out, never) || !strings.Contains(out, recent) {
			return fmt.Errorf("node status is missing a node:\n%s", out)
		}
		return nil
	})
}

func testNodeEnvironmentSet(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "node")
	createNode(c, name)
	env := uniqueName(t, "env")
	createEnvironment(c, env)

	out := c.run("node", "environment-set", name, env)
	if !strings.Contains(out, fmt.Sprintf("node %q environment to %q", name, env)) {
		t.Errorf("environment-set output = %q", out)
	}
	wantEqual(t, "environment", showNode(c, name).Environment, env)
}

func testNodePolicySet(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "node")
	createNode(c, name)
	out := c.run("node", "policy-set", name, "prod", "appserver")
	if !strings.Contains(out, fmt.Sprintf("node %q to policy %q in group %q", name, "appserver", "prod")) {
		t.Errorf("policy-set output = %q", out)
	}
	n := showNode(c, name)
	wantEqual(t, "policy_name", n.PolicyName, "appserver")
	wantEqual(t, "policy_group", n.PolicyGroup, "prod")
}

func testNodeSSHSkipSearch(t *testing.T, _ Target, c *cli) {
	s := startSSHServer(t, "hello from ssh\n")
	out := c.run(append([]string{"node", "ssh", "127.0.0.1", "echo hello", "--skip-search"}, s.sshArgs()...)...)
	wantEqual(t, "output", out, "127.0.0.1\thello from ssh\n")
	wantSlice(t, "commands", s.received(), []string{"echo hello"})
}

// testNodeSSHSearch finds the host through search, using a node whose fqdn
// is the local SSH server's address.
func testNodeSSHSearch(t *testing.T, _ Target, c *cli) {
	s := startSSHServer(t, "hello from search\n")
	name := uniqueName(t, "node")
	file := writeJSON(t, map[string]any{
		"name":             name,
		"chef_environment": "_default",
		"automatic":        map[string]any{"fqdn": "127.0.0.1"},
	})
	c.run("node", "create", name, "--file", file)
	c.cleanup("node", "delete", name)

	eventually(t, searchTimeout, func() error {
		r := c.exec(runOpts{}, append([]string{"node", "ssh", "name:" + name, "uptime"}, s.sshArgs()...)...)
		if r.exitCode != 0 || !strings.Contains(r.stdout, "hello from search") {
			return fmt.Errorf("node ssh by search: %s", r)
		}
		return nil
	})
	wantEqual(t, "command", s.received()[0], "uptime")
}

func testNodeBootstrap(t *testing.T, _ Target, c *cli) {
	s := startSSHServer(t, "bootstrap ok\n")
	name := uniqueName(t, "boot")
	c.cleanup("node", "delete", name)
	c.cleanup("client", "delete", name)

	out := c.run(append([]string{"node", "bootstrap", "127.0.0.1", "--node-name", name}, s.sshArgs()...)...)
	wantEqual(t, "output", out, fmt.Sprintf("Bootstrapped node %q on 127.0.0.1\n", name))

	cmds := s.received()
	if len(cmds) == 0 {
		t.Fatal("bootstrap ran no command over SSH")
	}
	script := strings.Join(cmds, "\n")
	for _, want := range []string{
		"curl -L 'https://omnitruck.cinc.sh/install.sh'",
		"node_name '" + name + "'",
		c.orgURL(c.tgt.Org),
		"cinc-client -j /etc/cinc/first-boot.json",
		"BEGIN RSA PRIVATE KEY",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("bootstrap script missing %q:\n%s", want, script)
		}
	}
	// Validatorless bootstrap: the CLI creates the node's client on the
	// server and ships its key in the script.
	var client map[string]any
	c.json(&client, "client", "show", name)
	if client["name"] != name && client["clientname"] != name {
		t.Errorf("client show after bootstrap = %v, want client %s", client, name)
	}
}

func testNodeBootstrapPolicy(t *testing.T, _ Target, c *cli) {
	s := startSSHServer(t, "bootstrap ok\n")
	name := uniqueName(t, "boot")
	c.cleanup("node", "delete", name)
	c.cleanup("client", "delete", name)

	c.run(append([]string{"node", "bootstrap", "127.0.0.1", "--node-name", name,
		"--policy-name", "appserver", "--policy-group", "prod"}, s.sshArgs()...)...)
	script := strings.Join(s.received(), "\n")
	for _, want := range []string{`"policy_name": "appserver"`, `"policy_group": "prod"`} {
		if !strings.Contains(script, want) {
			t.Errorf("policy bootstrap script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "chef_environment") {
		t.Errorf("policy bootstrap must not set chef_environment:\n%s", script)
	}
}
