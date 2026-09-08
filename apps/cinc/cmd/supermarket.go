package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/cli/printer"
	"github.com/cinc-project/cinc-cli/cli/supermarket"
	"github.com/cinc-project/cinc-cli/cli/supermarket/explore"
)

// newSupermarketCmd builds the `cinc supermarket` command group.
func newSupermarketCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "supermarket",
		Short: "Manage cookbooks on Chef Supermarket",
	}
	cmd.AddCommand(newSupermarketShareCmd())
	cmd.AddCommand(newSupermarketExploreCmd())
	cmd.AddCommand(newSupermarketDownloadCmd())
	cmd.AddCommand(newSupermarketInstallCmd())
	cmd.AddCommand(newSupermarketListCmd())
	cmd.AddCommand(newSupermarketSearchCmd())
	cmd.AddCommand(newSupermarketShowCmd())
	return cmd
}

// newSupermarketListCmd builds `cinc supermarket list`.
func newSupermarketListCmd() *cobra.Command {
	var (
		site    string
		order   string
		user    string
		limit   int
		verbose bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List cookbooks on Chef Supermarket",
		Example: `List cookbooks available on Chef Supermarket.
cinc supermarket list`,
		Long: "Lists every cookbook on Chef Supermarket. With --verbose the\n" +
			"output also includes the maintainer and latest published version\n" +
			"of each cookbook (one extra request to /universe, fast).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := resolveFormat(cmd)
			if err != nil {
				return err
			}
			client, err := supermarket.NewAnonymous(resolveSupermarketSite(cmd, site))
			if err != nil {
				return err
			}
			result, err := client.List(cmd.Context(), supermarket.ListOptions{
				Order: order, User: user, Limit: limit, Verbose: verbose,
			})
			if err != nil {
				return err
			}
			if format == printer.FormatJSON {
				return printer.New(cmd.OutOrStdout(), format).Value(result)
			}
			return printSupermarketEntries(cmd.OutOrStdout(), result.Entries, verbose)
		},
	}
	cmd.Flags().StringVar(&site, "supermarket-site", "", "URL of the Chef Supermarket site (default: profile supermarket_site, then https://supermarket.chef.io)")
	cmd.Flags().StringVar(&order, "order", "", "sort order: recently_updated, recently_added, most_downloaded, most_followed")
	cmd.Flags().StringVar(&user, "user", "", "only show cookbooks owned by this Supermarket username")
	cmd.Flags().IntVar(&limit, "limit", 0, "cap the number of entries returned (default: all)")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "include maintainer and latest version per cookbook")
	return cmd
}

// newSupermarketSearchCmd builds `cinc supermarket search`.
func newSupermarketSearchCmd() *cobra.Command {
	var (
		site    string
		limit   int
		verbose bool
	)
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search cookbooks on Chef Supermarket",
		Example: `Search Supermarket for cookbooks.
cinc supermarket search nginx`,
		Long: "Fuzzy-searches cookbook name, description, and maintainer on\n" +
			"Chef Supermarket. With --verbose the output also includes the\n" +
			"maintainer and latest published version of each hit.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := resolveFormat(cmd)
			if err != nil {
				return err
			}
			client, err := supermarket.NewAnonymous(resolveSupermarketSite(cmd, site))
			if err != nil {
				return err
			}
			result, err := client.Search(cmd.Context(), supermarket.SearchOptions{
				Query: args[0], Limit: limit, Verbose: verbose,
			})
			if err != nil {
				return err
			}
			if format == printer.FormatJSON {
				return printer.New(cmd.OutOrStdout(), format).Value(result)
			}
			return printSupermarketEntries(cmd.OutOrStdout(), result.Entries, verbose)
		},
	}
	cmd.Flags().StringVar(&site, "supermarket-site", "", "URL of the Chef Supermarket site (default: profile supermarket_site, then https://supermarket.chef.io)")
	cmd.Flags().IntVar(&limit, "limit", 0, "cap the number of entries returned (default: all matches)")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "include maintainer and latest version per cookbook")
	return cmd
}

// newSupermarketShowCmd builds `cinc supermarket show`.
func newSupermarketShowCmd() *cobra.Command {
	var site string
	cmd := &cobra.Command{
		Use:   "show <cookbook> [version]",
		Short: "Show a cookbook (or one of its versions) on Chef Supermarket",
		Example: `Show a Supermarket cookbook.
cinc supermarket show nginx
Show a specific version.
cinc supermarket show nginx 1.2.0`,
		Long: "Without a version argument, shows the cookbook record: maintainer,\n" +
			"description, latest version, total downloads, and the versions\n" +
			"published. With a version argument, shows that version's license,\n" +
			"tarball size, dependencies, and supported platforms.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := resolveFormat(cmd)
			if err != nil {
				return err
			}
			client, err := supermarket.NewAnonymous(resolveSupermarketSite(cmd, site))
			if err != nil {
				return err
			}
			opts := supermarket.ShowOptions{Cookbook: args[0]}
			if len(args) == 2 {
				opts.Version = args[1]
			}
			result, err := client.Show(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if format == printer.FormatJSON {
				return printer.New(cmd.OutOrStdout(), format).Value(result)
			}
			return printSupermarketShow(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&site, "supermarket-site", "", "URL of the Chef Supermarket site (default: profile supermarket_site, then https://supermarket.chef.io)")
	return cmd
}

// printSupermarketEntries renders list/search results in human mode.
// Without verbose: one cookbook name per line. With verbose: an
// aligned NAME / MAINTAINER / LATEST table.
func printSupermarketEntries(w io.Writer, entries []supermarket.ListEntry, verbose bool) error {
	if !verbose {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name)
		}
		return printer.New(w, printer.FormatHuman).List(names)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "NAME\tMAINTAINER\tLATEST"); err != nil {
		return err
	}
	for _, e := range entries {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\n", e.Name, e.Maintainer, e.LatestVersion); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// printSupermarketShow renders show results in human mode. Cookbook
// vs version is determined by which field of ShowResult is populated.
func printSupermarketShow(w io.Writer, r supermarket.ShowResult) error {
	if r.Version != nil {
		return printSupermarketVersion(w, r.Version)
	}
	return printSupermarketCookbook(w, r.Cookbook)
}

func printSupermarketCookbook(w io.Writer, cb *supermarket.Cookbook) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	row := func(k, v string) {
		fmt.Fprintf(tw, "%s\t%s\n", k+":", v)
	}
	row("Name", cb.Name)
	row("Maintainer", cb.Maintainer)
	if cb.Description != "" {
		row("Description", cb.Description)
	}
	row("Category", cb.Category)
	row("Latest version", supermarket.VersionFromURL(cb.LatestVersion))
	row("Updated", cb.UpdatedAt.Format("2006-01-02"))
	row("Downloads", formatThousands(cb.Metrics.Downloads.Total))
	if cb.ExternalURL != "" {
		row("Source URL", cb.ExternalURL)
	}
	versionList := supermarket.VersionListFromURLs(cb.Versions)
	row("Versions", strings.Join(firstN(versionList, 5), ", "))
	if extra := len(versionList) - 5; extra > 0 {
		fmt.Fprintf(tw, "\t... (%d more)\n", extra)
	}
	return tw.Flush()
}

func printSupermarketVersion(w io.Writer, v *supermarket.CookbookVersion) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	row := func(k, val string) {
		fmt.Fprintf(tw, "%s\t%s\n", k+":", val)
	}
	row("Version", v.Version)
	if v.License != "" {
		row("License", v.License)
	}
	if v.TarballSize > 0 {
		row("Tarball size", formatBytes(v.TarballSize))
	}
	if len(v.Dependencies) > 0 {
		row("Dependencies", "")
		for name, constraint := range v.Dependencies {
			fmt.Fprintf(tw, "  %s\t%s\n", name, constraint)
		}
	}
	if len(v.Supports) > 0 {
		row("Platforms", "")
		for name, constraint := range v.Supports {
			fmt.Fprintf(tw, "  %s\t%s\n", name, constraint)
		}
	}
	return tw.Flush()
}

func firstN(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// formatThousands renders an integer with comma thousands separators.
func formatThousands(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	// Walk from the right, inserting a comma every 3 digits.
	var b strings.Builder
	first := len(s) % 3
	if first == 0 {
		first = 3
	}
	b.WriteString(s[:first])
	for i := first; i < len(s); i += 3 {
		b.WriteByte(',')
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// formatBytes renders a byte count as a short human-friendly string.
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for k := n / unit; k >= unit; k /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// newSupermarketDownloadCmd builds `cinc supermarket download`.
// Like `explore`, this hits only anonymous endpoints, so no key is loaded
// and a missing credentials file is fine. An existing profile is still read
// for supermarket_site so downloads come from the configured Supermarket.
func newSupermarketDownloadCmd() *cobra.Command {
	var (
		file  string
		force bool
		site  string
	)
	cmd := &cobra.Command{
		Use:   "download <cookbook> [version]",
		Short: "Download a cookbook tarball from Chef Supermarket",
		Example: `Download a cookbook tarball from Supermarket.
cinc supermarket download nginx`,
		Long: "Downloads a cookbook from Chef Supermarket and writes it to disk\n" +
			"as a gzipped tarball. The version defaults to the latest published\n" +
			"version. By default the tarball lands at ./<cookbook>-<version>.tar.gz;\n" +
			"pass --file to choose a target file or directory.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := resolveFormat(cmd)
			if err != nil {
				return err
			}
			opts := supermarket.DownloadOptions{
				Cookbook: args[0],
				File:     file,
				Force:    force,
			}
			if len(args) == 2 {
				opts.Version = args[1]
			}
			client, err := supermarket.NewAnonymous(resolveSupermarketSite(cmd, site))
			if err != nil {
				return err
			}
			result, err := client.Download(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if format == printer.FormatJSON {
				return printer.New(cmd.OutOrStdout(), format).Value(result)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Downloaded %s %s to %s\n", result.Cookbook, result.Version, result.File)
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "output file or directory (default: ./<cookbook>-<version>.tar.gz)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite the output file if it already exists")
	cmd.Flags().StringVar(&site, "supermarket-site", "", "URL of the Chef Supermarket site (default: profile supermarket_site, then https://supermarket.chef.io)")
	return cmd
}

// newSupermarketInstallCmd builds `cinc supermarket install`. Unlike the
// other supermarket commands, install touches both worlds: it downloads
// the cookbook anonymously from Supermarket and then uploads it to the
// configured Cinc Server, so it resolves an authenticated client.
//
// There is no acceptance test for this command: cinc-zero simulates a
// Chef Infra Server but does not serve the Supermarket API, so the
// download half can't be exercised end-to-end. Coverage lives in the
// unit test, which fakes both halves (the same gap that already excludes
// every other `cinc supermarket` command).
func newSupermarketInstallCmd() *cobra.Command {
	var site string
	cmd := &cobra.Command{
		Use:   "install <cookbook> [version]",
		Short: "Install a Supermarket cookbook onto the Cinc Server",
		Example: `Install the latest version of a cookbook from Supermarket onto the server.
cinc supermarket install nginx
Install a specific version.
cinc supermarket install nginx 1.2.0`,
		Long: "Downloads a cookbook from Chef Supermarket and uploads it to your\n" +
			"configured Cinc Server in one step. The version defaults to the\n" +
			"latest published version. Only the named cookbook is installed;\n" +
			"its dependencies are not resolved.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := resolveFormat(cmd)
			if err != nil {
				return err
			}
			server, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			client, err := supermarket.NewAnonymous(resolveSupermarketSite(cmd, site))
			if err != nil {
				return err
			}
			opts := supermarket.InstallOptions{Cookbook: args[0]}
			if len(args) == 2 {
				opts.Version = args[1]
			}
			result, err := client.Install(cmd.Context(), server, opts)
			if err != nil {
				return err
			}
			if format == printer.FormatJSON {
				return printer.New(cmd.OutOrStdout(), format).Value(result)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Installed cookbook %q version %s into the server\n", result.Cookbook, result.Version)
			return nil
		},
	}
	cmd.Flags().StringVar(&site, "supermarket-site", "", "URL of the Chef Supermarket site (default: profile supermarket_site, then https://supermarket.chef.io)")
	return cmd
}

// newSupermarketExploreCmd builds the `cinc supermarket explore` TUI.
// It needs no credentials — every endpoint it touches is anonymous — so it
// never runs the first-run flow. An existing profile is still consulted for
// supermarket_site, so `explore` browses the same Supermarket the rest of
// the commands use, but a missing credentials file is not an error.
func newSupermarketExploreCmd() *cobra.Command {
	var site string
	cmd := &cobra.Command{
		Use:   "explore",
		Short: "Browse Chef Supermarket cookbooks in a terminal UI",
		Example: `Browse Supermarket cookbooks in an interactive terminal UI.
cinc supermarket explore`,
		Long: "Launches an interactive terminal UI for browsing cookbooks on Chef\n" +
			"Supermarket. Move with arrow keys, press / to search, press d/u/a to\n" +
			"sort by Downloads, Updated, or Alphabetical, enter for full details,\n" +
			"i to install the highlighted cookbook onto your Cinc Server, and q to\n" +
			"quit.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved := resolveSupermarketSite(cmd, site)
			return explore.Run(cmd.Context(), explore.Options{
				Site:    resolved,
				Stdin:   cmd.InOrStdin(),
				Stdout:  cmd.OutOrStdout(),
				Stderr:  cmd.ErrOrStderr(),
				Install: supermarketInstaller(cmd, resolved),
			})
		},
	}
	cmd.Flags().StringVar(&site, "supermarket-site", "", "URL of the Chef Supermarket site (default: profile supermarket_site, then https://supermarket.chef.io)")
	return cmd
}

// supermarketInstaller returns the closure the explore TUI calls when the
// user installs a cookbook. Credentials are resolved lazily — only when
// the closure runs — so launching `cinc supermarket explore` stays
// credential-free. site is already resolved by the caller. Any credential
// or upload failure flows back to the TUI footer.
func supermarketInstaller(cmd *cobra.Command, site string) func(context.Context, string, string) error {
	return func(ctx context.Context, name, version string) error {
		server, err := resolveClient(cmd)
		if err != nil {
			return err
		}
		client, err := supermarket.NewAnonymous(site)
		if err != nil {
			return err
		}
		_, err = client.Install(ctx, server, supermarket.InstallOptions{Cookbook: name, Version: version})
		return err
	}
}

// newSupermarketShareCmd builds the `cinc supermarket share <cookbook>` command.
func newSupermarketShareCmd() *cobra.Command {
	var (
		cookbookPath string
		site         string
		dryRun       bool
		noChefignore bool
	)
	cmd := &cobra.Command{
		Use:   "share <cookbook> [category]",
		Short: "Share a cookbook on Chef Supermarket",
		Example: `Share a local cookbook to Supermarket (requires credentials).
cinc supermarket share nginx 'Web Servers'`,
		Long: "Packages a local cookbook and uploads it to Chef Supermarket.\n" +
			"Uploads are signed with the profile's identity. By default that's\n" +
			"client_name/client_key, the same identity used against your Cinc\n" +
			"Server. To publish to the public Supermarket under a different\n" +
			"identity, set supermarket_client_name and/or supermarket_key in the\n" +
			"profile; each falls back independently to client_name/client_key\n" +
			"when unset, so you can override just the username, just the key, or\n" +
			"both.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := resolveFormat(cmd)
			if err != nil {
				return err
			}
			opts := supermarket.ShareOptions{
				Cookbook:       args[0],
				CookbookPath:   cookbookPath,
				DryRun:         dryRun,
				SkipChefignore: noChefignore,
			}
			if len(args) == 2 {
				opts.Category = args[1]
			}
			var result supermarket.ShareResult
			if dryRun {
				result, err = supermarket.DryRun(opts)
			} else {
				var profileErr error
				profile, profileErr := resolveSupermarketProfile(cmd)
				if profileErr != nil {
					return profileErr
				}
				client, clientErr := supermarket.New(profile, site)
				if clientErr != nil {
					return clientErr
				}
				result, err = client.Share(cmd.Context(), opts)
			}
			if err != nil {
				return err
			}
			if format == printer.FormatJSON {
				return printer.New(cmd.OutOrStdout(), format).Value(result)
			}
			renderShareResult(cmd.OutOrStdout(), result, dryRun)
			return nil
		},
	}
	cmd.Flags().StringVar(&cookbookPath, "cookbook-path", "", "directory or path list containing cookbooks (default current directory)")
	cmd.Flags().StringVar(&site, "supermarket-site", "", "URL of the Chef Supermarket site (default: profile supermarket_site, then https://supermarket.chef.io)")
	// Uploads sign with supermarket_client_name/supermarket_key when set,
	// falling back per-field to client_name/client_key.
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "build the cookbook tarball without uploading it")
	cmd.Flags().BoolVar(&noChefignore, "no-chefignore", false, "do not exclude files matched by the cookbook's chefignore file")
	return cmd
}

// renderShareResult writes the friendly, checkmarked summary of a cookbook
// share to w. A dry run reports the tarball it built and lists its contents;
// a real upload confirms the Supermarket the cookbook landed on and links to
// its published version page. The ✓ glyphs are drawn green when w is a
// terminal (see useColor), and plain otherwise so pipes and tests stay clean.
func renderShareResult(w io.Writer, result supermarket.ShareResult, dryRun bool) {
	check := colorize(useColor(w), ansiBoldGreen, "✓")

	// The cookbook always carries a version from its metadata; fall back to the
	// bare name only if it's somehow missing so the header never reads oddly.
	name := result.Cookbook
	if result.Version != "" {
		name = fmt.Sprintf("%s %s", result.Cookbook, result.Version)
	}

	if dryRun {
		fmt.Fprintf(w, "Packaging cookbook %s (dry run — not uploading)\n\n", name)
		fmt.Fprintf(w, "  %s Built %s (%s)\n\n", check, result.Tarball, humanizeSize(result.TarballSize))
		fmt.Fprintln(w, "Contents:")
		for _, file := range result.Files {
			fmt.Fprintf(w, "  %s\n", file)
		}
		return
	}

	fmt.Fprintf(w, "Releasing cookbook %s\n\n", name)
	fmt.Fprintf(w, "  %s Built %s (%s)\n", check, result.Tarball, humanizeSize(result.TarballSize))
	fmt.Fprintf(w, "  %s Uploaded to %s\n\n", check, result.Site)
	fmt.Fprintf(w, "%s is now live: %s\n", name, result.URL)
}

// humanizeSize renders a byte count as a compact, human-friendly size such as
// "734 B" or "4.2 KB" for use in the share summary.
func humanizeSize(n int) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := int64(n) / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
