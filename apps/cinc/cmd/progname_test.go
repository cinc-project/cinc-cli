package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/cli/progname"
)

func TestProgramNameFromArgv(t *testing.T) {
	cases := map[string]string{
		"/usr/bin/cinc":                     "cinc",
		"/opt/cinc-workstation/bin/cinc-ng": "cinc-ng",
		"cinc-ng":                           "cinc-ng",
		// Windows resolves executables case-insensitively.
		"cinc.exe":    "cinc",
		"CINC.EXE":    "CINC",
		"Cinc-Ng.Exe": "Cinc-Ng",
		// Unusable argv[0] falls back to the canonical name.
		"":  "cinc",
		".": "cinc",
		// argv[0] is caller-controlled and cobra interpolates the root
		// name into completion scripts unquoted, so anything that is not
		// a plain command name is refused rather than echoed.
		"cinc`id`":       "cinc",
		"cinc; echo hi":  "cinc",
		"cinc$(id)":      "cinc",
		"cinc'":          "cinc",
		"cinc\"":         "cinc",
		"-cinc":          "cinc",
		".cinc":          "cinc",
		"cinc\nid":       "cinc",
		"../../etc/pass": "pass",
	}
	for argv0, want := range cases {
		if got := programNameFromArgv(argv0); got != want {
			t.Errorf("programNameFromArgv(%q) = %q, want %q", argv0, got, want)
		}
	}
}

// withProgramName renames the tree the way Execute does and restores the
// package-global afterwards.
func withProgramName(t *testing.T, root *cobra.Command, name string) {
	t.Helper()
	prev := progname.Get()
	progname.Set(name)
	t.Cleanup(func() { progname.Set(prev) })
	applyProgramName(root, name)
}

func TestApplyProgramNameRenamesUsage(t *testing.T) {
	root := newRootCmd()
	withProgramName(t, root, "cinc-ng")

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("--help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "cinc-ng [command]") {
		t.Errorf("usage does not follow the invoked name\ngot:\n%s", out)
	}
	if strings.Contains(out, "\n  cinc [command]") {
		t.Errorf("usage still shows the compiled-in name\ngot:\n%s", out)
	}
}

// Cobra prints Example verbatim, so a rename that stops at root.Use leaves
// every help page advertising a command the user does not have.
func TestApplyProgramNameRewritesExamplesAcrossTheTree(t *testing.T) {
	root := newRootCmd()
	withProgramName(t, root, "cinc-ng")

	var stale []string
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		for _, text := range []string{cmd.Short, cmd.Long, cmd.Example} {
			if strings.Contains(text, "cinc ") {
				stale = append(stale, cmd.CommandPath())
				break
			}
		}
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(root)

	if len(stale) > 0 {
		t.Errorf("%d commands still name the canonical binary in their help text: %v", len(stale), stale)
	}
}

func TestApplyProgramNameLeavesCanonicalTreeAlone(t *testing.T) {
	// gendocs builds the tree directly and must keep the canonical name,
	// so the generated reference does not churn per-builder.
	root := newRootCmd()
	applyProgramName(root, "cinc")

	if root.Use != "cinc" {
		t.Errorf("root.Use = %q, want the canonical name untouched", root.Use)
	}
}

func TestMissingCredentialsErrorFollowsProgramName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	swapTTY(t, false)
	prev := progname.Get()
	progname.Set("cinc-ng")
	t.Cleanup(func() { progname.Set(prev) })

	stderr := new(bytes.Buffer)
	c := fakeCmd("", "", "", stderr)
	_, err := resolveProfile(c)
	if err == nil || !strings.Contains(err.Error(), "cinc-ng config create") {
		t.Errorf("expected the error to point at `cinc-ng config create`, got: %v", err)
	}
}

func TestVersionFollowsProgramName(t *testing.T) {
	prev := progname.Get()
	progname.Set("cinc-ng")
	t.Cleanup(func() { progname.Set(prev) })

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("version failed: %v", err)
	}
	if !strings.HasPrefix(buf.String(), "cinc-ng ") {
		t.Errorf("version output should lead with the invoked name\ngot:\n%s", buf.String())
	}
}
