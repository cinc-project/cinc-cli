package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// runArgs runs the real command tree with args and returns the error.
// Argument validation happens before any RunE, so nothing here reaches
// a server or reads a credentials file.
func runArgs(t *testing.T, args ...string) error {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	root := newRootCmd()
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs(args)
	return root.Execute()
}

func TestArgErrorNamesTheMissingArgument(t *testing.T) {
	err := runArgs(t, "node", "show")
	if err == nil {
		t.Fatal("expected an error for a missing argument")
	}
	for _, want := range []string{
		"`cinc node show` needs <name>",
		"cinc node show <name>",
		"cinc node show --help",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got:\n%s", want, err)
		}
	}
	if strings.Contains(err.Error(), "arg(s)") {
		t.Errorf("error still has cobra's wording:\n%s", err)
	}
	if strings.Contains(err.Error(), "\n") {
		t.Errorf("an argument error should be one line, got:\n%s", err)
	}
}

func TestArgErrorNamesEveryMissingArgument(t *testing.T) {
	err := runArgs(t, "client", "key", "create", "worker-01")
	if err == nil || !strings.Contains(err.Error(), "needs <key-name>") {
		t.Errorf("error should name the one missing argument, got: %v", err)
	}
	err = runArgs(t, "databag", "secret", "show")
	if err == nil || !strings.Contains(err.Error(), "needs <bag> and <id>") {
		t.Errorf("error should name both missing arguments, got: %v", err)
	}
}

func TestArgErrorNamesTheExtraArguments(t *testing.T) {
	err := runArgs(t, "node", "show", "web01", "extra", "more")
	if err == nil {
		t.Fatal("expected an error for extra arguments")
	}
	for _, want := range []string{
		"only takes <name>",
		`"extra" "more"`,
		"quotes",
		"cinc node show <name>",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got:\n%s", want, err)
		}
	}
}

func TestArgErrorForACommandThatTakesNone(t *testing.T) {
	err := runArgs(t, "node", "list", "web01")
	if err == nil || !strings.Contains(err.Error(), "`cinc node list` doesn't take any arguments") ||
		!strings.Contains(err.Error(), `"web01"`) {
		t.Errorf("error should say the command takes no arguments, got: %v", err)
	}
}

func TestArgErrorForAVariadicArgument(t *testing.T) {
	err := runArgs(t, "group", "member", "add", "admins")
	if err == nil || !strings.Contains(err.Error(), "needs at least one <name>") {
		t.Errorf("error should ask for at least one <name>, got: %v", err)
	}
}

// Every leaf command's argument errors must read as our own message, never
// cobra's "accepts 1 arg(s), received 0", whatever the count.
func TestEveryArgErrorIsExplained(t *testing.T) {
	root := newRootCmd()
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
		}
		if c.Args == nil {
			return
		}
		for n := 0; n <= 5; n++ {
			args := make([]string, n)
			for i := range args {
				args[i] = "x"
			}
			err := c.Args(c, args)
			if err == nil {
				continue
			}
			msg := err.Error()
			if strings.Contains(msg, "arg(s)") || strings.HasPrefix(msg, "unknown command") {
				t.Errorf("%s with %d args: unexplained error %q", c.CommandPath(), n, msg)
			}
			if strings.Contains(msg, "can't use those arguments") {
				t.Errorf("%s with %d args: its Use line %q doesn't match its Args validator: %q", c.CommandPath(), n, c.Use, msg)
			}
			if !strings.Contains(msg, c.UseLine()) {
				t.Errorf("%s with %d args: error should show the usage line, got %q", c.CommandPath(), n, msg)
			}
		}
	}
	walk(root)
}
