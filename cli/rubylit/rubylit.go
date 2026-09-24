// Package rubylit renders Go values as Ruby source literals for the Ruby
// configuration files cinc generates (client.rb and friends), which the
// target's cinc-client evaluates as code.
package rubylit

import "strings"

// Quote renders s as a single-quoted Ruby string literal. Ruby single quotes
// do not interpolate #{...} (unlike double quotes and unlike Go's %q), so a
// value such as a node or policy name can't inject Ruby into the file that
// embeds it. Only backslash and the single quote itself are special inside a
// single-quoted Ruby literal.
func Quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}
