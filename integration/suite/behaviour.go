package suite

// cliFamily covers CLI behaviour rather than one noun: config, first-run
// setup, profiles, chef-compat environment variables, output formats and
// error messages. Not ported from test/acceptance yet.
var cliFamily = family{
	pending: []string{
		"config create",
		"config validate",
		"version",
	},
}
