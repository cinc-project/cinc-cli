package explore

import (
	"context"
	"errors"
	"sync"

	cinc "github.com/cinc-project/cinc-api"
)

// adminsCache memoizes the org's admins group for one explore session, so
// user summaries (which run as concurrent tea.Cmds, one per highlighted
// user) share a single fetch instead of each issuing their own.
//
// registry builds one cache and hands it to both the Users and Groups kinds;
// the model calls registry once, so the cache lives as long as the session.
// The memo is keyed by client: picking another profile hands the kinds a
// new *cinc.Client, which misses and fetches that org's group afresh.
//
// Invalidation: the Groups kind drops the memo after it saves, creates, or
// deletes the admins group, and the Users kind after it deletes a user, so
// a membership change made in the explorer shows up in the next summary.
// Changes made outside the explorer show up only once a profile is picked
// again (which builds a new client) or the explorer restarts; that
// staleness is the price of not refetching per user.
//
// Errors: a 403 or 404 means the signed-in user can't read the group or the
// org has none, which asking again won't change, so it is cached and every
// summary reports Unknown. Any other failure (network, 5xx, a cancelled
// context) is treated as transient and not cached; the next summary retries.
//
// The mutex is held across the fetch on purpose: concurrent summaries wait
// for the first one's answer rather than racing to fetch it themselves.
// A nil *adminsCache is valid and fetches on every call.
type adminsCache struct {
	mu      sync.Mutex
	client  *cinc.Client // the client the memo below was fetched with
	members []string
	known   bool // false when the cached lookup failed permanently
}

func newAdminsCache() *adminsCache { return &adminsCache{} }

// lookup returns the admins group's direct users for c's org. ok is false
// when the group couldn't be read, in which case the caller reports Unknown.
func (a *adminsCache) lookup(ctx context.Context, c *cinc.Client) (members []string, ok bool) {
	if a == nil {
		members, err := fetchAdmins(ctx, c)
		return members, err == nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == c {
		return a.members, a.known
	}
	members, err := fetchAdmins(ctx, c)
	switch {
	case err == nil:
		a.client, a.members, a.known = c, members, true
	case errors.Is(err, cinc.ErrForbidden), errors.Is(err, cinc.ErrNotFound):
		a.client, a.members, a.known = c, nil, false
	default:
		return nil, false
	}
	return a.members, a.known
}

// invalidate drops the memo for c, so the next lookup refetches. A memo for
// a different client is left alone.
func (a *adminsCache) invalidate(c *cinc.Client) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == c {
		a.client, a.members, a.known = nil, nil, false
	}
}

// fetchAdmins reads the admins group's direct users, uncached.
func fetchAdmins(ctx context.Context, c *cinc.Client) ([]string, error) {
	group, _, err := c.Groups.Get(ctx, adminsGroup)
	if err != nil {
		return nil, err
	}
	return group.Users, nil
}
