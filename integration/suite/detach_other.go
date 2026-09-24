//go:build !unix

package suite

// execDetached needs a new session without a controlling terminal, which
// only Unix offers; the cases using it skip elsewhere.
func (c *cli) execDetached(args ...string) result {
	c.t.Helper()
	c.t.Skip("running without a controlling terminal needs Unix")
	return result{}
}
