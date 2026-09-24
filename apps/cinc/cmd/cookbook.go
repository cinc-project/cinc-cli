package cmd

import (
	"fmt"
	"path/filepath"

	cinc "github.com/cinc-project/cinc-api"

	"github.com/spf13/cobra"

	localcookbook "github.com/cinc-project/cinc-cli/cli/cookbook"
	"github.com/cinc-project/cinc-cli/cli/printer"
)

// newCookbookCmd builds the `cinc cookbook` command group.
func newCookbookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cookbook",
		Short: "Manage cookbooks on the Cinc Server",
	}
	cmd.AddCommand(newCookbookListCmd())
	cmd.AddCommand(newCookbookShowCmd())
	cmd.AddCommand(newCookbookDeleteCmd())
	cmd.AddCommand(newCookbookUploadCmd())
	cmd.AddCommand(newCookbookDownloadCmd())
	cmd.AddCommand(newACLCmd("cookbook", cinc.ACLCookbooks))
	return cmd
}

// newCookbookDownloadCmd builds the `cinc cookbook download <name> [version]`
// command. With no version it asks the server for cinc.LatestVersion, which
// resolves to the highest version. Every file in the version's manifest is
// written under <dir>/<name>-<version>/ (recreating the cookbook layout),
// where <dir> defaults to the current directory and is overridable with
// --dir, matching knife's `cookbook download`.
func newCookbookDownloadCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "download <name> [version]",
		Short: "Download a cookbook version from the server",
		Example: `Download a cookbook's latest version into ./<name>-<version>/.
cinc cookbook download nginx
Download a specific version into a chosen directory.
cinc cookbook download nginx 1.2.0 --dir ./cookbooks`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			name := args[0]
			version := cinc.LatestVersion
			if len(args) == 2 {
				version = args[1]
			}
			// Fetch the manifest first so the directory is named after the
			// concrete version, never "_latest", then download from that same
			// manifest.
			cb, _, err := c.Cookbooks.Get(cmd.Context(), name, version)
			if err != nil {
				return err
			}
			destDir := filepath.Join(dir, name+"-"+cb.Version)
			if err := c.Cookbooks.DownloadFiles(cmd.Context(), cb, destDir); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Downloaded cookbook %q version %s to %s\n", name, cb.Version, destDir)
			return nil
		},
	}
	cmd.Flags().StringVarP(&dir, "dir", "d", ".", "parent directory to download the cookbook into")
	return cmd
}

// newCookbookShowCmd builds the `cinc cookbook show <name> [version]`
// command. With no version it shows cinc.LatestVersion, which the server
// resolves to the highest version.
func newCookbookShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name> [version]",
		Short: "Show a cookbook version manifest",
		Example: `Show the latest version's file manifest.
cinc cookbook show nginx
Show a specific version.
cinc cookbook show nginx 1.2.0`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := resolveFormat(cmd)
			if err != nil {
				return err
			}
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			version := cinc.LatestVersion
			if len(args) == 2 {
				version = args[1]
			}
			cb, _, err := c.Cookbooks.Get(cmd.Context(), args[0], version)
			if err != nil {
				return err
			}
			return printer.New(cmd.OutOrStdout(), format).Value(cb)
		},
	}
}

// newCookbookUploadCmd builds the `cinc cookbook upload <name>...` command.
func newCookbookUploadCmd() *cobra.Command {
	var cookbookPath string
	cmd := &cobra.Command{
		Use:   "upload <name>...",
		Short: "Upload cookbook versions to the Cinc Server",
		Example: `Upload a cookbook from your cookbook path.
cinc cookbook upload nginx`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := resolveFormat(cmd)
			if err != nil {
				return err
			}
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			results := make([]cookbookUploadResult, 0, len(args))
			for _, name := range args {
				dir, err := localcookbook.Locate(name, cookbookPath)
				if err != nil {
					return err
				}
				// The name and version uploaded are the ones the metadata
				// declares, normalized as Chef does ("1.2" is 1.2.0), and the
				// name is the metadata's, not the directory's.
				cb, err := localcookbook.Load(dir, false)
				if err != nil {
					return err
				}
				if err := c.Cookbooks.Upload(cmd.Context(), cb); err != nil {
					return err
				}
				results = append(results, cookbookUploadResult{
					Cookbook: cb.Name, Version: cb.Version, Uploaded: true,
				})
			}
			if format == printer.FormatJSON {
				return printer.New(cmd.OutOrStdout(), format).Value(results)
			}
			for _, result := range results {
				fmt.Fprintf(cmd.OutOrStdout(), "Uploaded cookbook %q version %s\n", result.Cookbook, result.Version)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&cookbookPath, "cookbook-path", "", "directory or path list containing cookbooks (default current directory)")
	return cmd
}

type cookbookUploadResult struct {
	Cookbook string `json:"cookbook"`
	Version  string `json:"version"`
	Uploaded bool   `json:"uploaded"`
}

// newCookbookDeleteCmd builds the `cinc cookbook delete <name> <version>`
// command. The server identifies a cookbook by name and version, so both
// are required.
func newCookbookDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name> <version>",
		Short: "Delete a cookbook version from the server",
		Example: `Delete a specific cookbook version from the server.
cinc cookbook delete nginx 1.2.0`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			name, version := args[0], args[1]
			if _, err := c.Cookbooks.Delete(cmd.Context(), name, version); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted cookbook %q version %s\n", name, version)
			return nil
		},
	}
}

// newCookbookListCmd builds the `cinc cookbook list` command.
func newCookbookListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List cookbooks on the server",
		Example: `List every cookbook on the server.
cinc cookbook list`,
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
			names, err := listNames(cmd.Context(), c.Cookbooks.List)
			if err != nil {
				return err
			}
			return printer.New(cmd.OutOrStdout(), format).List(names)
		},
	}
}
