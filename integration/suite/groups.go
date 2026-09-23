package suite

// groupFamily is not ported from test/acceptance yet.
var groupFamily = family{
	pending: []string{
		"group create",
		"group delete",
		"group edit",
		"group list",
		"group member add",
		"group member remove",
		"group show",
	},
}
