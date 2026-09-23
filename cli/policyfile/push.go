package policyfile

import (
	"context"
	"fmt"

	cinc "github.com/cinc-project/cinc-api"
)

// ArtifactsToUpload counts the lock's cookbooks the server does not hold yet
// as cookbook artifacts under the identifiers the lock records. That is how
// many cookbooks cinc-api's PushRevision uploads: it skips an artifact the
// server already has, so a lock pushed to a second group uploads none.
//
// The count is read just before the push, so a concurrent push of the same
// artifacts can make it high by the ones the other push stored first.
func ArtifactsToUpload(ctx context.Context, c *cinc.Client, lock *cinc.PolicyRevision) (int, error) {
	if len(lock.CookbookLocks) == 0 {
		return 0, nil
	}
	remote, _, err := c.CookbookArtifacts.List(ctx)
	if err != nil {
		return 0, fmt.Errorf("cinc: list cookbook artifacts: %w", err)
	}
	missing := 0
	for name, cl := range lock.CookbookLocks {
		if !hasArtifact(remote[name], cl.Identifier) {
			missing++
		}
	}
	return missing, nil
}

func hasArtifact(entry cinc.CookbookArtifactListEntry, identifier string) bool {
	for _, v := range entry.Versions {
		if v.Identifier == identifier {
			return true
		}
	}
	return false
}
