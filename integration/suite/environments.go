package suite

// environmentFamily is not ported from test/acceptance yet.
var environmentFamily = family{
	pending: []string{
		"environment create",
		"environment delete",
		"environment edit",
		"environment list",
		"environment show",
	},
}
