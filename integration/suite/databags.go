package suite

// databagFamily is not ported from test/acceptance yet.
var databagFamily = family{
	pending: []string{
		"databag create",
		"databag delete",
		"databag item create",
		"databag item delete",
		"databag item edit",
		"databag item list",
		"databag item show",
		"databag list",
		"databag secret create",
		"databag secret edit",
		"databag secret show",
		"databag show",
	},
}
