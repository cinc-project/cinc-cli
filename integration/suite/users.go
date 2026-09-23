package suite

// userFamily is not ported from test/acceptance yet.
var userFamily = family{
	pending: []string{
		"user create",
		"user delete",
		"user edit",
		"user list",
		"user password",
		"user show",
	},
}
