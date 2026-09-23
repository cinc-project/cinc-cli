package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/cli/client"
	"github.com/cinc-project/cinc-cli/cli/config"
	"github.com/cinc-project/cinc-cli/cli/progname"
)

// newRootCmd builds the root `cinc` command and registers its
// subcommands.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "cinc",
		Short: "Cinc is a unified command-line tool for Cinc/Chef Infra",
		// Execute prints errors itself so it can swallow the
		// first-run sentinel; cobra's own error reporting is silenced
		// here to avoid double-printing or leaking the sentinel.
		SilenceUsage:  true,
		SilenceErrors: true,
		// When invoked with no subcommand we still want a chance to
		// offer chef-credentials migration before falling back to the
		// help text. Subcommands keep their own behavior — this RunE
		// only fires for a bare `cinc` invocation.
		RunE: rootRunE,
		// --format is a persistent flag, so every command accepts it, but
		// only the commands that print structured output ever read it.
		// Check it here, before any command runs, so a typo is refused
		// before a create has already changed the server.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			_, err := resolveFormat(cmd)
			return err
		},
	}

	flags := root.PersistentFlags()
	flags.String("config", "", "path to the Cinc credentials file (default ~/.cinc/credentials)")
	flags.String("profile", "", "credentials profile to use (default: $CINC_PROFILE, then $CHEF_PROFILE, then \"default\")")
	flags.String("format", "human", "output format: human or json")

	root.AddCommand(newVersionCmd())
	root.AddCommand(newConfigCmd())
	root.AddCommand(newNodeCmd())
	root.AddCommand(newClientCmd())
	root.AddCommand(newUserCmd())
	root.AddCommand(newGroupCmd())
	root.AddCommand(newOrgCmd())
	root.AddCommand(newRoleCmd())
	root.AddCommand(newEnvironmentCmd())
	root.AddCommand(newCookbookCmd())
	root.AddCommand(newDataBagCmd())
	root.AddCommand(newPolicyCmd())
	root.AddCommand(newPolicyGroupCmd())
	root.AddCommand(newSupermarketCmd())
	root.AddCommand(newSearchCmd())
	root.AddCommand(newExploreCmd())

	return root
}

// Execute builds and runs the root command. It is the single entry point
// called by main(). The first-run sentinel is swallowed so a fresh
// user who just got walked through `cinc config create` exits at the
// "you're ready to go" message instead of having their original
// server-touching command run on the brand-new profile.
func Execute() error {
	name := programNameFromArgv(os.Args[0])
	progname.Set(name)
	root := newRootCmd()
	applyProgramName(root, name)
	err := root.Execute()
	if errors.Is(err, errFirstRunCompleted) {
		return nil
	}
	// Some commands (e.g. `config validate`) already print their own detail;
	// exit non-zero without a second generic "Error: ..." line.
	if err != nil && !errors.Is(err, errAlreadyReported) {
		// A transport failure the user can fix (an untrusted certificate,
		// a server that isn't listening) is explained rather than printed
		// as Go's error chain.
		fmt.Fprintln(root.ErrOrStderr(), "Error:", client.Explain(err))
	}
	return err
}

// safeProgramName is the shape a program name has to have before it is
// echoed into help text and, via cobra, into generated shell-completion
// scripts. argv[0] is caller-controlled (a symlink name, `exec -a`), and
// cobra interpolates the root name into completion scripts unquoted, so an
// unvalidated name is a command-injection vector for anyone who sources
// them. Anything outside this shape falls back to the canonical name.
var safeProgramName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// programNameFromArgv returns the name the binary was invoked under, so a
// packager can install it as something other than `cinc` (the name is
// taken by other tools in some distributions) and the help text, the
// completion scripts and the "run `<name> ...`" hints all follow along.
// Falls back to the canonical name when argv[0] is unusable or unsafe.
func programNameFromArgv(argv0 string) string {
	name := filepath.Base(argv0)
	// Windows resolves executables case-insensitively, so CINC.EXE and
	// cinc.exe are both ordinary invocations.
	if ext := filepath.Ext(name); strings.EqualFold(ext, ".exe") {
		name = strings.TrimSuffix(name, ext)
	}
	if !safeProgramName.MatchString(name) {
		return progname.Default
	}
	return name
}

// applyProgramName renames the whole command tree. Cobra derives usage
// lines, "help for <name>" and completion registration from the root Use,
// but it prints Short/Long/Example verbatim, and those carry ~100 authored
// `cinc <verb>` command lines. Rewriting them here keeps a renamed binary
// from printing its own usage line directly above an example for a command
// the user does not have.
//
// Only Execute applies it: tests and the doc generator build the tree
// through newRootCmd/NewRootCmd and keep the canonical name, so generated
// docs are unaffected.
func applyProgramName(root *cobra.Command, name string) {
	if name == progname.Default {
		return
	}
	root.Use = name
	rewriteCommandText(root, name)
}

// rewriteCommandText replaces the canonical program name with name in every
// command's authored help text, depth-first over the whole tree.
func rewriteCommandText(cmd *cobra.Command, name string) {
	old := progname.Default + " "
	replacement := name + " "
	cmd.Short = strings.ReplaceAll(cmd.Short, old, replacement)
	cmd.Long = strings.ReplaceAll(cmd.Long, old, replacement)
	cmd.Example = strings.ReplaceAll(cmd.Example, old, replacement)
	for _, child := range cmd.Commands() {
		rewriteCommandText(child, name)
	}
}

// NewRootCmd returns a fresh root command tree. It exists so that
// out-of-tree tools (for example the doc generator under
// `tools/gendocs`) can walk the command tree without going through
// Execute. Tests inside this package should keep using the unexported
// newRootCmd.
func NewRootCmd() *cobra.Command {
	return newRootCmd()
}

// rootRunE handles a bare `cinc` invocation. If the default
// credentials file is missing it runs the first-run flow (chef
// migration when a knife config is present, otherwise an inline
// configure walk-through), then prints the usage help so the user
// still sees what commands are available. If the user explicitly
// declines a setup prompt we exit cleanly instead — they asked not to
// be set up, so dumping help on them would be noise. First-run
// failures are surfaced but do not block help.
func rootRunE(cmd *cobra.Command, _ []string) error {
	cincPath, err := config.DefaultPath()
	if err == nil {
		if _, err := os.Stat(cincPath); errors.Is(err, fs.ErrNotExist) {
			_, declined, runErr := offerFirstRun(cmd, cincPath)
			if runErr != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), runErr)
			}
			if declined {
				return nil
			}
		}
	}
	return cmd.Help()
}
