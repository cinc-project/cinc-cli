package cmd

import (
	"errors"
	"fmt"

	cinc "github.com/cinc-project/cinc-api"
	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/cli/printer"
	"github.com/cinc-project/cinc-cli/cli/progname"
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

// resolveSecretClient resolves the profile once and returns both the data
// bag secret and a server client for it. The secret comes first, so a
// missing or unusable secret fails before anything talks to the server.
func resolveSecretClient(cmd *cobra.Command) (*dataBagSecret, *cinc.Client, error) {
	profile, err := resolveProfile(cmd)
	if err != nil {
		return nil, nil, err
	}
	secret, source, err := resolveSecret(cmd, profile)
	if err != nil {
		return nil, nil, err
	}
	c, err := clientForProfile(cmd, profile)
	if err != nil {
		return nil, nil, err
	}
	return &dataBagSecret{key: secret, source: source}, c, nil
}

// dataBagSecret is a resolved data bag secret and where it came from, so
// a failed decrypt can say which secret it tried.
type dataBagSecret struct {
	key    []byte
	source string // e.g. "the --secret value" or "/path (from --secret-file)"
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
			secret, c, err := resolveSecretClient(cmd)
			if err != nil {
				return err
			}
			bag, id := args[0], args[1]
			item, err := loadOrEditNewItem(id, inputFile)
			if err != nil {
				return err
			}
			if _, _, err := c.DataBags.Items(bag).CreateEncrypted(cmd.Context(), item, secret.key); err != nil {
				return encryptItemError(err, bag, id)
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
			secret, c, err := resolveSecretClient(cmd)
			if err != nil {
				return err
			}
			bag, id := args[0], args[1]
			plain, _, err := c.DataBags.Items(bag).GetDecrypted(cmd.Context(), id, secret.key)
			if err != nil {
				return decryptItemError(cmd, err, bag, id, secret.source)
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
// disk for scripted use; like `databag item edit`, a file or edit naming a
// different id is refused.
func newDataBagSecretEditCmd() *cobra.Command {
	var inputFile string
	cmd := &cobra.Command{
		Use:   "edit <bag> <id>",
		Short: "Edit an encrypted data bag item",
		Example: `Edit an encrypted data bag item in your editor.
cinc databag secret edit passwords mysql --secret-file ~/.cinc/secret`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			secret, c, err := resolveSecretClient(cmd)
			if err != nil {
				return err
			}
			bag, id := args[0], args[1]
			items := c.DataBags.Items(bag)

			var updated cinc.DataBagItem
			if inputFile != "" {
				if updated, err = readDataBagItemFile(inputFile, id); err != nil {
					return err
				}
			} else {
				plain, _, err := items.GetDecrypted(cmd.Context(), id, secret.key)
				if err != nil {
					return decryptItemError(cmd, err, bag, id, secret.source)
				}
				edited, err := editDataBagItem(plain)
				if err != nil {
					return err
				}
				if unchanged(plain, edited) {
					fmt.Fprintf(cmd.OutOrStdout(), "Encrypted item %q in bag %q unchanged\n", id, bag)
					return nil
				}
				updated = edited
			}
			if _, _, err := items.UpdateEncrypted(cmd.Context(), updated, secret.key); err != nil {
				return encryptItemError(err, bag, id)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Updated encrypted item %q in bag %q\n", id, bag)
			return nil
		},
	}
	cmd.Flags().StringVar(&inputFile, "file", "", "read the updated item JSON from this file instead of launching the editor")
	addSecretFlags(cmd)
	return cmd
}

// encryptItemError explains an item that is already encrypted, which is
// what you get by handing `databag secret create` or `edit` an item copied
// from the server rather than its plaintext. Encrypting it again would
// store ciphertext that decrypts to more ciphertext. Anything else passes
// through unchanged.
func encryptItemError(err error, bag, id string) error {
	if errors.Is(err, cinc.ErrAlreadyEncrypted) {
		return fmt.Errorf("item %[1]q for data bag %[2]q already holds encrypted values, and encrypting them again would make it unreadable. Give us the plaintext values instead (`%[3]s databag secret show %[2]s %[1]s` prints them), or store the encrypted item as-is with `%[3]s databag item create` or `edit`.", id, bag, progname.Get())
	}
	return err
}

// decryptItemError turns a Decrypt failure into a conversational message.
// A not-encrypted item points the user at the plaintext commands; an
// authentication failure explains shared secrets, says which secret we
// tried (source), and how to point cinc at the right one. Anything else
// passes through unchanged.
func decryptItemError(cmd *cobra.Command, err error, bag, id, source string) error {
	prog := progname.Get()
	switch {
	case errors.Is(err, cinc.ErrNotEncrypted):
		return fmt.Errorf("item %[1]q in data bag %[2]q isn't encrypted, so there's nothing to decrypt. Read it with `%[3]s databag item show %[2]s %[1]s`, or change it with `%[3]s databag item edit %[2]s %[1]s`.", id, bag, prog)
	case errors.Is(err, cinc.ErrDataBagAuth):
		return fmt.Errorf(`couldn't decrypt item %q in data bag %q with the secret we used, %s. That's probably the wrong secret for this item.

An encrypted data bag item is locked with a shared secret: a piece of text chosen by whoever encrypted it. Only that exact text unlocks the item, so any other secret, even one that works for a different data bag, fails like this.

To fix it, get the secret this item was encrypted with (whoever set up the data bag has it), save it in a file, and pass it with --secret-file <path>. To use it every time, add secret_file = "<path>" to your %q profile in %s.`,
			id, bag, source, profileNameForCommand(cmd), resolveConfigPath(cmd))
	}
	return err
}
