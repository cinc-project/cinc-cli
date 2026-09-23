//go:build !unix

package suite

// execWithoutTerminal needs a new session to shed the controlling
// terminal, which only unix offers; elsewhere the case skips.
func execWithoutTerminal(c *cli, _ ...string) result {
	c.t.Helper()
	c.t.Skip("detaching from the terminal needs a unix session")
	return result{}
}
