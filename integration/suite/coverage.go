package suite

// exempt lists leaf commands no suite case runs, each with the reason. The
// coverage guard (coverage_test.go) rejects an entry without one.
var exempt = map[string]string{
	"supermarket download": "talks to a Supermarket, not a CINC Server; covered by httptest unit tests",
	"supermarket explore":  "talks to a Supermarket, not a CINC Server; covered by httptest unit tests",
	"supermarket install":  "talks to a Supermarket, not a CINC Server; covered by httptest unit tests",
	"supermarket list":     "talks to a Supermarket, not a CINC Server; covered by httptest unit tests",
	"supermarket search":   "talks to a Supermarket, not a CINC Server; covered by httptest unit tests",
	"supermarket share":    "talks to a Supermarket, not a CINC Server; covered by httptest unit tests",
	"supermarket show":     "talks to a Supermarket, not a CINC Server; covered by httptest unit tests",
}
