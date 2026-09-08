package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	cinc "github.com/tas50/cinc-api"

	"github.com/cinc-project/cinc-cli/cli/printer"
)

// keyOwner describes one of the two collections of keys the Cinc Server
// exposes — a client's or a user's. The two `key` sub-nouns are identical
// except for which scope they target, so newKeyCmd builds the verbs once and
// each noun supplies its own owner descriptor.
type keyOwner struct {
	noun   string // "client" or "user", used in help text and messages
	sample string // a sample owner name used in generated examples
	scope  func(*cinc.Client, string) *cinc.KeyScope
}

var clientKeyOwner = keyOwner{
	noun:   "client",
	sample: "worker-01",
	scope:  func(c *cinc.Client, name string) *cinc.KeyScope { return c.Keys.Client(name) },
}

var userKeyOwner = keyOwner{
	noun:   "user",
	sample: "alice",
	scope:  func(c *cinc.Client, name string) *cinc.KeyScope { return c.Keys.User(name) },
}

// newKeyCmd builds the `key` sub-group for the given owner, e.g.
// `cinc client key …` or `cinc user key …`.
func newKeyCmd(owner keyOwner) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "key",
		Short: "Manage a " + owner.noun + "'s public keys",
	}
	cmd.AddCommand(newKeyListCmd(owner))
	cmd.AddCommand(newKeyShowCmd(owner))
	cmd.AddCommand(newKeyCreateCmd(owner))
	cmd.AddCommand(newKeyEditCmd(owner))
	cmd.AddCommand(newKeyDeleteCmd(owner))
	return cmd
}

// newKeyListCmd builds `cinc <owner> key list <name>`, listing the owner's
// key names. Like the other `list` verbs it renders names, not full key
// material; use `key show` for a single key's details.
func newKeyListCmd(owner keyOwner) *cobra.Command {
	return &cobra.Command{
		Use:   "list <" + owner.noun + ">",
		Short: "List a " + owner.noun + "'s keys",
		Example: fmt.Sprintf(`List a %[1]s's keys.
cinc %[1]s key list %[2]s`, owner.noun, owner.sample),
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
			keys, _, err := owner.scope(c, args[0]).List(cmd.Context())
			if err != nil {
				return err
			}
			names := make([]string, 0, len(keys))
			for _, k := range keys {
				names = append(names, k.Name)
			}
			slices.Sort(names)
			return printer.New(cmd.OutOrStdout(), format).List(names)
		},
	}
}

// newKeyShowCmd builds `cinc <owner> key show <name> <key-name>`.
func newKeyShowCmd(owner keyOwner) *cobra.Command {
	return &cobra.Command{
		Use:   "show <" + owner.noun + "> <key-name>",
		Short: "Show one of a " + owner.noun + "'s keys",
		Example: fmt.Sprintf(`Show one of a %[1]s's keys.
cinc %[1]s key show %[2]s default`, owner.noun, owner.sample),
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := resolveFormat(cmd)
			if err != nil {
				return err
			}
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			key, _, err := owner.scope(c, args[0]).Get(cmd.Context(), args[1])
			if err != nil {
				return err
			}
			return printer.New(cmd.OutOrStdout(), format).Value(key)
		},
	}
}

// newKeyCreateCmd builds `cinc <owner> key create <name> <key-name>`.
//
// Like `client create`, the server generates an RSA key pair by default
// and returns the private key, which is streamed to stdout or written to
// `--key-file`. `--public-key` supplies an existing public key instead, in
// which case the server returns no private key. `--expires` sets the
// expiration date (default "infinity").
func newKeyCreateCmd(owner keyOwner) *cobra.Command {
	var (
		keyFile       string
		publicKeyFile string
		expires       string
	)
	cmd := &cobra.Command{
		Use:   "create <" + owner.noun + "> <key-name>",
		Short: "Add a key to a " + owner.noun,
		Example: fmt.Sprintf(`Have the server generate the key pair and write the private key to a file.
cinc %[1]s key create %[2]s rotation --key-file rotation.pem

Register a public key you already have; the server generates nothing.
cinc %[1]s key create %[2]s laptop --public-key ~/.ssh/id_rsa.pub

Add a key that expires on a given date.
cinc %[1]s key create %[2]s temp --expires 2030-01-01T00:00:00Z`, owner.noun, owner.sample),
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			ownerName, keyName := args[0], args[1]
			k := &cinc.Key{Name: keyName, ExpirationDate: expires}
			if publicKeyFile != "" {
				pem, err := os.ReadFile(publicKeyFile)
				if err != nil {
					return fmt.Errorf("cinc: read public key: %w", err)
				}
				k.PublicKey = string(pem)
			} else {
				k.CreateKey = true
			}
			created, _, err := owner.scope(c, ownerName).Create(cmd.Context(), k)
			if err != nil {
				return err
			}
			if created.PrivateKey == "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Added key %q to %s %q\n", keyName, owner.noun, ownerName)
				return nil
			}
			fileMsg := fmt.Sprintf("Added key %q to %s %q (key written to %s)", keyName, owner.noun, ownerName, keyFile)
			return writePrivateKey(cmd.OutOrStdout(), created.PrivateKey, keyFile, fileMsg)
		},
	}
	cmd.Flags().StringVarP(&keyFile, "key-file", "f", "", "write the generated private key to this file instead of stdout")
	cmd.Flags().StringVar(&publicKeyFile, "public-key", "", "path to a PEM public key; the server will not generate a key pair")
	cmd.Flags().StringVar(&expires, "expires", "infinity", "expiration date (ISO-8601 UTC) or 'infinity'")
	return cmd
}

// newKeyEditCmd builds `cinc <owner> key edit <name> <key-name>`. It fetches
// the key, opens its JSON in the shared editor (where the expiration date,
// public key, or name can be changed), and PUTs the result back. `--file`
// reads the updated JSON from disk for scripted use, mirroring the other
// edit verbs.
func newKeyEditCmd(owner keyOwner) *cobra.Command {
	var inputFile string
	cmd := &cobra.Command{
		Use:   "edit <" + owner.noun + "> <key-name>",
		Short: "Edit one of a " + owner.noun + "'s keys",
		Example: fmt.Sprintf(`Edit one of a %[1]s's keys, for example its expiration.
cinc %[1]s key edit %[2]s default`, owner.noun, owner.sample),
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			ownerName, keyName := args[0], args[1]
			scope := owner.scope(c, ownerName)

			var updated cinc.Key
			if inputFile != "" {
				data, err := os.ReadFile(inputFile)
				if err != nil {
					return fmt.Errorf("cinc: read %s: %w", inputFile, err)
				}
				if err := json.Unmarshal(data, &updated); err != nil {
					return fmt.Errorf("cinc: parse %s: %w", inputFile, err)
				}
			} else {
				current, _, err := scope.Get(cmd.Context(), keyName)
				if err != nil {
					return err
				}
				edited, err := editKey(current)
				if err != nil {
					return err
				}
				if reflect.DeepEqual(*current, *edited) {
					fmt.Fprintf(cmd.OutOrStdout(), "Key %q on %s %q unchanged\n", keyName, owner.noun, ownerName)
					return nil
				}
				updated = *edited
			}
			// A key edit may rename the key (body name differs from the path
			// segment), so the body name is left as the user set it; only an
			// empty name is backfilled from the path so a --file without a
			// name still targets the right key.
			if updated.Name == "" {
				updated.Name = keyName
			}

			if _, _, err := scope.Update(cmd.Context(), keyName, &updated); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Updated key %q on %s %q\n", keyName, owner.noun, ownerName)
			return nil
		},
	}
	cmd.Flags().StringVar(&inputFile, "file", "", "read the updated key JSON from this file instead of launching the editor")
	return cmd
}

// newKeyDeleteCmd builds `cinc <owner> key delete <name> <key-name>`.
func newKeyDeleteCmd(owner keyOwner) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <" + owner.noun + "> <key-name>",
		Short: "Delete one of a " + owner.noun + "'s keys",
		Example: fmt.Sprintf(`Delete one of a %[1]s's keys.
cinc %[1]s key delete %[2]s rotation`, owner.noun, owner.sample),
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			ownerName, keyName := args[0], args[1]
			if _, err := owner.scope(c, ownerName).Delete(cmd.Context(), keyName); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted key %q from %s %q\n", keyName, owner.noun, ownerName)
			return nil
		},
	}
}

// writePrivateKey streams a server-generated private key to stdout (adding a
// trailing newline when the PEM lacks one) or, when keyFile is set, writes it
// 0600 and prints fileMsg. Callers handle the no-key (bring-your-own-public-
// key) case themselves. Shared by client/user create, key create, and client
// reregister.
func writePrivateKey(out io.Writer, priv, keyFile, fileMsg string) error {
	if keyFile != "" {
		// Open with O_TRUNC (not O_EXCL) so reregister and friends can
		// overwrite an existing key, then force 0600 explicitly: a
		// pre-existing file would otherwise keep its looser mode, and the
		// create mode is masked by umask. A private key must never be
		// group- or world-readable.
		f, err := os.OpenFile(keyFile, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("cinc: write key file: %w", err)
		}
		if err := f.Chmod(0o600); err != nil {
			_ = f.Close() // already returning an error
			return fmt.Errorf("cinc: write key file: %w", err)
		}
		if _, err := f.WriteString(priv); err != nil {
			_ = f.Close() // already returning an error
			return fmt.Errorf("cinc: write key file: %w", err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("cinc: write key file: %w", err)
		}
		fmt.Fprintln(out, fileMsg)
		return nil
	}
	if _, err := fmt.Fprint(out, priv); err != nil {
		return err
	}
	if !strings.HasSuffix(priv, "\n") {
		fmt.Fprintln(out)
	}
	return nil
}
