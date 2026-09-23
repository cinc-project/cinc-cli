package suite

// policyFamily is not ported from test/acceptance yet.
var policyFamily = family{
	pending: []string{
		"policy clean",
		"policy clean-cookbooks",
		"policy create",
		"policy delete",
		"policy diff",
		"policy export",
		"policy install",
		"policy list",
		"policy push",
		"policy push-archive",
		"policy show",
		"policy-group delete",
		"policy-group list",
		"policy-group show",
	},
}
