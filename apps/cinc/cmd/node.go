package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	cinc "github.com/tas50/cinc-api"

	"github.com/cinc-project/cinc-cli/cli/components"
	"github.com/cinc-project/cinc-cli/cli/printer"
	"github.com/cinc-project/cinc-cli/cli/remote"
)

var nodeRemoteRunner remote.Runner = remote.NativeRunner{}

// newNodeCmd builds the `cinc node` command group.
func newNodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "Manage nodes on the Cinc Server",
	}
	cmd.AddCommand(newNodeListCmd())
	cmd.AddCommand(newNodeShowCmd())
	cmd.AddCommand(newNodeCreateCmd())
	cmd.AddCommand(newNodeEditCmd())
	cmd.AddCommand(newNodeDeleteCmd())
	cmd.AddCommand(newNodeSSHCmd())
	cmd.AddCommand(newNodeBootstrapCmd())
	cmd.AddCommand(newNodeRunListCmd())
	cmd.AddCommand(newNodeTagCmd())
	cmd.AddCommand(newNodeStatusCmd())
	cmd.AddCommand(newNodeEnvironmentSetCmd())
	cmd.AddCommand(newNodePolicySetCmd())
	cmd.AddCommand(newACLCmd("node", "nodes"))
	return cmd
}

// newNodeCreateCmd builds the `cinc node create <name>` command. By default it
// POSTs a minimal node carrying the name, an empty run list, and the optional
// --environment / --run-list values. With --file the full node JSON is read
// from disk, with the positional name overriding whatever "name" the file
// declares.
func newNodeCreateCmd() *cobra.Command {
	var (
		environment string
		runList     string
		policyName  string
		policyGroup string
		inputFile   string
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a node on the server",
		Example: `Create a node with a starting environment and run-list.
cinc node create web01 --environment prod --run-list 'recipe[base],role[web]'

Create a Policyfile-managed node pinned to a policy group.
cinc node create web01 --policy-group prod --policy-name base

Create a node from a JSON file.
cinc node create web01 --file web01.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if (policyName == "") != (policyGroup == "") {
				return fmt.Errorf("--policy-name and --policy-group must be provided together")
			}
			// A node is managed either by a run-list/environment or by a
			// Policyfile, never both — they are two incompatible methods.
			if policyName != "" && (cmd.Flags().Changed("run-list") || cmd.Flags().Changed("environment")) {
				return fmt.Errorf("--policy-name/--policy-group cannot be combined with --run-list or --environment (use a run-list/environment or a Policyfile, not both)")
			}
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			node := cinc.Node{Name: args[0], RunList: []string{}}
			if inputFile != "" {
				data, err := os.ReadFile(inputFile)
				if err != nil {
					return fmt.Errorf("cinc: read %s: %w", inputFile, err)
				}
				if err := json.Unmarshal(data, &node); err != nil {
					return fmt.Errorf("cinc: parse %s: %w", inputFile, err)
				}
				node.Name = args[0]
			}
			if environment != "" {
				node.Environment = environment
			}
			if cmd.Flags().Changed("run-list") {
				node.RunList = splitCSV(runList)
			}
			if node.RunList == nil {
				node.RunList = []string{}
			}
			if policyName != "" {
				node.PolicyName = policyName
				node.PolicyGroup = policyGroup
			}
			if _, _, err := c.Nodes.Create(cmd.Context(), &node); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created node %q\n", node.Name)
			return nil
		},
	}
	cmd.Flags().StringVarP(&environment, "environment", "E", "", "chef_environment for the new node")
	cmd.Flags().StringVar(&runList, "run-list", "", "comma-separated run list for the new node")
	cmd.Flags().StringVar(&policyName, "policy-name", "", "policy name for a Policyfile-managed node (requires --policy-group)")
	cmd.Flags().StringVar(&policyGroup, "policy-group", "", "policy group for a Policyfile-managed node (requires --policy-name)")
	cmd.Flags().StringVar(&inputFile, "file", "", "read the full node JSON from this file instead of using flags")
	return cmd
}

// newNodeEditCmd builds the `cinc node edit <name>` command. It fetches the
// node, opens its JSON in the shared editor, and PUTs the result back. The
// path arg pins the node name. `--file` reads the updated JSON from disk for
// scripted use.
func newNodeEditCmd() *cobra.Command {
	var inputFile string
	cmd := &cobra.Command{
		Use:   "edit <name>",
		Short: "Edit a node on the server",
		Example: `Open a node's JSON in your editor and save changes back.
cinc node edit web01`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			name := args[0]

			var updated cinc.Node
			if inputFile != "" {
				data, err := os.ReadFile(inputFile)
				if err != nil {
					return fmt.Errorf("cinc: read %s: %w", inputFile, err)
				}
				if err := json.Unmarshal(data, &updated); err != nil {
					return fmt.Errorf("cinc: parse %s: %w", inputFile, err)
				}
			} else {
				current, _, err := c.Nodes.Get(cmd.Context(), name)
				if err != nil {
					return err
				}
				edited, changed, err := editNodeForm(current)
				if err != nil {
					return err
				}
				if !changed {
					fmt.Fprintf(cmd.OutOrStdout(), "Node %q unchanged\n", name)
					return nil
				}
				updated = *edited
			}
			updated.Name = name
			if updated.RunList == nil {
				updated.RunList = []string{}
			}

			if _, _, err := c.Nodes.Update(cmd.Context(), &updated); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Updated node %q\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&inputFile, "file", "", "read the updated node JSON from this file instead of launching the editor")
	return cmd
}

type nodeSSHFlags struct {
	user         string
	password     string
	identityFile string
	agentSocket  string
	port         int
	timeout      time.Duration
	useAgent     bool
	verifyHost   bool
	noVerifyHost bool
	skipSearch   bool
	attribute    string
	concurrency  int
	exitOnError  bool
}

func newNodeSSHCmd() *cobra.Command {
	flags := nodeSSHFlags{port: 22, timeout: 30 * time.Second, useAgent: true, verifyHost: true, attribute: "fqdn", concurrency: 10}
	cmd := &cobra.Command{
		Use:   "ssh [search-query] [command]",
		Short: "Run an SSH command on nodes",
		Example: `Run a command on every node matching a search query.
cinc node ssh 'role:web' 'sudo systemctl restart nginx' --ssh-user ubuntu

Run a command on an explicit list of hosts, skipping search.
cinc node ssh 'web01 web02' uptime --ssh-user ubuntu --skip-search`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := resolveFormat(cmd)
			if err != nil {
				return err
			}
			query, sshCommand := argAt(args, 0), argAt(args, 1)
			if err := promptNodeSSH(cmd, &query, &sshCommand, &flags); err != nil {
				return err
			}
			targets, err := nodeSSHTargets(cmd, query, flags)
			if err != nil {
				return err
			}
			results := remote.RunMany(cmd.Context(), nodeRemoteRunner, targets, sshCommand, remoteOptions(flags), flags.concurrency, flags.exitOnError)
			if format == printer.FormatJSON {
				if err := printer.New(cmd.OutOrStdout(), format).Value(results); err != nil {
					return err
				}
			} else {
				printRemoteResults(cmd, results)
			}
			if failed := countRemoteFailures(results); failed > 0 {
				return fmt.Errorf("node ssh failed on %d host(s)", failed)
			}
			return nil
		},
	}
	addNodeSSHFlags(cmd, &flags)
	cmd.Flags().BoolVar(&flags.skipSearch, "skip-search", false, "skip Cinc Server search and treat search query as a space-separated host list")
	cmd.Flags().StringVar(&flags.attribute, "attribute", "fqdn", "node attribute used as the SSH host")
	cmd.Flags().IntVar(&flags.concurrency, "concurrency", 10, "maximum concurrent SSH sessions")
	cmd.Flags().BoolVar(&flags.exitOnError, "exit-on-error", false, "stop launching new SSH sessions after the first failure")
	return cmd
}

type nodeBootstrapFlags struct {
	nodeSSHFlags
	nodeName         string
	runList          string
	environment      string
	policyName       string
	policyGroup      string
	sudo             bool
	bootstrapVersion string
	bootstrapURL     string
	dryRun           bool
}

func newNodeBootstrapCmd() *cobra.Command {
	flags := nodeBootstrapFlags{
		nodeSSHFlags: nodeSSHFlags{port: 22, timeout: 30 * time.Second, useAgent: true, verifyHost: true},
		environment:  "_default",
		sudo:         true,
		bootstrapURL: remote.DefaultBootstrapURL,
	}
	cmd := &cobra.Command{
		Use:   "bootstrap [target]",
		Short: "Bootstrap a node with Cinc Client over SSH",
		Example: `Bootstrap a host with a first-run run-list.
cinc node bootstrap web01.example.com --ssh-user ubuntu --run-list 'recipe[base]'

Bootstrap a host managed by a Policyfile policy group.
cinc node bootstrap web01.example.com --ssh-user ubuntu --policy-name base --policy-group prod`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := argAt(args, 0)
			if err := promptNodeBootstrap(cmd, &target, &flags); err != nil {
				return err
			}
			if flags.nodeName == "" {
				flags.nodeName = target
			}
			if err := validateBootstrapFlags(flags, cmd.Flags().Changed("environment")); err != nil {
				return err
			}
			profile, err := resolveProfile(cmd)
			if err != nil {
				return err
			}
			// The bootstrap script hard-codes the server URL into the client.rb
			// it writes, so the profile must actually name a server. A real run
			// gets this from resolveClient below, but a dry run never calls it
			// and would otherwise print a script pointing at "/organizations/".
			if err := profile.Validate(); err != nil {
				return err
			}
			var privateKey string
			if !flags.dryRun {
				c, err := resolveClient(cmd)
				if err != nil {
					return err
				}
				publicKey := ""
				privateKey, publicKey, err = cinc.GenerateKeyPair()
				if err != nil {
					return fmt.Errorf("bootstrap: generate client key: %w", err)
				}
				req := &cinc.APIClient{Name: flags.nodeName}
				req.ChefKey.PublicKey = publicKey
				if _, _, err := c.Clients.Create(cmd.Context(), req); err != nil {
					return err
				}
			} else {
				privateKey = "DRY-RUN-CLIENT-KEY"
			}
			bootstrapCommand, err := remote.BootstrapCommand(remote.BootstrapOptions{
				NodeName:         flags.nodeName,
				ServerURL:        strings.TrimRight(profile.ServerURL, "/") + "/organizations/" + profile.Org,
				ClientKeyPEM:     privateKey,
				RunList:          splitCSV(flags.runList),
				Environment:      flags.environment,
				PolicyName:       flags.policyName,
				PolicyGroup:      flags.policyGroup,
				Sudo:             flags.sudo,
				BootstrapURL:     flags.bootstrapURL,
				BootstrapVersion: flags.bootstrapVersion,
			})
			if err != nil {
				return err
			}
			if flags.dryRun {
				fmt.Fprintln(cmd.OutOrStdout(), bootstrapCommand)
				return nil
			}
			result := nodeRemoteRunner.Run(cmd.Context(), remote.Target{Host: target}, bootstrapCommand, remoteOptions(flags.nodeSSHFlags))
			if result.ExitCode != 0 {
				return fmt.Errorf("bootstrap failed on %s: %s; client %q was created and may need cleanup before retry", target, firstNonEmpty(result.Error, result.Stderr), flags.nodeName)
			}
			format, err := resolveFormat(cmd)
			if err != nil {
				return err
			}
			if format == printer.FormatJSON {
				return printer.New(cmd.OutOrStdout(), format).Value(result)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Bootstrapped node %q on %s\n", flags.nodeName, target)
			return nil
		},
	}
	addNodeSSHFlags(cmd, &flags.nodeSSHFlags)
	cmd.Flags().StringVar(&flags.nodeName, "node-name", "", "node name to configure on the Cinc Server (default target host)")
	cmd.Flags().StringVar(&flags.runList, "run-list", "", "comma-separated first run-list")
	cmd.Flags().StringVar(&flags.environment, "environment", "_default", "first run environment")
	cmd.Flags().StringVar(&flags.policyName, "policy-name", "", "policyfile name for first run")
	cmd.Flags().StringVar(&flags.policyGroup, "policy-group", "", "policy group for first run")
	cmd.Flags().BoolVar(&flags.sudo, "sudo", true, "run bootstrap commands through sudo")
	cmd.Flags().StringVar(&flags.bootstrapVersion, "bootstrap-version", "", "Cinc Client version to install")
	cmd.Flags().StringVar(&flags.bootstrapURL, "bootstrap-url", remote.DefaultBootstrapURL, "Cinc install script URL")
	cmd.Flags().BoolVar(&flags.dryRun, "dry-run", false, "print the bootstrap script without creating a client or connecting")
	return cmd
}

// newNodeListCmd builds the `cinc node list` command.
func newNodeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List nodes on the server",
		Example: `List every node registered on the server.
cinc node list`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := resolveFormat(cmd)
			if err != nil {
				return err
			}
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			names, err := fetchNodeNames(cmd.Context(), c)
			if err != nil {
				return err
			}
			return printer.New(cmd.OutOrStdout(), format).List(names)
		},
	}
}

// newNodeShowCmd builds the `cinc node show <name>` command.
func newNodeShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show a node",
		Example: `Show a node's headline details: name, platform, run-list, environment, and policy.
cinc node show web01

Show the full node object, including all attributes, as JSON.
cinc node show web01 --format json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := resolveFormat(cmd)
			if err != nil {
				return err
			}
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			node, _, err := c.Nodes.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if format == printer.FormatJSON {
				return printer.New(cmd.OutOrStdout(), format).Value(node)
			}
			return writeNodeShowHuman(cmd, node)
		},
	}
}

// writeNodeShowHuman renders a node as a teammate-friendly summary: the node
// name (bold on a terminal) followed by its headline key/value pairs. The full
// object — including the large automatic attribute tree — is available via
// --format json; here we surface only what someone scanning a node cares about.
func writeNodeShowHuman(cmd *cobra.Command, node *cinc.Node) error {
	out := cmd.OutOrStdout()
	name := node.Name
	if boldEnabled(out) {
		name = "\x1b[1m" + name + "\x1b[0m"
	}
	fmt.Fprintln(out, name)

	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	// Optional rows only appear when the node actually carries the value.
	optional := func(label, value string) {
		if value != "" {
			fmt.Fprintf(tw, "%s\t%s\n", label, value)
		}
	}
	optional("Platform", node.AttributeString("platform"))
	optional("Platform Version", node.AttributeString("platform_version"))

	runList := "(none)"
	if len(node.RunList) > 0 {
		runList = strings.Join(node.RunList, ", ")
	}
	fmt.Fprintf(tw, "Run List\t%s\n", runList)

	environment := node.Environment
	if environment == "" {
		environment = "_default"
	}
	fmt.Fprintf(tw, "Environment\t%s\n", environment)

	optional("Policy Name", node.PolicyName)
	optional("Policy Group", node.PolicyGroup)
	return tw.Flush()
}

// newNodeDeleteCmd builds the `cinc node delete <name>` command.
func newNodeDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a node from the server",
		Example: `Delete a node from the server.
cinc node delete web01`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			name := args[0]
			if _, err := c.Nodes.Delete(cmd.Context(), name); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted node %q\n", name)
			return nil
		},
	}
}

// fetchNodeNames returns the sorted names of every node on the server.
func fetchNodeNames(ctx context.Context, c *cinc.Client) ([]string, error) {
	index, _, err := c.Nodes.List(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(index))
	for name := range index {
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}

func addNodeSSHFlags(cmd *cobra.Command, flags *nodeSSHFlags) {
	cmd.Flags().StringVar(&flags.user, "ssh-user", "", "SSH user")
	cmd.Flags().StringVar(&flags.password, "ssh-password", "", "SSH password")
	cmd.Flags().StringVar(&flags.identityFile, "ssh-identity-file", "", "SSH identity file")
	cmd.Flags().StringVar(&flags.agentSocket, "ssh-agent-socket", "", "SSH agent socket path")
	cmd.Flags().IntVar(&flags.port, "ssh-port", 22, "SSH port")
	cmd.Flags().DurationVar(&flags.timeout, "ssh-timeout", 30*time.Second, "SSH connection timeout")
	cmd.Flags().BoolVar(&flags.useAgent, "ssh-agent", true, "use SSH agent authentication")
	cmd.Flags().BoolVar(&flags.verifyHost, "host-key-verify", true, "verify SSH host keys using known_hosts")
	cmd.Flags().BoolVar(&flags.noVerifyHost, "no-host-key-verify", false, "disable SSH host key verification")
}

func promptNodeSSH(cmd *cobra.Command, query, sshCommand *string, flags *nodeSSHFlags) error {
	reader := bufio.NewReader(cmd.InOrStdin())
	out := cmd.OutOrStdout()
	var err error
	if *query == "" {
		*query, err = components.PromptWithDefault(reader, out, "Search query or host list", *query)
		if err != nil {
			return err
		}
	}
	if *sshCommand == "" {
		*sshCommand, err = components.PromptWithDefault(reader, out, "SSH command", *sshCommand)
		if err != nil {
			return err
		}
	}
	if flags.user == "" {
		flags.user, err = components.PromptWithDefault(reader, out, "SSH user", flags.user)
		if err != nil {
			return err
		}
	}
	if *query == "" || *sshCommand == "" || flags.user == "" {
		return fmt.Errorf("node ssh requires search query, command, and --ssh-user")
	}
	return nil
}

func promptNodeBootstrap(cmd *cobra.Command, target *string, flags *nodeBootstrapFlags) error {
	reader := bufio.NewReader(cmd.InOrStdin())
	out := cmd.OutOrStdout()
	var err error
	if *target == "" {
		*target, err = components.PromptWithDefault(reader, out, "Target host", *target)
		if err != nil {
			return err
		}
	}
	if flags.user == "" {
		flags.user, err = components.PromptWithDefault(reader, out, "SSH user", flags.user)
		if err != nil {
			return err
		}
	}
	if flags.nodeName == "" {
		flags.nodeName, err = components.PromptWithDefault(reader, out, "Node name", *target)
		if err != nil {
			return err
		}
	}
	if *target == "" || flags.user == "" || flags.nodeName == "" {
		return fmt.Errorf("node bootstrap requires target, --ssh-user, and node name")
	}
	return nil
}

func nodeSSHTargets(cmd *cobra.Command, query string, flags nodeSSHFlags) ([]remote.Target, error) {
	if flags.skipSearch {
		hosts := strings.Fields(query)
		targets := make([]remote.Target, 0, len(hosts))
		for _, host := range hosts {
			targets = append(targets, remote.Target{Host: host})
		}
		return targets, nil
	}
	c, err := resolveClient(cmd)
	if err != nil {
		return nil, err
	}
	rows, err := c.Search.SearchAll(cmd.Context(), "node", expandNodeSSHQuery(query))
	if err != nil {
		return nil, err
	}
	targets := make([]remote.Target, 0, len(rows))
	for _, row := range rows {
		host, err := searchRowAttribute(row, flags.attribute)
		if err != nil {
			return nil, err
		}
		if host != "" {
			targets = append(targets, remote.Target{Host: host})
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("node ssh found no hosts for query %q", query)
	}
	return targets, nil
}

func expandNodeSSHQuery(query string) string {
	if strings.Contains(query, ":") {
		return query
	}
	return "tags:*" + query + "* OR roles:*" + query + "* OR fqdn:*" + query + "* OR addresses:*" + query + "*"
}

func searchRowAttribute(row json.RawMessage, attr string) (string, error) {
	var data any
	if err := json.Unmarshal(row, &data); err != nil {
		return "", err
	}
	for _, path := range candidateAttributePaths(attr) {
		if value, ok := lookupAttribute(data, path); ok {
			return attributeString(value), nil
		}
	}
	return "", fmt.Errorf("search row missing SSH attribute %q", attr)
}

func candidateAttributePaths(attr string) [][]string {
	if strings.Contains(attr, ".") {
		return [][]string{strings.Split(attr, ".")}
	}
	return [][]string{{attr}, {"automatic", attr}, {"normal", attr}, {"default", attr}, {"override", attr}}
}

func lookupAttribute(data any, path []string) (any, bool) {
	current := data
	for _, part := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func attributeString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []any:
		if len(v) == 0 {
			return ""
		}
		return attributeString(v[0])
	default:
		return fmt.Sprint(v)
	}
}

func remoteOptions(flags nodeSSHFlags) remote.SSHOptions {
	return remote.SSHOptions{
		User: flags.user, Password: flags.password, IdentityFile: flags.identityFile, AgentSocket: flags.agentSocket,
		Port: flags.port, Timeout: flags.timeout, UseAgent: flags.useAgent, VerifyHost: flags.verifyHost && !flags.noVerifyHost,
	}
}

func printRemoteResults(cmd *cobra.Command, results []remote.CommandResult) {
	for _, result := range results {
		writePrefixedLines(cmd.OutOrStdout(), result.Host, result.Stdout)
		writePrefixedLines(cmd.OutOrStdout(), result.Host, result.Stderr)
		if result.Error != "" && result.Stdout == "" && result.Stderr == "" {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", result.Host, result.Error)
		}
	}
}

func writePrefixedLines(out interface{ Write([]byte) (int, error) }, host, text string) {
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if line != "" {
			fmt.Fprintf(out, "%s\t%s\n", host, line)
		}
	}
}

func countRemoteFailures(results []remote.CommandResult) int {
	var failed int
	for _, result := range results {
		if result.ExitCode != 0 {
			failed++
		}
	}
	return failed
}

func validateBootstrapFlags(flags nodeBootstrapFlags, environmentChanged bool) error {
	if (flags.policyName == "") != (flags.policyGroup == "") {
		return fmt.Errorf("--policy-name and --policy-group must be provided together")
	}
	// A node is managed either by a run-list/environment or by a Policyfile,
	// never both — they are two incompatible methods. (--environment defaults to
	// "_default", so only reject an explicitly set value.)
	if flags.policyName != "" && flags.runList != "" {
		return fmt.Errorf("--run-list cannot be combined with policy bootstrap flags")
	}
	if flags.policyName != "" && environmentChanged {
		return fmt.Errorf("--environment cannot be combined with policy bootstrap flags (use a run-list/environment or a Policyfile, not both)")
	}
	return nil
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func argAt(args []string, i int) string {
	if i >= len(args) {
		return ""
	}
	return args[i]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "unknown error"
}
