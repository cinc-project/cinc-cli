package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	cinc "github.com/cinc-project/cinc-api"
	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/cli/printer"
)

// newClientCmd builds the `cinc client` command group.
func newClientCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "client",
		Short: "Manage API clients on the Cinc Server",
	}
	cmd.AddCommand(newClientListCmd())
	cmd.AddCommand(newClientShowCmd())
	cmd.AddCommand(newClientCreateCmd())
	cmd.AddCommand(newClientEditCmd())
	cmd.AddCommand(newClientDeleteCmd())
	cmd.AddCommand(newClientReregisterCmd())
	cmd.AddCommand(newKeyCmd(clientKeyOwner))
	cmd.AddCommand(newACLCmd("client", cinc.ACLClients))
	return cmd
}

// newClientReregisterCmd builds `cinc client reregister <name>`. It
// regenerates the client's "default" key, invalidating the old private key
// and emitting the new one (to stdout, or to --key-file). The cinc-api
// Clients.Reregister method owns the delete-then-recreate workflow and its
// non-atomic recovery guidance.
func newClientReregisterCmd() *cobra.Command {
	var keyFile string
	cmd := &cobra.Command{
		Use:   "reregister <name>",
		Short: "Regenerate a client's default key, invalidating the old one",
		Example: `Regenerate a client's key and write the new private key to disk.
cinc client reregister worker-01 --key-file worker-01.pem`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			name := args[0]
			key, _, err := c.Clients.Reregister(cmd.Context(), name)
			if err != nil {
				return err
			}
			if key.PrivateKey == "" {
				return fmt.Errorf("cinc: server returned no private key when reregistering %q", name)
			}
			return emitPrivateKey(cmd, fmt.Sprintf("Reregistered client %q", name), "key", key.PrivateKey, keyFile)
		},
	}
	cmd.Flags().StringVarP(&keyFile, "key-file", "f", "", "write the new private key to this file instead of stdout")
	return cmd
}

// newClientShowCmd builds the `cinc client show <name>` command.
func newClientShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show an API client",
		Example: `Show an API client, including whether it is a validator.
cinc client show worker-01`,
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
			client, _, err := c.Clients.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return printer.New(cmd.OutOrStdout(), format).Value(client)
		},
	}
}

// newClientEditCmd builds the `cinc client edit <name>` command. It
// fetches the named client, presents the editable fields in a small
// form (see editor.go), and PUTs the result back to the server.
// With `--file` the JSON is read from a file unmodified, which makes
// the command scriptable and keeps the unit and integration tests off the
// TUI codepath. The form short-circuits with "unchanged" when the
// user submits without modifying any field.
func newClientEditCmd() *cobra.Command {
	var inputFile string
	cmd := &cobra.Command{
		Use:   "edit <name>",
		Short: "Edit an API client on the server",
		Example: `Open an API client's JSON in your editor and save it back.
cinc client edit worker-01`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			name := args[0]

			var updated cinc.APIClient
			if inputFile != "" {
				data, err := os.ReadFile(inputFile)
				if err != nil {
					return fmt.Errorf("cinc: read %s: %w", inputFile, err)
				}
				if err := json.Unmarshal(data, &updated); err != nil {
					return fmt.Errorf("cinc: parse %s: %w", inputFile, err)
				}
			} else {
				current, _, err := c.Clients.Get(cmd.Context(), name)
				if err != nil {
					return err
				}
				edited, err := editClient(current)
				if err != nil {
					return err
				}
				if unchanged(*current, *edited) {
					fmt.Fprintf(cmd.OutOrStdout(), "Client %q unchanged\n", name)
					return nil
				}
				updated = *edited
			}
			updated.Name = name

			if _, _, err := c.Clients.Update(cmd.Context(), &updated); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Updated client %q\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&inputFile, "file", "", "read the updated client JSON from this file instead of launching the form")
	return cmd
}

// newClientCreateCmd builds the `cinc client create <name>` command.
//
// The server generates an RSA key pair and returns the private key in
// the response. By default the private key is streamed to stdout so it
// can be piped into a file; `--key-file` writes it to disk instead.
// `--public-key` lets the caller supply their own public key, in which
// case the server does not generate a key pair and no private key is
// returned.
func newClientCreateCmd() *cobra.Command {
	var (
		validator     bool
		keyFile       string
		publicKeyFile string
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an API client on the server",
		Example: `Create a client; the server generates the key, written to a file.
cinc client create worker-01 --key-file worker-01.pem

Create a validator client, used to bootstrap new nodes.
cinc client create bootstrap-validator --validator --key-file validator.pem

Register a public key you already have; the server generates no key.
cinc client create worker-01 --public-key worker-01.pub`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			req := &cinc.APIClient{Name: args[0], Validator: validator}
			if publicKeyFile != "" {
				pem, err := os.ReadFile(publicKeyFile)
				if err != nil {
					return fmt.Errorf("cinc: read public key: %w", err)
				}
				req.PublicKey = string(pem)
			}
			created, _, err := c.Clients.Create(cmd.Context(), req)
			if err != nil {
				return err
			}
			return emitPrivateKey(cmd, fmt.Sprintf("Created client %q", req.Name), "key", created.ChefKey.PrivateKey, keyFile)
		},
	}
	cmd.Flags().BoolVar(&validator, "validator", false, "create a validator client")
	cmd.Flags().StringVarP(&keyFile, "key-file", "f", "", "write the generated private key to this file instead of stdout")
	cmd.Flags().StringVar(&publicKeyFile, "public-key", "", "path to a PEM public key; the server will not generate a key pair")
	return cmd
}

// newClientDeleteCmd builds the `cinc client delete <name>` command.
func newClientDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete an API client from the server",
		Example: `Delete an API client from the server.
cinc client delete worker-01`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			name := args[0]
			if _, err := c.Clients.Delete(cmd.Context(), name); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted client %q\n", name)
			return nil
		},
	}
}

// newClientListCmd builds the `cinc client list` command.
func newClientListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List API clients on the server",
		Example: `List every API client on the server.
cinc client list`,
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
			names, err := listNames(cmd.Context(), c.Clients.List)
			if err != nil {
				return err
			}
			return printer.New(cmd.OutOrStdout(), format).List(names)
		},
	}
}
