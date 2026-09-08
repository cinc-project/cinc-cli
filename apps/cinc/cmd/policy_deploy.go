package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	cinc "github.com/cinc-project/cinc-api"
	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/cli/policyfile"
	"github.com/cinc-project/cinc-cli/cli/printer"
)

const defaultLockFile = "Policyfile.lock.json"

// newPolicyPushCmd builds `cinc policy push <group> [lock]`. It deploys an
// existing Policyfile.lock.json: it fetches every cookbook the lock pins (from
// the source the lock records, into the cinc cache), uploads each as a cookbook
// artifact, and associates the revision with the named policy group.
func newPolicyPushCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push <group> [lock]",
		Short: "Deploy a Policyfile lock to a policy group",
		Example: `Deploy ./Policyfile.lock.json to a policy group, uploading its cookbooks.
cinc policy push prod
Deploy a specific lock file.
cinc policy push prod Policyfile.lock.json`,
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
			group := args[0]
			lockPath := defaultLockFile
			if len(args) == 2 {
				lockPath = args[1]
			}

			lock, lockJSON, err := cinc.LoadPolicyfileLock(lockPath)
			if err != nil {
				return err
			}
			fetcher, err := newFetcher(filepath.Dir(lockPath), c)
			if err != nil {
				return err
			}
			cookbooks, err := fetchLockCookbooks(cmd.Context(), fetcher, lock)
			if err != nil {
				return err
			}
			rev, _, err := c.Policies.PushRevision(cmd.Context(), lockJSON, group, cookbooks)
			if err != nil {
				return err
			}
			return emitPushResult(cmd, format, lock.Name, group, rev, len(cookbooks))
		},
	}
	return cmd
}

// newPolicyExportCmd builds `cinc policy export [lock] [dir]`. It assembles a
// standalone bundle (cookbooks + lock + a local-mode client config) for an
// air-gapped `cinc-client -z`. A server connection is only needed when the lock
// has chef_server-sourced cookbooks.
func newPolicyExportCmd() *cobra.Command {
	var archive bool
	cmd := &cobra.Command{
		Use:   "export [lock] [dir]",
		Short: "Assemble a standalone bundle from a Policyfile lock",
		Example: `Assemble a standalone bundle for an air-gapped cinc-client -z run.
cinc policy export Policyfile.lock.json ./bundle --archive`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := resolveFormat(cmd)
			if err != nil {
				return err
			}
			lockPath := defaultLockFile
			if len(args) >= 1 {
				lockPath = args[0]
			}
			lock, lockJSON, err := cinc.LoadPolicyfileLock(lockPath)
			if err != nil {
				return err
			}
			destDir := lock.Name
			if len(args) == 2 {
				destDir = args[1]
			}

			// Export only needs the server for chef_server cookbook sources, so
			// a missing/unusable config is tolerated here (nil Chef client).
			chef, _ := resolveClient(cmd)
			fetcher, err := newFetcher(filepath.Dir(lockPath), chef)
			if err != nil {
				return err
			}
			result, err := policyfile.Export(cmd.Context(), fetcher, lock, lockJSON, destDir, archive)
			if err != nil {
				return err
			}
			if format == printer.FormatJSON {
				return printer.New(cmd.OutOrStdout(), format).Value(result)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Exported policy %q to %s\n", result.Policy, result.Dir)
			if result.Archive != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Wrote archive %s\n", result.Archive)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&archive, "archive", "a", false, "also write the bundle as a .tar.gz archive")
	return cmd
}

// newPolicyPushArchiveCmd builds `cinc policy push-archive <group> [archive]`.
// It deploys a bundle a previous `policy export` produced: it loads the bundle's
// Policyfile.lock.json and the cookbooks under its cookbook tree, then uploads
// each as a cookbook artifact and associates the revision with the named group.
// This is the inverse of `policy export` and the offline-friendly sibling of
// `policy push` (which fetches cookbooks from their sources rather than a bundle).
func newPolicyPushArchiveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push-archive <group> [archive]",
		Short: "Deploy an exported Policyfile bundle to a policy group",
		Long: "Deploy a bundle produced by `cinc policy export` to a policy group.\n\n" +
			"The archive may be a .tar.gz (as written by `cinc policy export --archive`)\n" +
			"or an already-extracted bundle directory. When you don't name one, cinc\n" +
			"looks in the current directory for an extracted bundle (a\n" +
			"Policyfile.lock.json beside you) and then for a single .tar.gz archive.",
		Example: `Deploy a previously exported bundle archive to a policy group.
cinc policy push-archive prod appserver.tar.gz
Deploy an extracted bundle directory.
cinc policy push-archive prod ./appserver`,
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
			group := args[0]
			archiveArg := ""
			if len(args) == 2 {
				archiveArg = args[1]
			}
			archivePath, err := resolvePushArchivePath(archiveArg)
			if err != nil {
				return err
			}

			dir, cleanup, err := policyfile.OpenBundle(archivePath)
			if err != nil {
				return err
			}
			defer cleanup()

			lock, lockJSON, err := cinc.LoadPolicyfileLock(filepath.Join(dir, policyfile.BundleLockName))
			if err != nil {
				return err
			}
			cookbooks, err := policyfile.LoadBundleCookbooks(dir, lock)
			if err != nil {
				return err
			}
			rev, _, err := c.Policies.PushRevision(cmd.Context(), lockJSON, group, cookbooks)
			if err != nil {
				return err
			}
			return emitPushResult(cmd, format, lock.Name, group, rev, len(cookbooks))
		},
	}
	return cmd
}

// resolvePushArchivePath decides which bundle to deploy. When the user named one
// we just confirm it exists; otherwise we look in the current directory for an
// extracted bundle (a Policyfile.lock.json) and then for a single .tar.gz
// archive, erroring conversationally when there's nothing — or too much — to pick.
func resolvePushArchivePath(arg string) (string, error) {
	if arg != "" {
		if _, err := os.Stat(arg); err != nil {
			return "", fmt.Errorf("we couldn't find the bundle %q to push. Pass a .tar.gz archive or an extracted bundle directory, or run `cinc policy export --archive` to create one", arg)
		}
		return arg, nil
	}
	if _, err := os.Stat(defaultLockFile); err == nil {
		return ".", nil
	}
	matches, _ := filepath.Glob("*.tar.gz")
	slices.Sort(matches)
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("we couldn't find a bundle to push here. Pass the .tar.gz archive (or extracted bundle directory) to deploy, e.g. `cinc policy push-archive GROUP appserver.tar.gz`")
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("we found several .tar.gz archives here (%s) and aren't sure which to push. Name the one you mean, e.g. `cinc policy push-archive GROUP %s`", strings.Join(matches, ", "), matches[0])
	}
}

// newFetcher builds a policyfile.Fetcher rooted at the default cinc cookbook
// cache, resolving path sources relative to lockDir and using chef for
// chef_server sources.
func newFetcher(lockDir string, chef *cinc.Client) (*policyfile.Fetcher, error) {
	cacheRoot, err := policyfile.DefaultCacheRoot()
	if err != nil {
		return nil, err
	}
	return &policyfile.Fetcher{CacheRoot: cacheRoot, LockDir: lockDir, Chef: chef}, nil
}

// fetchLockCookbooks resolves every cookbook the lock pins to an uploadable
// LocalCookbook, fetching+caching as needed.
func fetchLockCookbooks(ctx context.Context, fetcher *policyfile.Fetcher, lock *cinc.PolicyRevision) (map[string]*cinc.LocalCookbook, error) {
	cookbooks := make(map[string]*cinc.LocalCookbook, len(lock.CookbookLocks))
	for name, cl := range lock.CookbookLocks {
		dir, err := fetcher.EnsureCookbook(ctx, name, cl)
		if err != nil {
			return nil, err
		}
		cb, err := cinc.LocalCookbookFromDir(dir, cl.Version)
		if err != nil {
			return nil, fmt.Errorf("cinc: load cookbook %q from %s: %w", name, dir, err)
		}
		cookbooks[name] = cb
	}
	return cookbooks, nil
}

func emitPushResult(cmd *cobra.Command, format printer.Format, policy, group string, rev *cinc.PolicyRevision, uploaded int) error {
	if format == printer.FormatJSON {
		return printer.New(cmd.OutOrStdout(), format).Value(map[string]any{
			"policy":             policy,
			"group":              group,
			"revision_id":        rev.RevisionID,
			"cookbooks_uploaded": uploaded,
		})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Pushed policy %q (revision %s) to group %q with %d cookbook(s)\n",
		policy, rev.RevisionID, group, uploaded)
	return nil
}
