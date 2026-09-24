package suite

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

var searchFamily = family{cases: []testCase{
	{"search/node", []string{"search"}, testSearchNode},
	{"search/node-attributes", []string{"search"}, testSearchNodeAttributes},
	{"search/partial", []string{"search"}, testSearchPartial},
	{"search/role", []string{"search"}, testSearchRole},
	{"search/escaped-query", []string{"search"}, testSearchEscapedQuery},
	{"search/environment", []string{"search"}, testSearchEnvironment},
	{"search/client", []string{"search"}, testSearchClient},
	{"search/databag", []string{"search"}, testSearchDataBag},
	{"search/databag-partial", []string{"search"}, testSearchDataBagPartial},
	{"search/paging", []string{"search"}, testSearchPaging},
	{"search/past-the-end", []string{"search"}, testSearchPastTheEnd},
	{"search/no-match", []string{"search"}, testSearchNoMatch},
	{"search/negative-paging", []string{"search"}, testSearchNegativePaging},
	{"search/bad-query", []string{"search"}, testSearchBadQuery},
	{"search/missing-index", []string{"search"}, testSearchMissingIndex},
	{"explore/help", []string{"explore"}, testExploreHelp},
	{"explore/requires-tty", []string{"explore"}, testExploreRequiresTTY},
	{"explore/browse", []string{"explore"}, testExploreBrowse},
	{"explore/search", []string{"explore"}, testExploreSearch},
	{"explore/delete", []string{"explore"}, testExploreDelete},
	{"explore/databag-search", []string{"explore"}, testExploreDataBagSearch},
}}

// searchJSON is the shape of `cinc search --format json`.
type searchJSON struct {
	Total int               `json:"total"`
	Start int               `json:"start"`
	Rows  []json.RawMessage `json:"rows"`
}

// search runs `cinc search --format json` and decodes the result, returning
// an error rather than failing so it can sit inside eventually.
func (c *cli) searchQuery(args ...string) (searchJSON, error) {
	c.t.Helper()
	r := c.exec(runOpts{}, append(append([]string{"search"}, args...), "--format", "json")...)
	if r.exitCode != 0 {
		return searchJSON{}, fmt.Errorf("search failed: %s", r)
	}
	var out searchJSON
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		return searchJSON{}, fmt.Errorf("decode %q: %v", r.stdout, err)
	}
	return out, nil
}

// rowNames returns each row's "name" field, sorted.
func (s searchJSON) rowNames() []string {
	var names []string
	for _, raw := range s.Rows {
		var o struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(raw, &o)
		names = append(names, o.Name)
	}
	slices.Sort(names)
	return names
}

// awaitSearch waits until the query returns exactly want rows, and returns
// that result.
func (c *cli) awaitSearch(want int, args ...string) searchJSON {
	c.t.Helper()
	var got searchJSON
	eventually(c.t, searchTimeout, func() error {
		var err error
		if got, err = c.searchQuery(args...); err != nil {
			return err
		}
		if got.Total != want || len(got.Rows) != want {
			return fmt.Errorf("search %v: total %d with %d rows, want %d", args, got.Total, len(got.Rows), want)
		}
		return nil
	})
	return got
}

// wantLines fails the case unless out, split into lines, is exactly want.
func wantLines(t *testing.T, what, out string, want ...string) {
	t.Helper()
	wantSlice(t, what, strings.Split(strings.TrimSuffix(out, "\n"), "\n"), want)
}

// wantContains fails the case unless out contains every one of want.
func wantContains(t *testing.T, what, out string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("%s missing %q:\n%s", what, w, out)
		}
	}
}

func testSearchNode(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "node")
	createNode(c, name, "--environment", "_default", "--run-list", "recipe[base]")
	query := "name:" + name

	got := c.awaitSearch(1, "node", query)
	wantSlice(t, "json names", got.rowNames(), []string{name})
	wantEqual(t, "json start", got.Start, 0)
	var n map[string]any
	if err := json.Unmarshal(got.Rows[0], &n); err != nil {
		t.Fatal(err)
	}
	// A full search row is the whole node.
	wantEqual(t, "row chef_environment", fmt.Sprint(n["chef_environment"]), "_default")
	wantEqual(t, "row run_list", fmt.Sprint(n["run_list"]), "[recipe[base]]")

	wantLines(t, "search -i", c.run("search", "node", query, "-i"), name)
	wantLines(t, "search --id-only", c.run("search", "node", query, "--id-only"), name)

	table := c.run("search", "node", query)
	wantContains(t, "search table", table, "NAME", "ENVIRONMENT", "PLATFORM", "RUN LIST", name, "recipe[base]", "1 node matched")
}

// testSearchNodeAttributes finds nodes by their attributes: a nested key is
// searchable both by its leaf name and by its full underscore-joined path,
// across every precedence level, as erchef indexes it.
func testSearchNodeAttributes(t *testing.T, _ Target, c *cli) {
	token := randomHex(t, 6)
	name := uniqueName(t, "node")
	file := writeJSON(t, map[string]any{
		"name":             name,
		"chef_environment": "_default",
		"normal":           map[string]any{"cinc_app": map[string]any{"tier": "n" + token}},
		"default":          map[string]any{"cinc_dflt": "d" + token},
		"override":         map[string]any{"cinc_ovr": "o" + token},
		"automatic":        map[string]any{"cinc_auto": "a" + token},
	})
	c.run("node", "create", name, "--file", file)
	c.cleanup("node", "delete", name)

	for _, q := range []string{
		"cinc_app_tier:n" + token,
		"tier:n" + token,
		"cinc_dflt:d" + token,
		"cinc_ovr:o" + token,
		"cinc_auto:a" + token,
		"cinc_app_tier:n" + token + " AND name:" + name,
	} {
		got := c.awaitSearch(1, "node", q)
		wantSlice(t, "matches for "+q, got.rowNames(), []string{name})
	}
	if got, err := c.searchQuery("node", "cinc_app_tier:x"+token); err != nil || got.Total != 0 {
		t.Errorf("a non-matching value should find nothing: %+v %v", got, err)
	}
}

// testSearchPartial projects attributes with -a: the JSON rows carry just
// the requested keys under "data", and the table turns them into columns.
func testSearchPartial(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "node")
	file := writeJSON(t, map[string]any{
		"name":             name,
		"chef_environment": "_default",
		"normal":           map[string]any{"app": map[string]any{"port": 8080, "tier": "web"}},
	})
	c.run("node", "create", name, "--file", file)
	c.cleanup("node", "delete", name)
	query := "name:" + name

	got := c.awaitSearch(1, "node", query, "-a", "app.port", "--attribute", "app.tier")
	var row struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(got.Rows[0], &row); err != nil {
		t.Fatal(err)
	}
	wantEqual(t, "data.name", fmt.Sprint(row.Data["name"]), name)
	wantEqual(t, "data[app.port]", fmt.Sprint(row.Data["app.port"]), "8080")
	wantEqual(t, "data[app.tier]", fmt.Sprint(row.Data["app.tier"]), "web")
	if _, ok := row.Data["chef_environment"]; ok {
		t.Errorf("partial row carries unrequested chef_environment: %v", row.Data)
	}

	table := c.run("search", "node", query, "-a", "app.port")
	wantContains(t, "partial table", table, "NAME", "APP.PORT", name, "8080", "1 node matched")
	wantLines(t, "partial -i", c.run("search", "node", query, "-a", "app.port", "-i"), name)
}

func testSearchRole(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "role")
	createRoleFromFile(c, name, map[string]any{
		"name":        name,
		"description": "searchable role",
		"run_list":    []string{"recipe[base]"},
	})

	got := c.awaitSearch(1, "role", "name:"+name)
	wantSlice(t, "json names", got.rowNames(), []string{name})
	var r map[string]any
	if err := json.Unmarshal(got.Rows[0], &r); err != nil {
		t.Fatal(err)
	}
	wantEqual(t, "row description", fmt.Sprint(r["description"]), "searchable role")

	wantLines(t, "search -i", c.run("search", "role", "name:"+name, "-i"), name)
	table := c.run("search", "role", "name:"+name)
	wantContains(t, "role table", table, "NAME", "DESCRIPTION", "RUN LIST", name, "searchable role", "recipe[base]", "1 role matched")
}

// testSearchEscapedQuery searches a run list the way knife's documentation
// does, with the brackets backslash-escaped: erchef reads recipe\[base\] as
// the term recipe[base]. (erchef answers 400 to the quoted form
// run_list:"recipe[base]", so escaping is the one form that works.)
func testSearchEscapedQuery(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "role")
	createRole(c, name)
	c.run("role", "edit", name, "--file", writeJSON(t, map[string]any{"run_list": []string{"recipe[base]"}}))
	got := c.awaitSearch(1, "role", "name:"+name+` AND run_list:recipe\[base\]`)
	wantSlice(t, "json names", got.rowNames(), []string{name})
}

func testSearchEnvironment(t *testing.T, _ Target, c *cli) {
	name := uniqueName(t, "env")
	createEnvironmentFromFile(c, name, map[string]any{
		"name":              name,
		"description":       "searchable env",
		"cookbook_versions": map[string]string{"apache2": "~> 1.2.0", "nginx": "= 2.0.0"},
	})

	got := c.awaitSearch(1, "environment", "name:"+name)
	wantSlice(t, "json names", got.rowNames(), []string{name})
	wantLines(t, "search -i", c.run("search", "environment", "name:"+name, "-i"), name)
	table := c.run("search", "environment", "name:"+name)
	wantContains(t, "environment table", table, "NAME", "DESCRIPTION", "COOKBOOKS", name, "searchable env", "1 environment matched")
	// COOKBOOKS counts the constraints.
	for _, line := range strings.Split(table, "\n") {
		if strings.HasPrefix(line, name) && !strings.HasSuffix(strings.TrimSpace(line), "2") {
			t.Errorf("environment row should count 2 cookbook constraints: %q", line)
		}
	}
}

func testSearchClient(t *testing.T, _ Target, c *cli) {
	name := addClientActor(c, "robot")

	got := c.awaitSearch(1, "client", "name:"+name)
	wantSlice(t, "json names", got.rowNames(), []string{name})
	wantLines(t, "search -i", c.run("search", "client", "name:"+name, "-i"), name)
	table := c.run("search", "client", "name:"+name)
	wantContains(t, "client table", table, "NAME", "VALIDATOR", name, "false", "1 client matched")
}

// createSearchBag creates a data bag holding the given items, keyed by id,
// and registers its cleanup.
func createSearchBag(c *cli, items map[string]map[string]any) string {
	c.t.Helper()
	bag := uniqueName(c.t, "bag")
	c.run("databag", "create", bag)
	c.cleanup("databag", "delete", bag)
	for id, item := range items {
		item["id"] = id
		c.run("databag", "item", "create", bag, id, "--file", writeJSON(c.t, item))
	}
	return bag
}

// testSearchDataBag searches a data bag's items. erchef returns each item
// wrapped (name "data_bag_item_<bag>_<id>", the item under raw_data); the
// CLI must still identify rows by the item's id and summarize the item's own
// keys, not the wrapper's.
func testSearchDataBag(t *testing.T, _ Target, c *cli) {
	bag := createSearchBag(c, map[string]map[string]any{
		"alice": {"role": "admin", "shell": "zsh"},
		"bob":   {"role": "editor", "shell": "bash"},
	})

	c.awaitSearch(2, bag, "*:*")
	got := c.awaitSearch(1, bag, "role:admin")
	var row map[string]any
	if err := json.Unmarshal(got.Rows[0], &row); err != nil {
		t.Fatal(err)
	}
	item, _ := row["raw_data"].(map[string]any)
	if item == nil {
		item = row
	}
	wantEqual(t, "matched item id", fmt.Sprint(item["id"]), "alice")

	wantLines(t, "search -i", c.run("search", bag, "*:*", "-i"), "alice", "bob")
	table := c.run("search", bag, "role:admin")
	wantContains(t, "data bag table", table, "ID", "KEYS", "alice", "role, shell", "1 result matched")
	if strings.Contains(table, "data_bag_item_") || strings.Contains(table, "raw_data") {
		t.Errorf("data bag table shows the search wrapper rather than the item:\n%s", table)
	}
}

// testSearchDataBagPartial projects a data bag item's own keys.
func testSearchDataBagPartial(t *testing.T, _ Target, c *cli) {
	bag := createSearchBag(c, map[string]map[string]any{
		"alice": {"role": "admin", "shell": "zsh"},
	})
	got := c.awaitSearch(1, bag, "id:alice", "-a", "shell")
	var row struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(got.Rows[0], &row); err != nil {
		t.Fatal(err)
	}
	wantEqual(t, "data.id", fmt.Sprint(row.Data["id"]), "alice")
	wantEqual(t, "data.shell", fmt.Sprint(row.Data["shell"]), "zsh")

	wantLines(t, "partial -i", c.run("search", bag, "id:alice", "-a", "shell", "-i"), "alice")
	wantContains(t, "partial table", c.run("search", bag, "id:alice", "-a", "shell"), "NAME", "SHELL", "alice", "zsh")
}

// createPagingNodes creates n nodes sharing a unique attribute value and
// returns their names (sorted) and the query that matches exactly them.
func createPagingNodes(t *testing.T, c *cli, n int) ([]string, string) {
	t.Helper()
	token := randomHex(t, 6)
	var names []string
	for range n {
		name := uniqueName(t, "node")
		file := writeJSON(t, map[string]any{
			"name":             name,
			"chef_environment": "_default",
			"normal":           map[string]any{"cinc_page": token},
		})
		c.run("node", "create", name, "--file", file)
		c.cleanup("node", "delete", name)
		names = append(names, name)
	}
	slices.Sort(names)
	query := "cinc_page:" + token
	c.awaitSearch(n, "node", query)
	return names, query
}

// testSearchPaging walks one result set a page at a time with --rows and
// --start: every page reports the full total, and the pages together return
// each match exactly once.
func testSearchPaging(t *testing.T, _ Target, c *cli) {
	names, query := createPagingNodes(t, c, 5)

	var seen []string
	for _, page := range []struct{ start, rows, want int }{{0, 2, 2}, {2, 2, 2}, {4, 2, 1}} {
		got, err := c.searchQuery("node", query, "--rows", fmt.Sprint(page.rows), "--start", fmt.Sprint(page.start))
		if err != nil {
			t.Fatal(err)
		}
		wantEqual(t, fmt.Sprintf("total at start %d", page.start), got.Total, 5)
		wantEqual(t, fmt.Sprintf("start at start %d", page.start), got.Start, page.start)
		wantEqual(t, fmt.Sprintf("rows at start %d", page.start), len(got.Rows), page.want)
		seen = append(seen, got.rowNames()...)
	}
	slices.Sort(seen)
	wantSlice(t, "every page together", seen, names)

	// Without --rows the CLI fetches every match from --start on.
	got, err := c.searchQuery("node", query, "--start", "3")
	if err != nil {
		t.Fatal(err)
	}
	wantEqual(t, "rows from start 3", len(got.Rows), 2)
	wantEqual(t, "total from start 3", got.Total, 5)

	table := c.run("search", "node", query, "--rows", "2")
	wantContains(t, "paged table", table, "Showing 2 of 5 nodes")
	if ids := strings.Fields(c.run("search", "node", query, "--rows", "3", "-i")); len(ids) != 3 {
		t.Errorf("search --rows 3 -i printed %d names, want 3: %v", len(ids), ids)
	}
	wantLines(t, "all ids", c.run("search", "node", query, "-i"), names...)
}

// testSearchPastTheEnd asks for a page beyond the last match: no rows, but
// the total still counts every match.
func testSearchPastTheEnd(t *testing.T, _ Target, c *cli) {
	_, query := createPagingNodes(t, c, 2)
	got, err := c.searchQuery("node", query, "--rows", "2", "--start", "10")
	if err != nil {
		t.Fatal(err)
	}
	wantEqual(t, "total", got.Total, 2)
	wantEqual(t, "rows", len(got.Rows), 0)
}

// testSearchNoMatch checks the empty result in each output form. The JSON
// rows are an empty array, never null, so scripts can iterate them.
func testSearchNoMatch(t *testing.T, _ Target, c *cli) {
	query := "name:" + uniqueName(t, "ghost")
	wantEqual(t, "table", c.run("search", "node", query), "No nodes matched.\n")
	wantEqual(t, "role table", c.run("search", "role", query), "No roles matched.\n")
	wantEqual(t, "-i", c.run("search", "node", query, "-i"), "")
	for _, extra := range [][]string{nil, {"--rows", "5"}} {
		out := c.run(append([]string{"search", "node", query, "--format", "json"}, extra...)...)
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(out), &raw); err != nil {
			t.Fatalf("search %v --format json: %v\n%s", extra, err, out)
		}
		wantEqual(t, fmt.Sprintf("total %v", extra), string(raw["total"]), "0")
		wantEqual(t, fmt.Sprintf("rows %v", extra), string(raw["rows"]), "[]")
	}
}

// testSearchNegativePaging checks that a negative --rows or --start is
// refused before any request is made, rather than being passed to the server
// or silently read as "everything".
func testSearchNegativePaging(t *testing.T, _ Target, c *cli) {
	wantStderr(t, c.fail("search", "node", "*:*", "--rows", "-1"), "--rows")
	wantStderr(t, c.fail("search", "node", "*:*", "--start", "-1"), "--start")
}

// testSearchBadQuery checks that a query erchef cannot parse is reported
// back as a 400.
func testSearchBadQuery(t *testing.T, _ Target, c *cli) {
	for _, q := range []string{"name:(", "AND AND", "name:[a TO"} {
		wantBadRequest(t, c.fail("search", "node", q))
	}
}

// testSearchMissingIndex searches an index that is neither a built-in one
// nor a data bag: erchef answers 404.
func testSearchMissingIndex(t *testing.T, _ Target, c *cli) {
	ghost := uniqueName(t, "ghostbag")
	wantNotFound(t, c.fail("search", ghost, "*:*"))
	wantNotFound(t, c.fail("search", ghost, "*:*", "--format", "json"))
}
