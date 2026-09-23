package suite

// orgFamily is not ported from test/acceptance yet.
var orgFamily = family{
	pending: []string{
		"org create",
		"org delete",
		"org edit",
		"org invite create",
		"org invite list",
		"org invite rescind",
		"org list",
		"org member add",
		"org member list",
		"org member remove",
		"org show",
	},
}
