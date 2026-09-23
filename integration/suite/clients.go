package suite

// clientFamily is not ported from test/acceptance yet.
var clientFamily = family{
	pending: []string{
		"client create",
		"client delete",
		"client edit",
		"client list",
		"client reregister",
		"client show",
	},
}
