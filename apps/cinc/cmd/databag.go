package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"

	cinc "github.com/cinc-project/cinc-api"
	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/cli/printer"
)

// newDataBagCmd builds the `cinc databag` command group.
func newDataBagCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "databag",
		Short: "Manage data bags on the Cinc Server",
	}
	cmd.AddCommand(newDataBagListCmd())
	cmd.AddCommand(newDataBagShowCmd())
	cmd.AddCommand(newDataBagCreateCmd())
	cmd.AddCommand(newDataBagDeleteCmd())
	cmd.AddCommand(newDataBagItemCmd())
	cmd.AddCommand(newDataBagSecretCmd())
	cmd.AddCommand(newACLCmd("databag", "data"))
	return cmd
}

// newDataBagShowCmd builds the `cinc databag show <bag>` command. A
// data bag has no document of its own on the server, so showing a bag
// enumerates the item IDs it holds — mirroring knife's `data bag show
// BAG`. Drill into an individual item with `cinc databag item show`.
func newDataBagShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <bag>",
		Short: "Show a data bag's item IDs",
		Example: `Show the item IDs in a data bag.
cinc databag show passwords`,
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
			ids, err := fetchDataBagItemIDs(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			return printer.New(cmd.OutOrStdout(), format).List(ids)
		},
	}
}

// newDataBagCreateCmd builds `cinc databag create <bag> [item]`,
// mirroring knife's `knife data bag create BAG [ITEM]`:
//
//   - One arg: POST an empty bag to /data. A 409 from the server is
//     surfaced so the user knows the bag was already there.
//   - Two args: ensure the bag exists (a 409 is silent — the user
//     asked for an item too, so the bag's prior existence is fine),
//     then create the named item. When no --file is supplied the
//     built-in JSON editor opens with `{"id": "<item>"}` as the
//     starting template.
func newDataBagCreateCmd() *cobra.Command {
	var inputFile string
	cmd := &cobra.Command{
		Use:   "create <bag> [item]",
		Short: "Create a data bag, optionally with an initial item",
		Example: `Create an empty data bag.
cinc databag create passwords
Create a data bag and add an item in one step (opens your editor).
cinc databag create passwords mysql`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			bag := args[0]

			bagErr := bagCreateOrPropagate(cmd, c, bag)
			if len(args) == 1 {
				if bagErr != nil {
					return bagErr
				}
				fmt.Fprintf(out, "Created data bag %q\n", bag)
				return nil
			}
			// 2-arg form: the bag must now exist, but it may have
			// existed before this command ran. Only announce a fresh
			// bag creation when one actually happened.
			if bagErr != nil && !errors.Is(bagErr, cinc.ErrConflict) {
				return bagErr
			}
			if bagErr == nil {
				fmt.Fprintf(out, "Created data bag %q\n", bag)
			}

			id := args[1]
			item, err := loadOrEditNewItem(id, inputFile)
			if err != nil {
				return err
			}
			item["id"] = id
			if _, _, err := c.DataBags.Items(bag).Create(cmd.Context(), item); err != nil {
				return err
			}
			fmt.Fprintf(out, "Created item %q in data bag %q\n", id, bag)
			return nil
		},
	}
	cmd.Flags().StringVar(&inputFile, "file", "", "read the new item JSON from this file instead of launching the editor (2-arg form only)")
	return cmd
}

// bagCreateOrPropagate POSTs the bag and returns the error from the
// server unchanged. Conflict (409) is returned as a normal error so
// the caller can decide whether to surface or swallow it.
func bagCreateOrPropagate(cmd *cobra.Command, c *cinc.Client, bag string) error {
	_, err := c.DataBags.Create(cmd.Context(), bag)
	return err
}

// loadOrEditNewItem produces a DataBagItem either by reading the
// given file or by opening the editor on a stub item carrying just
// the id field.
func loadOrEditNewItem(id, inputFile string) (cinc.DataBagItem, error) {
	if inputFile != "" {
		data, err := os.ReadFile(inputFile)
		if err != nil {
			return nil, fmt.Errorf("cinc: read %s: %w", inputFile, err)
		}
		if err := validateDataBagItem(data); err != nil {
			return nil, err
		}
		var item cinc.DataBagItem
		if err := json.Unmarshal(data, &item); err != nil {
			return nil, fmt.Errorf("cinc: parse %s: %w", inputFile, err)
		}
		return item, nil
	}
	return editDataBagItem(cinc.DataBagItem{"id": id})
}

// newDataBagItemCmd builds the `cinc databag item` command group.
// Data bag items are arbitrary JSON documents (always carrying an
// "id" key) so the item-level verbs live under their own subgroup.
func newDataBagItemCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "item",
		Short: "Manage items within a data bag",
	}
	cmd.AddCommand(newDataBagItemListCmd())
	cmd.AddCommand(newDataBagItemShowCmd())
	cmd.AddCommand(newDataBagItemCreateCmd())
	cmd.AddCommand(newDataBagItemEditCmd())
	cmd.AddCommand(newDataBagItemDeleteCmd())
	return cmd
}

// newDataBagItemCreateCmd builds `cinc databag item create <bag> <id>`. The
// bag must already exist (use `cinc databag create` to make one). Without
// --file the built-in JSON editor opens on a stub carrying just the id; the
// path arg's id always pins the item identifier. This is the item-scoped
// equivalent of the two-arg `databag create <bag> <item>` form.
func newDataBagItemCreateCmd() *cobra.Command {
	var inputFile string
	cmd := &cobra.Command{
		Use:   "create <bag> <id>",
		Short: "Create an item in a data bag",
		Example: `Create an item in a data bag; your editor opens to edit its JSON.
cinc databag item create passwords mysql`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			if _, _, err := c.DataBags.Items(bag).Create(cmd.Context(), item); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created item %q in data bag %q\n", id, bag)
			return nil
		},
	}
	cmd.Flags().StringVar(&inputFile, "file", "", "read the new item JSON from this file instead of launching the editor")
	return cmd
}

// newDataBagItemShowCmd builds `cinc databag item show <bag> <id>`. It
// fetches a single item and renders its JSON document — the human
// format is the same pretty-printed JSON knife emits.
func newDataBagItemShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <bag> <id>",
		Short: "Show a data bag item on the server",
		Example: `Show a data bag item.
cinc databag item show passwords mysql`,
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
			bag, id := args[0], args[1]
			item, _, err := c.DataBags.Items(bag).Get(cmd.Context(), id)
			if err != nil {
				return err
			}
			return printer.New(cmd.OutOrStdout(), format).Value(item)
		},
	}
}

// newDataBagItemDeleteCmd builds `cinc databag item delete <bag> <id>`.
// It removes a single item from a bag, leaving the bag itself in place.
func newDataBagItemDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <bag> <id>",
		Short: "Delete a data bag item from the server",
		Example: `Delete an item from a data bag.
cinc databag item delete passwords mysql`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			bag, id := args[0], args[1]
			if _, err := c.DataBags.Items(bag).Delete(cmd.Context(), id); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted item %q from data bag %q\n", id, bag)
			return nil
		},
	}
}

// newDataBagItemListCmd builds the `cinc databag item list <bag>`
// command. It enumerates the item IDs within one bag — the bag itself
// is required because items are scoped per bag on the server.
func newDataBagItemListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <bag>",
		Short: "List items in a data bag",
		Example: `List the item IDs in a data bag.
cinc databag item list passwords`,
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
			ids, err := fetchDataBagItemIDs(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			return printer.New(cmd.OutOrStdout(), format).List(ids)
		},
	}
}

// fetchDataBagItemIDs returns the sorted item IDs within a single bag.
func fetchDataBagItemIDs(ctx context.Context, c *cinc.Client, bag string) ([]string, error) {
	index, _, err := c.DataBags.Items(bag).List(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(index))
	for id := range index {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}

// newDataBagItemEditCmd builds `cinc databag item edit <bag> <id>`.
// It fetches the item, opens its JSON in the built-in editor (same
// engine as `cinc client edit`), validates on save, and PUTs the
// result back. `--file` reads the updated JSON from disk for
// scripted use. The path arg's id pins the item identifier so an
// edit can never accidentally rename the item out from under itself.
func newDataBagItemEditCmd() *cobra.Command {
	var inputFile string
	cmd := &cobra.Command{
		Use:   "edit <bag> <id>",
		Short: "Edit a data bag item on the server",
		Example: `Edit a data bag item in your editor.
cinc databag item edit passwords mysql`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
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
				if err := json.Unmarshal(data, &updated); err != nil {
					return fmt.Errorf("cinc: parse %s: %w", inputFile, err)
				}
				if err := validateDataBagItem(data); err != nil {
					return err
				}
			} else {
				current, _, err := items.Get(cmd.Context(), id)
				if err != nil {
					return err
				}
				edited, err := editDataBagItem(current)
				if err != nil {
					return err
				}
				if reflect.DeepEqual(current, edited) {
					fmt.Fprintf(cmd.OutOrStdout(), "Item %q in bag %q unchanged\n", id, bag)
					return nil
				}
				updated = edited
			}
			updated["id"] = id

			if _, _, err := items.Update(cmd.Context(), updated); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Updated item %q in bag %q\n", id, bag)
			return nil
		},
	}
	cmd.Flags().StringVar(&inputFile, "file", "", "read the updated item JSON from this file instead of launching the editor")
	return cmd
}

// validateDataBagItem checks that b is valid JSON and contains a
// non-empty string "id" key. It is exported via the editor seam.
func validateDataBagItem(b []byte) error {
	var item cinc.DataBagItem
	if err := json.Unmarshal(b, &item); err != nil {
		return err
	}
	if item.ID() == "" {
		return errors.New("data bag item is missing a non-empty \"id\" field")
	}
	return nil
}

// newDataBagDeleteCmd builds the `cinc databag delete <name>` command.
func newDataBagDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a data bag from the server",
		Example: `Delete a data bag and all of its items.
cinc databag delete passwords`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			name := args[0]
			if _, err := c.DataBags.Delete(cmd.Context(), name); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted data bag %q\n", name)
			return nil
		},
	}
}

// newDataBagListCmd builds the `cinc databag list` command.
func newDataBagListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List data bags on the server",
		Example: `List every data bag on the server.
cinc databag list`,
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
			names, err := fetchDataBagNames(cmd.Context(), c)
			if err != nil {
				return err
			}
			return printer.New(cmd.OutOrStdout(), format).List(names)
		},
	}
}

// fetchDataBagNames returns the sorted names of every data bag on the
// server.
func fetchDataBagNames(ctx context.Context, c *cinc.Client) ([]string, error) {
	index, _, err := c.DataBags.List(ctx)
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
