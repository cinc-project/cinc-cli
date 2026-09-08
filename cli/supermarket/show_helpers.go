package supermarket

import (
	"sort"

	sm "github.com/cinc-project/cinc-supermarket-api"
)

// VersionFromURL extracts the version segment from a Supermarket version URL
// like https://supermarket.chef.io/api/v1/cookbooks/nginx/versions/12_0_4
// returning "12.0.4". It delegates to the cinc-supermarket SDK, which owns the
// Supermarket version-encoding rules.
func VersionFromURL(s string) string {
	return sm.VersionFromURL(s)
}

// VersionListFromURLs decodes a slice of Supermarket version URLs and
// returns the version strings sorted newest-first.
func VersionListFromURLs(urls []string) []string {
	if len(urls) == 0 {
		return nil
	}
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		if v := sm.VersionFromURL(u); v != "" {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return sm.CompareVersions(out[i], out[j]) > 0
	})
	return out
}
