package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"

	cinc "github.com/cinc-project/cinc-api"
	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/cli/printer"
)

// newDataBagSecretCmd builds the `cinc databag secret` command group:
// Chef-compatible encrypted data bag items. The plaintext you write is
// encrypted client-side (AES-256-GCM, the modern v3 wire format) with a
// shared secret before it ever leaves your machine, and decrypted on the
// way back. The item's "id" is always left in cleartext so the server can
// index it.
//
// Every command here needs the secret. Supply it with --secret-file (or
// the literal --secret), $CINC_SECRET_FILE, or a `secret_file` key in your
// credentials profile.
//
// There's no `list` or `delete` here on purpose: enumerating or removing
// an item never touches its contents, so reach for `cinc databag item
// list` and `cinc databag item delete` — they work the same whether or not
// the item is encrypted.
func newDataBagSecretCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secret",
		Short: "Manage encrypted data bag items",
		Long: `Manage Chef-compatible encrypted data bag items.

Values are encrypted client-side with a shared secret before upload and
decrypted on read; the item "id" stays in cleartext. Provide the secret
with --secret-file, --secret, $CINC_SECRET_FILE, or a secret_file key in
your profile.

To list or delete encrypted items use the regular "cinc databag item list"
and "cinc databag item delete" commands; neither needs the secret.`,
	}
	cmd.AddCommand(newDataBagSecretCreateCmd())
	cmd.AddCommand(newDataBagSecretShowCmd())
	cmd.AddCommand(newDataBagSecretEditCmd())
	return cmd
}

// addSecretFlags registers the secret-resolution flags shared by every
// `databag secret` command.
func addSecretFlags(cmd *cobra.Command) {
	cmd.Flags().String("secret-file", "", "path to the encrypted data bag secret file")
	cmd.Flags().String("secret", "", "the encrypted data bag secret as a literal string (mutually exclusive with --secret-file)")
}

// newDataBagSecretCreateCmd builds `cinc databag secret create <bag> <id>`.
// The bag must already exist (use `cinc databag create`). Without --file
// the built-in JSON editor opens on a stub carrying just the id; every
// value except the id is then encrypted before the item is POSTed.
func newDataBagSecretCreateCmd() *cobra.Command {
	var inputFile string
	cmd := &cobra.Command{
		Use:   "create <bag> <id>",
		Short: "Create an encrypted item in a data bag",
		Example: `Create an encrypted item; your editor opens to edit its plaintext JSON.
cinc databag secret create passwords mysql --secret-file ~/.cinc/secret`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			profile, err := resolveProfile(cmd)
			if err != nil {
				return err
			}
			secret, err := resolveSecret(cmd, profile)
			if err != nil {
				return err
			}
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			bag, id := args[0], args[1]
			item, err := loadOrEditNewItem(id, inputFile)
			if err != nil {
				return err
			}
			item["id"] = id
			encrypted, err := item.Encrypt(secret)
			if err != nil {
				return err
			}
			if _, _, err := c.DataBags.Items(bag).Create(cmd.Context(), encrypted); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created encrypted item %q in data bag %q\n", id, bag)
			return nil
		},
	}
	cmd.Flags().StringVar(&inputFile, "file", "", "read the new item JSON from this file instead of launching the editor")
	addSecretFlags(cmd)
	return cmd
}

// newDataBagSecretShowCmd builds `cinc databag secret show <bag> <id>`. It
// fetches the item, decrypts it with the resolved secret, and renders the
// plaintext document (human format is the same pretty-printed JSON knife
// emits).
func newDataBagSecretShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <bag> <id>",
		Short: "Show and decrypt an encrypted data bag item",
		Example: `Show an encrypted data bag item, decrypted.
cinc databag secret show passwords mysql --secret-file ~/.cinc/secret`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := resolveFormat(cmd)
			if err != nil {
				return err
			}
			profile, err := resolveProfile(cmd)
			if err != nil {
				return err
			}
			secret, err := resolveSecret(cmd, profile)
			if err != nil {
				return err
			}
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			bag, id := args[0], args[1]
			item, _, err := c.DataBags.Items(bag).Get(cmd.Context(), id)
			if err != nil {
				return err
			}
			plain, err := item.Decrypt(secret)
			if err != nil {
				return decryptItemError(err, bag, id)
			}
			return printer.New(cmd.OutOrStdout(), format).Value(plain)
		},
	}
	addSecretFlags(cmd)
	return cmd
}

// newDataBagSecretEditCmd builds `cinc databag secret edit <bag> <id>`. It
// fetches and decrypts the item, opens its plaintext in the built-in JSON
// editor (same engine as `cinc databag item edit`), re-encrypts the saved
// result, and PUTs it back. `--file` reads the updated plaintext JSON from
// disk for scripted use. The path arg's id pins the identifier.
func newDataBagSecretEditCmd() *cobra.Command {
	var inputFile string
	cmd := &cobra.Command{
		Use:   "edit <bag> <id>",
		Short: "Edit an encrypted data bag item",
		Example: `Edit an encrypted data bag item in your editor.
cinc databag secret edit passwords mysql --secret-file ~/.cinc/secret`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			profile, err := resolveProfile(cmd)
			if err != nil {
				return err
			}
			secret, err := resolveSecret(cmd, profile)
			if err != nil {
				return err
			}
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			bag, id := args[0], args[1]
			items := c.DataBags.Items(bag)

			var updated cinc.DataBagItem
			if inputFile != "" {
				data, err := os.ReadFile(inputFile)
				if err != nil {
					return fmt.Errorf("cinc: read %s: %w", inputFile, err)
				}
				if err := validateDataBagItem(data); err != nil {
					return err
				}
				if err := json.Unmarshal(data, &updated); err != nil {
					return fmt.Errorf("cinc: parse %s: %w", inputFile, err)
				}
			} else {
				current, _, err := items.Get(cmd.Context(), id)
				if err != nil {
					return err
				}
				plain, err := current.Decrypt(secret)
				if err != nil {
					return decryptItemError(err, bag, id)
				}
				edited, err := editDataBagItem(plain)
				if err != nil {
					return err
				}
				if reflect.DeepEqual(plain, edited) {
					fmt.Fprintf(cmd.OutOrStdout(), "Encrypted item %q in bag %q unchanged\n", id, bag)
					return nil
				}
				updated = edited
			}
			updated["id"] = id
			encrypted, err := updated.Encrypt(secret)
			if err != nil {
				return err
			}
			if _, _, err := items.Update(cmd.Context(), encrypted); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Updated encrypted item %q in bag %q\n", id, bag)
			return nil
		},
	}
	cmd.Flags().StringVar(&inputFile, "file", "", "read the updated item JSON from this file instead of launching the editor")
	addSecretFlags(cmd)
	return cmd
}

// decryptItemError turns a Decrypt failure into a conversational message.
// A not-encrypted item points the user at the plaintext command; an
// authentication failure suggests a wrong secret. Anything else passes
// through unchanged.
func decryptItemError(err error, bag, id string) error {
	switch {
	case errors.Is(err, cinc.ErrNotEncrypted):
		return fmt.Errorf("item %q in data bag %q isn't encrypted — read it with `cinc databag item show %s %s` instead.", id, bag, bag, id)
	case errors.Is(err, cinc.ErrDataBagAuth):
		return fmt.Errorf("couldn't decrypt item %q in data bag %q — wrong secret? Check your --secret-file or the secret_file key in your profile.", id, bag)
	}
	return err
}
