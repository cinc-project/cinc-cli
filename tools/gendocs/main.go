// gendocs regenerates the per-command Markdown reference under
// docs/commands/ from the live cobra command tree, plus a top-level
// README.md index that links to each per-command page with a one-line
// summary pulled from each command's Short.
//
// Usage: go run ./tools/gendocs [output-dir]
//
// The default output directory is docs/commands relative to the
// current working directory. The tool wipes the target directory
// before writing so deleted commands do not linger as stale files.
//
// To keep generated output reproducible the cobra "auto-generated"
// timestamp footer is suppressed.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	"github.com/cinc-project/cinc-cli/apps/cinc/cmd"
)

func main() {
	outDir := "docs/commands"
	if len(os.Args) > 1 {
		outDir = os.Args[1]
	}

	if err := os.RemoveAll(outDir); err != nil {
		exit("clear %s: %v", outDir, err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		exit("create %s: %v", outDir, err)
	}

	root := cmd.NewRootCmd()
	root.DisableAutoGenTag = true

	// Capture each command's Example and clear it before generation. Cobra
	// would wrap the whole Example in a single code fence; instead gendocs
	// renders examples as Markdown (prose paragraphs, with command lines turned
	// into fenced code blocks) and injects them after generation.
	examples := map[string]string{}
	captureExamples(root, examples)

	if err := doc.GenMarkdownTreeCustom(root, outDir, frontmatter, linkHandler); err != nil {
		exit("generate docs: %v", err)
	}

	for path, ex := range examples {
		file := filepath.Join(outDir, strings.ReplaceAll(path, " ", "_")+".md")
		if err := injectExamples(file, ex); err != nil {
			exit("inject examples into %s: %v", file, err)
		}
	}

	if err := writeIndex(root, outDir); err != nil {
		exit("write index: %v", err)
	}

	abs, _ := filepath.Abs(outDir)
	fmt.Printf("Wrote %s\n", abs)
}

// captureExamples records each command's authored Example keyed by command
// path, clearing it so cobra does not render its own fenced Examples block.
func captureExamples(c *cobra.Command, into map[string]string) {
	if strings.TrimSpace(c.Example) != "" {
		into[c.CommandPath()] = c.Example
		c.Example = ""
	}
	for _, sub := range c.Commands() {
		captureExamples(sub, into)
	}
}

// renderExamples turns an authored Example into a Markdown "Examples" section.
// Examples are authored as plain text with no indentation: lines that begin
// with the binary name are grouped into fenced code blocks, and every other
// non-blank line is prose explaining what the following command does.
func renderExamples(raw string) string {
	var b strings.Builder
	b.WriteString("### Examples\n\n")
	inCode := false
	closeCode := func() {
		if inCode {
			b.WriteString("```\n\n")
			inCode = false
		}
	}
	for line := range strings.SplitSeq(raw, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == "":
			closeCode()
		case t == "cinc" || strings.HasPrefix(t, "cinc "):
			if !inCode {
				b.WriteString("```bash\n")
				inCode = true
			}
			b.WriteString(t + "\n")
		default:
			closeCode()
			b.WriteString(t + "\n\n")
		}
	}
	closeCode()
	return b.String()
}

// injectExamples inserts the rendered Examples section into a generated command
// page, immediately before the first "### Options" / "### SEE ALSO" heading —
// where cobra would otherwise have placed its own Examples block.
func injectExamples(file, raw string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	content := string(data)
	idx := -1
	for _, marker := range []string{"### Options", "### SEE ALSO"} {
		if i := strings.Index(content, marker); i >= 0 && (idx == -1 || i < idx) {
			idx = i
		}
	}
	section := renderExamples(raw)
	if idx == -1 {
		content += "\n" + section
	} else {
		content = content[:idx] + section + content[idx:]
	}
	return os.WriteFile(file, []byte(content), 0o644)
}

// frontmatter returns an empty prefix; we keep the generated output as
// plain Markdown without any static-site frontmatter.
func frontmatter(string) string { return "" }

// linkHandler rewrites the cross-references cobra emits in the "SEE
// ALSO" section so they resolve relative to docs/commands/ rather than
// pointing at the parent docs/ directory.
func linkHandler(name string) string {
	base := strings.TrimSuffix(name, ".md")
	return base + ".md"
}

// writeIndex builds docs/commands/README.md: a flat alphabetical list
// of every command in the tree, each line linking to that command's
// generated page and showing its cobra Short summary.
func writeIndex(root *cobra.Command, outDir string) error {
	type entry struct {
		path     string // e.g. "cinc client create"
		filename string // e.g. "cinc_client_create.md"
		short    string
	}
	var entries []entry
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Hidden || !c.IsAvailableCommand() && c != root {
			return
		}
		entries = append(entries, entry{
			path:     c.CommandPath(),
			filename: strings.ReplaceAll(c.CommandPath(), " ", "_") + ".md",
			short:    c.Short,
		})
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })

	var b strings.Builder
	fmt.Fprintln(&b, "# Command reference")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Every command in the `cinc` tree, with a one-line summary and a")
	fmt.Fprintln(&b, "link to the full reference page. This file is regenerated by")
	fmt.Fprintln(&b, "`make docs` from the live cobra command tree; do not edit by hand.")
	fmt.Fprintln(&b)
	for _, e := range entries {
		fmt.Fprintf(&b, "- [`%s`](%s): %s\n", e.path, e.filename, e.short)
	}

	return os.WriteFile(filepath.Join(outDir, "README.md"), []byte(b.String()), 0o644)
}

func exit(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "gendocs: "+format+"\n", a...)
	os.Exit(1)
}
