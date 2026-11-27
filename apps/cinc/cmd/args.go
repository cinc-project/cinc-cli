package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// explainArgErrors wraps the Args validator of root and every command
// under it, so a wrong argument count says which argument is missing or
// extra, with the usage line, instead of cobra's "accepts 1 arg(s),
// received 0". A validator's other errors pass through unchanged.
func explainArgErrors(root *cobra.Command) {
	if validate := root.Args; validate != nil {
		root.Args = func(cmd *cobra.Command, args []string) error {
			if err := validate(cmd, args); err != nil {
				return argCountError(cmd, args, err)
			}
			return nil
		}
	}
	for _, sub := range root.Commands() {
		explainArgErrors(sub)
	}
}

// argCountError rewrites one of cobra's argument-count errors using the
// placeholders in cmd.Use: <name> is required, [name] is optional, and a
// trailing ... repeats the last one. Errors that aren't about the count,
// or a count the Use line can't explain, keep cobra's text but still gain
// the usage line.
func argCountError(cmd *cobra.Command, args []string, err error) error {
	msg := err.Error()
	if !strings.Contains(msg, "arg(s)") && !strings.HasPrefix(msg, "unknown command") {
		return err
	}
	path := cmd.CommandPath()
	// One line, like every other error: the usage and where the examples are.
	usage := fmt.Sprintf(" Usage: `%s`. Run `%s --help` to see examples.", cmd.UseLine(), path)

	var required, all []string
	variadic := false
	for _, f := range strings.Fields(cmd.Use)[1:] {
		if strings.HasSuffix(f, "...") {
			variadic = true
			f = strings.TrimSuffix(f, "...")
		}
		all = append(all, f)
		if strings.HasPrefix(f, "<") {
			required = append(required, f)
		}
	}

	switch {
	case len(all) == 0 && len(args) > 0:
		return fmt.Errorf("`%s` doesn't take any arguments, but you gave it %s.%s", path, quoteAll(args), usage)
	case len(args) < len(required):
		missing := required[len(args):]
		if variadic && len(missing) == 1 && missing[0] == all[len(all)-1] {
			return fmt.Errorf("`%s` needs at least one %s.%s", path, missing[0], usage)
		}
		return fmt.Errorf("`%s` needs %s.%s", path, joinAnd(missing), usage)
	case !variadic && len(args) > len(all):
		return fmt.Errorf("`%s` only takes %s, but you also gave it %s. If one value has spaces in it, wrap it in quotes.%s",
			path, joinAnd(all), quoteAll(args[len(all):]), usage)
	}
	return fmt.Errorf("`%s` can't use those arguments (%s).%s", path, msg, usage)
}

// joinAnd joins items as "a", "a and b", or "a, b and c".
func joinAnd(items []string) string {
	if len(items) <= 1 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// quoteAll quotes each argument so empty or spaced values stay visible.
func quoteAll(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = fmt.Sprintf("%q", a)
	}
	return strings.Join(q, " ")
}
