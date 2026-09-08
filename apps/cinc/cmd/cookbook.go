package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

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
	cmd.AddCommand(newACLCmd("cookbook", "cookbooks"))
	return cmd
}

// newCookbookDownloadCmd builds the `cinc cookbook download <name> [version]`
// command. With no version it resolves the "_latest" sentinel the Chef Server
// exposes for the highest semver. Every file in the version's manifest is
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
			version := "_latest"
			if len(args) == 2 {
				version = args[1]
			}
			// Resolve the concrete version first so "_latest" never leaks into
			// the destination directory name.
			cb, _, err := c.Cookbooks.Get(cmd.Context(), name, version)
			if err != nil {
				return err
			}
			destDir := filepath.Join(dir, name+"-"+cb.Version)
			if err := c.Cookbooks.Download(cmd.Context(), name, cb.Version, destDir); err != nil {
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
// command. With no version the command resolves the special "_latest"
// sentinel that the Chef Server exposes for the highest semver.
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
			version := "_latest"
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
				version, err := localcookbook.ReadVersion(dir)
				if err != nil {
					return err
				}
				cb, err := localcookbook.UploadableFromDir(dir, version)
				if err != nil {
					return err
				}
				if err := c.Cookbooks.Upload(cmd.Context(), cb); err != nil {
					return err
				}
				results = append(results, cookbookUploadResult{
					Cookbook: name, Version: version, Uploaded: true,
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
			names, err := fetchCookbookNames(cmd.Context(), c)
			if err != nil {
				return err
			}
			return printer.New(cmd.OutOrStdout(), format).List(names)
		},
	}
}

// fetchCookbookNames returns the sorted names of every cookbook on the
// server. Cookbooks.List returns version metadata per entry; the list
// verb only enumerates names.
func fetchCookbookNames(ctx context.Context, c *cinc.Client) ([]string, error) {
	index, _, err := c.Cookbooks.List(ctx)
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
