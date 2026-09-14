package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/cli/policyfile/resolver"
	"github.com/cinc-project/cinc-cli/cli/policyfile/rubyeval"
	"github.com/cinc-project/cinc-cli/cli/printer"
)

// newPolicyInstallCmd builds `cinc policy install [Policyfile.rb]`. It
// evaluates a Policyfile with the embedded CRuby engine (real Ruby, so any
// dynamic Policyfile works) and writes the evaluation-only portion of a
// Policyfile.lock.json.
func newPolicyInstallCmd() *cobra.Command {
	var outFile string
	cmd := &cobra.Command{
		Use:   "install [Policyfile.rb]",
		Short: "Resolve a Policyfile.rb and write a push-ready lock",
		Long: "Resolve a Policyfile.rb into a push-ready Policyfile.lock.json.\n\n" +
			"cinc runs your Policyfile through an embedded CRuby engine (CRuby\n" +
			"compiled to WebAssembly, run with no system Ruby and no CGo), so any\n" +
			"valid Ruby works: loops, conditionals, helper methods, ENV, string\n" +
			"interpolation, and require_relative of sibling files all behave just\n" +
			"as they do with chef. The first run downloads a pinned ruby.wasm and\n" +
			"caches it; later runs are offline.\n\n" +
			"cinc then resolves your cookbooks: it reads each cookbook's metadata,\n" +
			"solves versions against every `depends` and the constraints in your\n" +
			"Policyfile, and computes the same content identifiers chef does. The\n" +
			"resulting Policyfile.lock.json is byte-for-byte compatible with what\n" +
			"`chef install` writes, so you can `cinc policy push` it straight to a\n" +
			"Cinc/Chef Infra Server.\n\n" +
			"Today path: cookbooks are the fully supported source. git:,\n" +
			"Supermarket, and chef server sources aren't resolved yet.",
		Example: `Resolve ./Policyfile.rb and write ./Policyfile.lock.json.
cinc policy install

Resolve a specific Policyfile and print a summary as JSON.
cinc policy install path/to/Policyfile.rb --format json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := resolveFormat(cmd)
			if err != nil {
				return err
			}

			pfPath := "Policyfile.rb"
			if len(args) == 1 {
				pfPath = args[0]
			}
			if _, err := os.Stat(pfPath); err != nil {
				return fmt.Errorf("we couldn't find a Policyfile at %s. Pass its path, or run `cinc policy create <name>` to scaffold one", pfPath)
			}

			eng := rubyeval.NewEngine()
			eval, raw, err := eng.EvaluateFileWithRaw(cmd.Context(), pfPath)
			if rubyeval.IsMisconfigured(err) {
				return fmt.Errorf("cinc needs the embedded Ruby engine (ruby.wasm) to evaluate a Policyfile, and the one it was pointed at can't be used: %w", err)
			}
			if rubyeval.IsUnavailable(err) {
				return fmt.Errorf("cinc needs the embedded Ruby engine (ruby.wasm) to evaluate a Policyfile, but we couldn't download it: %w.\nOnce you have network access the first run will cache it and later runs work offline", err)
			}
			if err != nil {
				// Evaluation problems (syntax errors, a raise in the file, an
				// empty run_list, …) come back as a normal error carrying the
				// chef-style messages the engine collected.
				return fmt.Errorf("we couldn't evaluate %s:\n%w", pfPath, err)
			}

			result, err := resolver.Resolve(cmd.Context(), eng, eval, raw, filepath.Dir(pfPath))
			if err != nil {
				return fmt.Errorf("we couldn't resolve %s:\n%w", pfPath, err)
			}

			lockPath := outFile
			if lockPath == "" {
				lockPath = filepath.Join(filepath.Dir(pfPath), "Policyfile.lock.json")
			}
			if err := os.WriteFile(lockPath, result.LockJSON, 0o644); err != nil {
				return fmt.Errorf("cinc: write %s: %w", lockPath, err)
			}

			if format == printer.FormatJSON {
				return printer.New(cmd.OutOrStdout(), format).Value(installSummary(eval.Name, lockPath, result))
			}
			renderInstall(cmd, pfPath, lockPath, eval.Name, result)
			return nil
		},
	}
	cmd.Flags().StringVar(&outFile, "output", "", "write the lock to this path instead of ./Policyfile.lock.json")
	return cmd
}

// installSummary is the JSON shape printed for `--format json`: the resolved
// policy name, revision id, lock path, and per-cookbook locks.
func installSummary(name, lockPath string, result *resolver.Result) map[string]any {
	cookbooks := make([]map[string]any, 0, len(result.Cookbooks))
	for _, cb := range result.Cookbooks {
		cookbooks = append(cookbooks, map[string]any{
			"name":       cb.Name,
			"version":    cb.Version,
			"identifier": cb.Identifier,
			"source":     cb.Source,
		})
	}
	return map[string]any{
		"name":        name,
		"revision_id": result.RevisionID,
		"lock":        lockPath,
		"cookbooks":   cookbooks,
	}
}

// renderInstall prints the human summary of a resolved Policyfile: the policy
// name, its revision id, and each cookbook lock it pins, ending with the lock
// path so the next step (`cinc policy push`) is obvious.
func renderInstall(cmd *cobra.Command, pfPath, lockPath, name string, result *resolver.Result) {
	out := cmd.OutOrStdout()
	if name == "" {
		name = "(unnamed)"
	}
	fmt.Fprintf(out, "Resolved %s (policy %q)\n\n", pfPath, name)

	if len(result.Cookbooks) > 0 {
		fmt.Fprintln(out, "  cookbook locks:")
		for _, cb := range result.Cookbooks {
			fmt.Fprintf(out, "    %-20s %-10s %s\n", cb.Name, cb.Version, shortID(cb.Identifier))
		}
		fmt.Fprintln(out)
	}

	fmt.Fprintf(out, "  revision id: %s\n", result.RevisionID)
	fmt.Fprintf(out, "\nWrote %s.\nReady to deploy with `cinc policy push <group>`.\n", lockPath)
}

// shortID abbreviates a content identifier for the human summary.
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
