package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	cliclient "github.com/cinc-project/cinc-cli/cli/client"
	"github.com/cinc-project/cinc-cli/cli/config"
	"github.com/cinc-project/cinc-cli/cli/printer"
	"github.com/cinc-project/cinc-cli/cli/progname"
)

// newConfigCmd builds the `cinc config` command group.
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage local Cinc configuration",
	}
	cmd.AddCommand(newConfigCreateCmd())
	cmd.AddCommand(newConfigValidateCmd())
	return cmd
}

func newConfigValidateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate [path]",
		Short: "Validate local Cinc TOML configuration and endpoint reachability",
		Example: `Run the pre-flight checks for every profile in your credentials file.
cinc config validate

Check just one profile.
cinc config validate --profile staging`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := configValidatePath(cmd, args)
			if err != nil {
				return err
			}
			format, err := resolveFormat(cmd)
			if err != nil {
				return err
			}

			var result configValidationResult
			if cfg, loadErr := config.Load(path); loadErr != nil {
				// The file did not parse; report it as the single top-level
				// check failing — there are no profiles to check.
				result = configValidationResult{
					Path:  path,
					Valid: false,
					TopLevel: []checkResult{
						{Name: "Credentials file is valid TOML", Passed: false, Detail: loadErr.Error()},
					},
				}
			} else {
				if cfg, err = profileToValidate(cmd, path, cfg); err != nil {
					return err
				}
				// The reachable check connects through cliclient.New, which warns
				// when a profile disables TLS verification. Validate reports TLS
				// posture as its own check, so silence the redundant warning here.
				defer cliclient.SilenceTLSWarning()()
				result = runConfigChecks(cmd.Context(), path, cfg)
			}

			if format == printer.FormatJSON {
				if err := printer.New(cmd.OutOrStdout(), format).Value(result); err != nil {
					return err
				}
			} else {
				renderConfigChecks(cmd.OutOrStdout(), result)
			}

			if !result.Valid {
				// The checks were already printed; wrap the sentinel so Execute
				// does not re-print a generic "Error: ..." line, while still
				// exiting non-zero.
				return fmt.Errorf("config validation failed: %w", errAlreadyReported)
			}
			return nil
		},
	}
	return cmd
}

// profileToValidate narrows cfg to the profile --profile names, so
// `config validate --profile staging` checks only staging. Without the flag
// every profile is checked; CINC_PROFILE and CHEF_PROFILE do not narrow it,
// since users often set them for every command and still expect validate
// to vet the whole file.
func profileToValidate(cmd *cobra.Command, path string, cfg *config.Config) (*config.Config, error) {
	name, _ := cmd.Flags().GetString("profile")
	if name == "" {
		return cfg, nil
	}
	p, ok := cfg.Profiles[name]
	if !ok {
		return nil, fmt.Errorf("there's no profile %q in %s. Run `%s config validate` without --profile to check every profile it has", name, path, progname.Get())
	}
	return &config.Config{Profiles: map[string]config.Profile{name: p}}, nil
}

func configValidatePath(cmd *cobra.Command, args []string) (string, error) {
	if len(args) > 0 {
		return config.ExpandHome(args[0])
	}
	cfgPath, err := configPathForCommand(cmd)
	if err != nil {
		return "", err
	}
	return config.ExpandHome(cfgPath)
}
