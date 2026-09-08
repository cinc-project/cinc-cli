package explore

import (
	"context"
	"encoding/json"
	"fmt"

	cinc "github.com/cinc-project/cinc-api"
)

// dataBagKind is the top-level Data Bags kind: a bag is a named
// container of items. Bags are created by name and drilled into to
// reach their items.
type dataBagKind struct{}

func (dataBagKind) Title() string     { return "Data Bags" }
func (dataBagKind) Columns() []string { return []string{"NAME"} }

func (dataBagKind) List(ctx context.Context, c *cinc.Client) ([]Row, error) {
	index, _, err := c.DataBags.List(ctx)
	if err != nil {
		return nil, err
	}
	return nameRows(index), nil
}

func (dataBagKind) CreateNamed(ctx context.Context, c *cinc.Client, name string) (CreateResult, error) {
	if _, err := c.DataBags.Create(ctx, name); err != nil {
		return CreateResult{}, err
	}
	return CreateResult{Name: name}, nil
}

func (dataBagKind) Delete(ctx context.Context, c *cinc.Client, name string) error {
	_, err := c.DataBags.Delete(ctx, name)
	return err
}

func (dataBagKind) Child(parent string) Kind { return dataBagItemsKind{bag: parent} }

// Summary shows how many items a bag holds — the one fact worth knowing
// before drilling in.
func (dataBagKind) Summary(ctx context.Context, c *cinc.Client, name string) (summaryView, error) {
	return summarize(ctx, c, name,
		func(ctx context.Context, c *cinc.Client, bag string) (*map[string]string, error) {
			index, _, err := c.DataBags.Items(bag).List(ctx)
			return &index, err
		},
		nil,
		func(_ context.Context, _ *cinc.Client, index *map[string]string) []summaryField {
			return []summaryField{{"Items", count(len(*index))}}
		})
}

// dataBagItemsKind lists and edits the items of one data bag. Items are
// free-form JSON objects keyed by their "id".
type dataBagItemsKind struct{ bag string }

func (k dataBagItemsKind) Title() string   { return k.bag }
func (dataBagItemsKind) Columns() []string { return []string{"ID"} }

// SearchIndex makes a bag's items searchable under the bag's own name,
// which is how the Chef search index keys data bag items.
func (k dataBagItemsKind) SearchIndex() string { return k.bag }

func (k dataBagItemsKind) List(ctx context.Context, c *cinc.Client) ([]Row, error) {
	index, _, err := c.DataBags.Items(k.bag).List(ctx)
	if err != nil {
		return nil, err
	}
	return nameRows(index), nil
}

func (k dataBagItemsKind) Describe(ctx context.Context, c *cinc.Client, id string) (string, error) {
	item, _, err := c.DataBags.Items(k.bag).Get(ctx, id)
	if err != nil {
		return "", err
	}
	return prettyJSON(item)
}

// Summary shows a free-form item's shape — its id, how many top-level keys
// it carries, and which keys those are — without dumping the whole JSON.
func (k dataBagItemsKind) Summary(ctx context.Context, c *cinc.Client, id string) (summaryView, error) {
	return summarize(ctx, c, id,
		func(ctx context.Context, c *cinc.Client, id string) (*cinc.DataBagItem, error) {
			item, _, err := c.DataBags.Items(k.bag).Get(ctx, id)
			return &item, err
		},
		nil,
		func(_ context.Context, _ *cinc.Client, item *cinc.DataBagItem) []summaryField {
			return dataBagItemSummaryFields(*item)
		})
}

// dataBagItemSummaryFields builds the curated facts panel for a data bag
// item. The "id" key names the item and is already the heading, so the key
// list and count cover the rest of its top-level shape.
func dataBagItemSummaryFields(item cinc.DataBagItem) []summaryField {
	keys := sortedKeys(item)
	return []summaryField{
		{"ID", orDash(itemID(item))},
		{"Keys", count(len(keys))},
		{"Top Keys", list(keys, 8)},
	}
}

func (k dataBagItemsKind) Save(ctx context.Context, c *cinc.Client, id string, edited []byte) error {
	var item cinc.DataBagItem
	if err := json.Unmarshal(edited, &item); err != nil {
		return fmt.Errorf("parse edited data bag item: %w", err)
	}
	_, _, err := c.DataBags.Items(k.bag).Update(ctx, item)
	return err
}

func (dataBagItemsKind) NewTemplate() []byte {
	return []byte(`{
  "id": "new-item"
}`)
}

func (k dataBagItemsKind) Create(ctx context.Context, c *cinc.Client, doc []byte) (CreateResult, error) {
	var item cinc.DataBagItem
	if err := json.Unmarshal(doc, &item); err != nil {
		return CreateResult{}, fmt.Errorf("parse new data bag item: %w", err)
	}
	if _, _, err := c.DataBags.Items(k.bag).Create(ctx, item); err != nil {
		return CreateResult{}, err
	}
	return CreateResult{Name: itemID(item)}, nil
}

func (k dataBagItemsKind) Delete(ctx context.Context, c *cinc.Client, id string) error {
	_, err := c.DataBags.Items(k.bag).Delete(ctx, id)
	return err
}

// itemID extracts the "id" field a data bag item is keyed by.
func itemID(item cinc.DataBagItem) string {
	if id, ok := item["id"].(string); ok {
		return id
	}
	return ""
}
