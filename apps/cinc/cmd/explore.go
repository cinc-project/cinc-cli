package cmd

import (
	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/cli/client"
	"github.com/cinc-project/cinc-cli/cli/explore"
)

// newExploreCmd builds the `cinc explore` command: a k9s-style terminal
// UI for the whole Cinc Server.
func newExploreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "explore",
		Short: "Browse and edit the Cinc Server in a terminal UI",
		Example: `Launch the interactive terminal UI to browse nodes, roles, and more.
cinc explore`,
		Long: "Launches an interactive, k9s-style terminal UI for the whole Cinc\n" +
			"Server. Pick a profile (when more than one is configured), choose an\n" +
			"object type, and browse, view, edit, create, delete, or download\n" +
			"objects from a contextual action bar.\n\n" +
			"Move with the arrow keys, / to filter the loaded list, s to run a\n" +
			"server-side search, : for the object-type menu, enter to open or\n" +
			"drill in, esc to go back, and q to quit.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadCredentials(cmd)
			if err != nil {
				return err
			}
			return explore.Run(cmd.Context(), explore.Options{
				Profiles:    cfg.Profiles,
				Preselected: explicitProfile(cmd),
				NewClient:   client.New,
				Stdin:       cmd.InOrStdin(),
				Stdout:      cmd.OutOrStdout(),
				Stderr:      cmd.ErrOrStderr(),
			})
		},
	}
}
