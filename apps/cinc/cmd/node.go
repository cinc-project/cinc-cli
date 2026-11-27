package cmd

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"iter"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	cinc "github.com/cinc-project/cinc-api"
	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/cli/components"
	"github.com/cinc-project/cinc-cli/cli/printer"
	"github.com/cinc-project/cinc-cli/cli/progname"
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
	cmd.AddCommand(newACLCmd("node", cinc.ACLNodes))
	return cmd
}

// newNodeCreateCmd builds the `cinc node create <name>` command. By default it
// POSTs a minimal node carrying the name, an empty run list, and the optional
// --environment / --run-list values. With --file the full node JSON is read
// from disk; a file naming a different node is refused (see checkFileName),
// and one with no name gets the positional name.
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
			node := cinc.Node{Name: args[0]}
			if inputFile != "" {
				if node, err = readJSONFile[cinc.Node](inputFile); err != nil {
					return err
				}
				if err := checkFileName(inputFile, "node", "name", node.Name, args[0]); err != nil {
					return err
				}
				node.Name = args[0]
			}
			if environment != "" {
				node.Environment = environment
			}
			if cmd.Flags().Changed("run-list") {
				node.RunList = splitCSV(runList)
			}
			if policyName != "" {
				node.PolicyName = policyName
				node.PolicyGroup = policyGroup
			}
			if _, err := c.Nodes.Create(cmd.Context(), &node); err != nil {
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
// node, opens it in the node edit form, and PUTs the result back unless
// nothing changed. The path arg pins the node name. `--file` replaces the
// node with the JSON read from disk, for scripted use, and is refused if it
// names a different node.
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

			if inputFile == "" {
				_, changed, err := c.Nodes.Modify(cmd.Context(), name, func(n *cinc.Node) error {
					edited, changed, err := editNodeForm(n)
					if err != nil || !changed {
						return err
					}
					*n = *edited
					n.Name = name
					return nil
				})
				if err != nil {
					return err
				}
				if !changed {
					fmt.Fprintf(cmd.OutOrStdout(), "Node %q unchanged\n", name)
					return nil
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Updated node %q\n", name)
				return nil
			}

			updated, err := readJSONFile[cinc.Node](inputFile)
			if err != nil {
				return err
			}
			if err := checkFileName(inputFile, "node", "name", updated.Name, name); err != nil {
				return err
			}
			updated.Name = name
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
			// Checked once, up front: a bad key file or missing known_hosts
			// would otherwise fail every host the same way, after the search.
			if err := remote.CheckOptions(remoteOptions(flags)); err != nil {
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
			return remoteFailureError(results, format == printer.FormatJSON)
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
		port: 22, timeout: 30 * time.Second, useAgent: true, verifyHost: true,
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
			// Resolved up front, before anything with a side effect: bootstrap
			// creates a client on the server and installs software on the
			// target, and a rejected --format should not cost the user either.
			format, err := resolveFormat(cmd)
			if err != nil {
				return err
			}
			target := argAt(args, 0)
			if err := promptNodeBootstrap(cmd, &target, &flags); err != nil {
				return err
			}
			if flags.nodeName == "" {
				flags.nodeName = target
			}
			if err := validateBootstrapFlags(target, flags, cmd.Flags().Changed("environment")); err != nil {
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
			bootstrapOpts := remote.BootstrapOptions{
				NodeName:         flags.nodeName,
				ServerURL:        cinc.FormatServerURL(profile.ServerURL, profile.Org),
				RunList:          splitCSV(flags.runList),
				Environment:      flags.environment,
				PolicyName:       flags.policyName,
				PolicyGroup:      flags.policyGroup,
				Sudo:             flags.sudo,
				BootstrapURL:     flags.bootstrapURL,
				BootstrapVersion: flags.bootstrapVersion,
			}
			// Everything that can be checked is checked before the client
			// is created: a failure after that leaves a client on the server
			// that the user has to delete before retrying.
			if err := remote.ValidateBootstrapOptions(bootstrapOpts); err != nil {
				return err
			}
			if !flags.dryRun {
				if err := remote.CheckOptions(remoteOptions(flags.nodeSSHFlags)); err != nil {
					return err
				}
				c, err := resolveClient(cmd)
				if err != nil {
					return err
				}
				privateKey, publicKey, err := cinc.GenerateKeyPair()
				if err != nil {
					return fmt.Errorf("couldn't generate a private key for the new client %q on this machine: %w. Nothing was sent to the server, so it's safe to try again.", flags.nodeName, err)
				}
				bootstrapOpts.ClientKeyPEM = privateKey
				req := &cinc.APIClient{Name: flags.nodeName}
				req.PublicKey = publicKey
				if _, _, err := c.Clients.Create(cmd.Context(), req); err != nil {
					return err
				}
			} else {
				bootstrapOpts.ClientKeyPEM = "DRY-RUN-CLIENT-KEY"
			}
			bootstrapCommand, err := remote.BootstrapCommand(bootstrapOpts)
			if err != nil {
				return err
			}
			if flags.dryRun {
				fmt.Fprintln(cmd.OutOrStdout(), bootstrapCommand)
				return nil
			}
			result := nodeRemoteRunner.Run(cmd.Context(), remote.Target{Host: target}, bootstrapCommand, remoteOptions(flags.nodeSSHFlags))
			if result.ExitCode != 0 {
				return bootstrapFailedError(target, flags.nodeName, result)
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
			names, err := listNames(cmd.Context(), c.Nodes.List)
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

	fmt.Fprintf(tw, "Environment\t%s\n", node.EnvironmentName())

	optional("Policy Name", node.PolicyName)
	optional("Policy Group", node.PolicyGroup)
	return tw.Flush()
}

// newNodeDeleteCmd builds the `cinc node delete <name>` command.
func newNodeDeleteCmd() *cobra.Command {
	return addYesFlag(&cobra.Command{
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
	}, nil, "")
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
	return nodeSSHMissingError(*query, *sshCommand, flags.user)
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
	return nodeBootstrapMissingError(*target, flags.user, flags.nodeName)
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
	var targets []remote.Target
	expanded := expandNodeSSHQuery(query)
	matched := 0
	for node, err := range searchNodesPartial(cmd.Context(), c, expanded, flags.attribute) {
		if err != nil {
			return nil, err
		}
		matched++
		host, err := nodeSSHHost(node, flags.attribute)
		if err != nil {
			return nil, err
		}
		if host != "" {
			targets = append(targets, remote.Target{Host: host})
		}
	}
	if len(targets) == 0 {
		return nil, noHostsError(query, expanded, flags.attribute, matched)
	}
	return targets, nil
}

// searchNodesPartial searches the node index for just the node name and the
// given attributes, rather than whole nodes, whose automatic attributes alone
// often run past 100 KB each. Each attribute may be a dotted path such as
// cloud.public_hostname.
//
// The server projects from the node's merged attributes (automatic over
// override over normal over default), so each value in a row's Data is
// already the one Chef's read precedence picks, keyed by the path asked for.
// An attribute the node doesn't have comes back as null.
//
// The row's name comes from its URL. The projection still asks for "name",
// which the library falls back to if a server's URL doesn't end in the node
// name, and which keeps the projection non-empty for --attribute name.
func searchNodesPartial(ctx context.Context, c *cinc.Client, query string, attrs ...string) iter.Seq2[*cinc.PartialNode, error] {
	paths := []string{"name"}
	for _, attr := range attrs {
		if !slices.Contains(paths, attr) {
			paths = append(paths, attr)
		}
	}
	return c.Search.PartialNodes(ctx, query, paths)
}

func expandNodeSSHQuery(query string) string {
	if strings.Contains(query, ":") {
		return query
	}
	return "tags:*" + query + "* OR roles:*" + query + "* OR fqdn:*" + query + "* OR addresses:*" + query + "*"
}

// nodeSSHHost reads the SSH host attribute from a partial search row. attr is
// the projected path, possibly dotted such as cloud.public_hostname, whose
// value the server has already resolved by Chef's read precedence. "name" is
// the node's own name, which isn't an attribute.
func nodeSSHHost(node *cinc.PartialNode, attr string) (string, error) {
	if attr == "name" {
		return node.Name, nil
	}
	value, ok := node.Value(attr)
	if !ok || value == nil {
		return "", fmt.Errorf("node %q has no %q attribute to connect to. Pick another with --attribute, or pass --attribute name to use the node name", node.Name, attr)
	}
	host, ok := node.Scalar(attr)
	if !ok {
		return "", fmt.Errorf("node %q's %q attribute isn't a single value we can connect to%s", node.Name, attr, nestedAttributeHint(attr, value))
	}
	return host, nil
}

// nestedAttributeHint suggests dotted paths into an attribute that holds an
// object, e.g. "; try --attribute cloud.public_hostname".
func nestedAttributeHint(attr string, value any) string {
	m, ok := value.(map[string]any)
	if !ok || len(m) == 0 {
		return ""
	}
	keys := slices.Sorted(maps.Keys(m))
	if len(keys) > 3 {
		keys = keys[:3]
	}
	paths := make([]string, len(keys))
	for i, k := range keys {
		paths[i] = attr + "." + k
	}
	return "; it holds an object, so name a key inside it, such as --attribute " + strings.Join(paths, " or ")
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
	for line := range strings.SplitSeq(strings.TrimRight(text, "\n"), "\n") {
		if line != "" {
			fmt.Fprintf(out, "%s\t%s\n", host, line)
		}
	}
}

func validateBootstrapFlags(target string, flags nodeBootstrapFlags, environmentChanged bool) error {
	prog := progname.Get()
	pinned := "A Policyfile-managed node is pinned by two things: the policy it runs (--policy-name) and the policy group that picks which revision of that policy it gets (--policy-group), such as prod or staging."
	switch {
	case flags.policyName != "" && flags.policyGroup == "":
		return fmt.Errorf("--policy-name %s needs a --policy-group too. %s For example:\n  %s node bootstrap %s --ssh-user %s --policy-name %s --policy-group prod",
			remote.QuoteArg(flags.policyName), pinned, prog, remote.QuoteArg(target), remote.QuoteArg(flags.user), remote.QuoteArg(flags.policyName))
	case flags.policyGroup != "" && flags.policyName == "":
		return fmt.Errorf("--policy-group %s needs a --policy-name too. %s For example:\n  %s node bootstrap %s --ssh-user %s --policy-name base --policy-group %s",
			remote.QuoteArg(flags.policyGroup), pinned, prog, remote.QuoteArg(target), remote.QuoteArg(flags.user), remote.QuoteArg(flags.policyGroup))
	}
	// A node is managed either by a run-list/environment or by a Policyfile,
	// never both: they are two incompatible methods. (--environment defaults
	// to "_default", so only reject an explicitly set value.)
	if flags.policyName != "" && flags.runList != "" {
		return fmt.Errorf("--run-list can't be combined with --policy-name and --policy-group. A node gets its recipes either from a run-list or from a Policyfile, never both, and the policy %q already carries its own run-list. Drop --run-list to bootstrap with the policy, or drop --policy-name and --policy-group to use the run-list.", flags.policyName)
	}
	if flags.policyName != "" && environmentChanged {
		return fmt.Errorf("--environment can't be combined with --policy-name and --policy-group. Environments are for nodes managed by a run-list; a Policyfile-managed node is pinned by its policy group (%q here) instead. Drop --environment to bootstrap with the policy, or drop --policy-name and --policy-group to use an environment and a run-list.", flags.policyGroup)
	}
	return nil
}

// nodeSSHMissingError names whatever node ssh still lacks after prompting,
// with an example built from the parts that were given.
func nodeSSHMissingError(query, sshCommand, user string) error {
	var missing []string
	if query == "" {
		missing = append(missing, "a search query or host list (which nodes to connect to)")
	}
	if sshCommand == "" {
		missing = append(missing, "a command to run on them")
	}
	if user == "" {
		missing = append(missing, "a user to log in as (--ssh-user)")
	}
	if len(missing) == 0 {
		return nil
	}
	prog := progname.Get()
	msg := fmt.Sprintf("node ssh needs %s. For example:\n  %s node ssh %s %s --ssh-user %s",
		remote.JoinWords(missing), prog,
		remote.QuoteArg(cmp.Or(query, "role:web")), remote.QuoteArg(cmp.Or(sshCommand, "uptime")), remote.QuoteArg(cmp.Or(user, "ubuntu")))
	if query == "" {
		msg += fmt.Sprintf(`

The first argument is a Cinc search query, such as role:web, name:web01, or platform:ubuntu. To list hosts yourself instead of searching, add --skip-search:
  %s node ssh 'web01.example.com web02.example.com' uptime --ssh-user ubuntu --skip-search`, prog)
	}
	return errors.New(msg)
}

// nodeBootstrapMissingError names whatever node bootstrap still lacks after
// prompting.
func nodeBootstrapMissingError(target, user, nodeName string) error {
	var missing []string
	if target == "" {
		missing = append(missing, "the host to bootstrap")
	}
	if user == "" {
		missing = append(missing, "a user to log in as (--ssh-user)")
	}
	if nodeName == "" && target != "" {
		missing = append(missing, "a node name (--node-name)")
	}
	if len(missing) == 0 {
		return nil
	}
	msg := fmt.Sprintf("node bootstrap needs %s. For example:\n  %s node bootstrap %s --ssh-user %s",
		remote.JoinWords(missing), progname.Get(), remote.QuoteArg(cmp.Or(target, "web01.example.com")), remote.QuoteArg(cmp.Or(user, "ubuntu")))
	if target == "" {
		msg += "\n\nThe host is the machine to set up, by name or IP address. Bootstrap logs in to it over SSH, installs Cinc Client, and registers it with your Cinc Server as a node, named after the host unless you pass --node-name."
	}
	return errors.New(msg)
}

// noHostsError explains a node ssh search that found nothing to connect to.
func noHostsError(query, expanded, attribute string, matched int) error {
	prog := progname.Get()
	if matched > 0 {
		return fmt.Errorf("%d node(s) matched %q, but none has a value in its %s attribute to connect to. Pick another attribute with --attribute, such as --attribute ipaddress, or use --attribute name to connect by node name.", matched, query, attribute)
	}
	if query == expanded {
		return fmt.Errorf(`no nodes matched %q, so there's nothing to connect to.

Queries are field:value pairs, such as role:web, name:web01, or platform:ubuntu, and * matches anything (name:web*). See what a query finds before running anything with:
  %s search node %s`, query, prog, remote.QuoteArg(query))
	}
	return fmt.Errorf(`no nodes matched %q, so there's nothing to connect to.

A word without a colon is a shortcut: it matches nodes whose tags, roles, fqdn, or IP addresses contain it, so we searched for:
  %s
To search one field instead, use field:value, such as role:web or name:web01. See what a query finds before running anything with:
  %s search node role:web
If %s is a host name rather than a search, add --skip-search to connect to it directly.`, query, expanded, prog, query)
}

// remoteFailureError summarizes a node ssh run. Each host's one-line error
// is already printed beside its output; this explains each kind of problem
// once, naming the hosts it hit, so a fleet failing the same way doesn't
// repeat the same explanation hundreds of times.
func remoteFailureError(results []remote.CommandResult, jsonOutput bool) error {
	var (
		failed, exited, skipped []string
		reasons                 []string
		byReason                = map[string][]remote.CommandResult{}
	)
	for _, r := range results {
		switch {
		case r.Skipped:
			skipped = append(skipped, r.Host)
		case r.ExitCode != 0:
			failed = append(failed, r.Host)
			if r.Reason == "" {
				exited = append(exited, r.Host)
				continue
			}
			if _, seen := byReason[r.Reason]; !seen {
				reasons = append(reasons, r.Reason)
			}
			byReason[r.Reason] = append(byReason[r.Reason], r)
		}
	}
	if len(failed) == 0 {
		return nil
	}
	where := "Each host's output is above, with the host name at the start of every line."
	if jsonOutput {
		where = "Each host's result is in the JSON above."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "node ssh failed on %d of %s: %s. %s", len(failed), hostCount(len(results)), hostList(failed), where)
	for _, reason := range reasons {
		hit := byReason[reason]
		first := hit[0]
		fmt.Fprintf(&b, "\n\n%s: %s", first.Host, first.Error)
		if first.Hint != "" {
			fmt.Fprintf(&b, "\n%s", first.Hint)
		}
		if len(hit) > 1 {
			others := make([]string, 0, len(hit)-1)
			for _, r := range hit[1:] {
				others = append(others, r.Host)
			}
			fmt.Fprintf(&b, "\nThe same problem hit %s: %s.", hostCount(len(others)), hostList(others))
		}
	}
	if len(exited) > 0 {
		fmt.Fprintf(&b, "\n\nThe command ran but exited with an error on %s; its output above shows why.", hostList(exited))
	}
	if len(skipped) > 0 {
		fmt.Fprintf(&b, "\n\n%s weren't tried, because --exit-on-error stops starting new hosts after the first failure.", hostCount(len(skipped)))
	}
	return errors.New(b.String())
}

func hostCount(n int) string {
	if n == 1 {
		return "1 host"
	}
	return fmt.Sprintf("%d hosts", n)
}

// hostList names up to ten hosts, then says how many more there were.
func hostList(hosts []string) string {
	const shown = 10
	if len(hosts) <= shown {
		return strings.Join(hosts, ", ")
	}
	return fmt.Sprintf("%s, and %d more", strings.Join(hosts[:shown], ", "), len(hosts)-shown)
}

// bootstrapFailedError explains a failed bootstrap and how to clean up the
// client it had already registered, which otherwise blocks the retry.
func bootstrapFailedError(target, nodeName string, result remote.CommandResult) error {
	prog := progname.Get()
	name := remote.QuoteArg(nodeName)
	cleanup := fmt.Sprintf(`Before connecting, bootstrap registered a client named %q on your Cinc Server, holding the key the node uses to talk to the server. A retry fails while that client exists, so delete it first:
  %s client delete %s`, nodeName, prog, name)
	if result.Reason != "" {
		msg := fmt.Sprintf("bootstrap couldn't run on %s: %s", target, result.Error)
		if result.Hint != "" {
			msg += "\n\n" + result.Hint
		}
		return fmt.Errorf("%s\n\nNothing was installed on %s. %s", msg, target, cleanup)
	}
	msg := fmt.Sprintf("bootstrap failed on %s: the bootstrap script exited with status %d", target, result.ExitCode)
	if result.Error != "" && !strings.HasPrefix(result.Error, "Process exited with status") {
		msg += " (" + result.Error + ")"
	}
	if tail := lastLines(cmp.Or(result.Stderr, result.Stdout), 15); tail != "" {
		msg += ". The last lines it printed:\n" + tail
	}
	return fmt.Errorf(`%s

The script installs Cinc Client on the host, then runs it once to register the node. %s
If that first Cinc Client run got far enough to save the node, `+"`%s node show %s`"+` will find it. Delete it too before retrying:
  %s node delete %s`, msg, cleanup, prog, name, prog, name)
}

// lastLines returns the last n non-empty lines of text, indented.
func lastLines(text string, n int) string {
	var lines []string
	for line := range strings.SplitSeq(strings.TrimRight(text, "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, "  "+line)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// gatherCSVArgs flattens args that may each be a single entry or a
// comma-separated list of entries into one ordered slice.
func gatherCSVArgs(args []string) []string {
	var out []string
	for _, arg := range args {
		out = append(out, splitCSV(arg)...)
	}
	return out
}

// splitCSV splits a comma-separated list, trimming each entry and dropping
// empty ones. It returns nil for an empty string.
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
