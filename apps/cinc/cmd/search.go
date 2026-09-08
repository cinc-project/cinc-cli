package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"

	cinc "github.com/cinc-project/cinc-api"
	"github.com/spf13/cobra"

	"github.com/cinc-project/cinc-cli/cli/printer"
)

// searchResult is the structured output of a search, emitted verbatim by
// `--format json`.
type searchResult struct {
	Total int               `json:"total"`
	Start int               `json:"start"`
	Rows  []json.RawMessage `json:"rows"`
}

// newSearchCmd builds the global `cinc search <index> <query>` command. It is
// a verb-first global utility (not `cinc node search`): the index selects what
// to search — node, role, environment, client, or any data bag name.
func newSearchCmd() *cobra.Command {
	var (
		attrs   []string
		idOnly  bool
		rowsCap int
		start   int
	)
	cmd := &cobra.Command{
		Use:   "search <index> <query>",
		Short: "Search the Cinc Server",
		Example: `Search an index: node, role, environment, client, or a data bag name.
cinc search node 'role:web'`,
		Long: "Search the Cinc Server's index for objects matching a Solr/Lucene query.\n\n" +
			"The index is one of node, role, environment, client, or a data bag name.\n" +
			"By default matches render as an aligned table; -a projects specific\n" +
			"attributes into columns, -i prints just the names, and --format json\n" +
			"emits the full result.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			index, query := args[0], args[1]
			format, err := resolveFormat(cmd)
			if err != nil {
				return err
			}
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}

			var opts []cinc.SearchOption
			if partial := partialProjection(attrs); partial != nil {
				opts = append(opts, cinc.WithPartial(partial))
			}
			result, err := runSearch(cmd.Context(), c, index, query, rowsCap, start, opts)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			switch {
			case format == printer.FormatJSON:
				return printer.New(out, format).Value(result)
			case idOnly:
				return printer.New(out, format).List(searchIDs(result.Rows, len(attrs) > 0))
			default:
				renderSearchTable(out, index, attrs, result)
				return nil
			}
		},
	}
	cmd.Flags().StringArrayVarP(&attrs, "attribute", "a", nil, "return only this attribute (repeatable); the requested attributes become the columns")
	cmd.Flags().BoolVarP(&idOnly, "id-only", "i", false, "print only the matching object names/ids, one per line")
	cmd.Flags().IntVar(&rowsCap, "rows", 0, "maximum number of results to return (0 returns all matches)")
	cmd.Flags().IntVar(&start, "start", 0, "offset of the first result to return")
	return cmd
}

// partialProjection turns the -a attribute paths into a partial-search
// projection, always including name and id so a row stays identifiable.
func partialProjection(attrs []string) map[string][]string {
	if len(attrs) == 0 {
		return nil
	}
	p := map[string][]string{"name": {"name"}, "id": {"id"}}
	for _, a := range attrs {
		p[a] = strings.Split(a, ".")
	}
	return p
}

// runSearch fetches results. With an explicit --rows or --start it returns that
// single page (reporting the server's total); otherwise it pages through every
// match so the default output is never silently truncated.
func runSearch(ctx context.Context, c *cinc.Client, index, query string, rowsCap, start int, opts []cinc.SearchOption) (searchResult, error) {
	if rowsCap > 0 || start > 0 {
		pageOpts := append([]cinc.SearchOption{cinc.WithStart(start)}, opts...)
		if rowsCap > 0 {
			pageOpts = append(pageOpts, cinc.WithRows(rowsCap))
		}
		res, _, err := c.Search.Query(ctx, index, query, pageOpts...)
		if err != nil {
			return searchResult{}, err
		}
		return searchResult{Total: res.Total, Start: res.Start, Rows: res.Rows}, nil
	}
	all, err := c.Search.SearchAll(ctx, index, query, opts...)
	if err != nil {
		return searchResult{}, err
	}
	return searchResult{Total: len(all), Rows: all}, nil
}

// objectMap normalizes a search row into the map to read fields from. Partial
// search wraps the projection under a "data" key; full rows are the object.
func objectMap(raw json.RawMessage, partial bool) map[string]any {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return map[string]any{}
	}
	if partial {
		if data, ok := m["data"].(map[string]any); ok {
			return data
		}
		return map[string]any{}
	}
	return m
}

// searchIDs returns the sorted identities (name, falling back to id) of rows.
func searchIDs(rows []json.RawMessage, partial bool) []string {
	ids := make([]string, 0, len(rows))
	for _, raw := range rows {
		ids = append(ids, rowIdentity(objectMap(raw, partial)))
	}
	slices.Sort(ids)
	return ids
}

// rowIdentity returns an object's name, or its id when there is no name (data
// bag items are keyed by id).
func rowIdentity(obj map[string]any) string {
	if name := cellString(obj["name"]); name != "" {
		return name
	}
	return cellString(obj["id"])
}

// searchColumn is one rendered column: a header and how to pull its cell.
type searchColumn struct {
	header string
	get    func(obj map[string]any) string
}

// renderSearchTable prints the aligned table and a count footer.
func renderSearchTable(out interface{ Write([]byte) (int, error) }, index string, attrs []string, result searchResult) {
	partial := len(attrs) > 0
	columns := searchColumns(index, attrs)

	type line struct {
		id    string
		cells []string
	}
	lines := make([]line, 0, len(result.Rows))
	for _, raw := range result.Rows {
		obj := objectMap(raw, partial)
		cells := make([]string, len(columns))
		for i, col := range columns {
			cells[i] = col.get(obj)
		}
		lines = append(lines, line{id: rowIdentity(obj), cells: cells})
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].id < lines[j].id })

	noun := indexNoun(index)
	if len(lines) == 0 {
		fmt.Fprintf(out, "No %s matched.\n", plural(noun))
		return
	}

	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	headers := make([]string, len(columns))
	for i, col := range columns {
		headers[i] = col.header
	}
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, l := range lines {
		fmt.Fprintln(tw, strings.Join(l.cells, "\t"))
	}
	_ = tw.Flush()

	fmt.Fprintln(out)
	if shown := len(lines); shown < result.Total {
		fmt.Fprintf(out, "Showing %d of %d %s\n", shown, result.Total, plural(noun))
	} else {
		fmt.Fprintf(out, "%d %s matched\n", shown, nounCount(noun, shown))
	}
}

// searchColumns picks the columns for an index. With -a the columns are name
// plus the requested attributes; otherwise a curated set per known index.
func searchColumns(index string, attrs []string) []searchColumn {
	if len(attrs) > 0 {
		cols := []searchColumn{{header: "NAME", get: func(o map[string]any) string { return rowIdentity(o) }}}
		for _, a := range attrs {
			cols = append(cols, searchColumn{header: strings.ToUpper(a), get: func(o map[string]any) string { return cellString(o[a]) }})
		}
		return cols
	}

	name := searchColumn{header: "NAME", get: func(o map[string]any) string { return rowIdentity(o) }}
	switch index {
	case "node":
		return []searchColumn{
			name,
			{header: "ENVIRONMENT", get: func(o map[string]any) string { return cellString(o["chef_environment"]) }},
			{header: "PLATFORM", get: nodePlatform},
			{header: "RUN LIST", get: func(o map[string]any) string { return listCell(o["run_list"]) }},
		}
	case "role":
		return []searchColumn{
			name,
			{header: "DESCRIPTION", get: func(o map[string]any) string { return cellString(o["description"]) }},
			{header: "RUN LIST", get: func(o map[string]any) string { return listCell(o["run_list"]) }},
		}
	case "environment":
		return []searchColumn{
			name,
			{header: "DESCRIPTION", get: func(o map[string]any) string { return cellString(o["description"]) }},
			{header: "COOKBOOKS", get: func(o map[string]any) string { return mapCount(o["cookbook_versions"]) }},
		}
	case "client":
		return []searchColumn{
			name,
			{header: "VALIDATOR", get: func(o map[string]any) string { return boolCell(o["validator"]) }},
		}
	default:
		// Data bag items (and any unknown index) are arbitrary JSON keyed by
		// id; summarize the top-level keys.
		return []searchColumn{
			{header: "ID", get: func(o map[string]any) string { return rowIdentity(o) }},
			{header: "KEYS", get: itemKeys},
		}
	}
}

// nodePlatform renders "<platform> <version>" from a node's automatic
// attributes, or "-" when the node has not converged yet.
func nodePlatform(o map[string]any) string {
	auto, ok := o["automatic"].(map[string]any)
	if !ok {
		return "-"
	}
	platform := cellString(auto["platform"])
	if platform == "" {
		return "-"
	}
	if version := cellString(auto["platform_version"]); version != "" {
		return platform + " " + version
	}
	return platform
}

// itemKeys lists a data bag item's top-level keys (minus chef bookkeeping).
func itemKeys(o map[string]any) string {
	keys := make([]string, 0, len(o))
	for k := range o {
		switch k {
		case "id", "chef_type", "data_bag":
			continue
		}
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if len(keys) == 0 {
		return "-"
	}
	return strings.Join(keys, ", ")
}

// cellString renders a scalar attribute value for a table cell.
func cellString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	case []any:
		return listCell(t)
	case map[string]any:
		return mapCount(t)
	default:
		return fmt.Sprint(t)
	}
}

// listCell joins a run-list-style array into a comma-separated cell.
func listCell(v any) string {
	arr, ok := v.([]any)
	if !ok {
		return "-"
	}
	parts := make([]string, 0, len(arr))
	for _, e := range arr {
		parts = append(parts, cellString(e))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

// boolCell renders a boolean attribute, defaulting to false for absent values.
func boolCell(v any) string {
	if b, ok := v.(bool); ok && b {
		return "true"
	}
	return "false"
}

// mapCount renders the size of a map-valued attribute (e.g. cookbook count).
func mapCount(v any) string {
	if m, ok := v.(map[string]any); ok {
		return fmt.Sprintf("%d", len(m))
	}
	return "0"
}

// indexNoun maps a search index to a human noun for the footer.
func indexNoun(index string) string {
	switch index {
	case "node", "role", "environment", "client":
		return index
	default:
		return "result"
	}
}

func plural(noun string) string { return noun + "s" }

// nounCount returns the singular noun for one match, plural otherwise.
func nounCount(noun string, n int) string {
	if n == 1 {
		return noun
	}
	return plural(noun)
}
