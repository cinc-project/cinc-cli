package resolver

import (
	cinc "github.com/cinc-project/cinc-api"
)

// CookbookIdentifier holds the two content-addressed identifiers chef computes
// for a Policyfile cookbook lock.
type CookbookIdentifier struct {
	// Content is the SHA1 hex of the cookbook fingerprint (chef's
	// CookbookProfiler::Identifiers#content_identifier).
	Content string
	// DottedDecimal is the X.Y.Z reinterpretation of the SHA1 used as the
	// cookbook_artifacts/<name>/<id> slug for Chef Infra Server 11.x
	// compatibility (#dotted_decimal_identifier).
	DottedDecimal string
}

// ComputeIdentifier computes the identifiers of the cookbook in cookbookDir
// at version (the version the resolver read from its metadata). cinc-api
// picks the files, exactly as an upload of the cookbook sends them, and
// fingerprints them the way chef-cli does. version is passed rather than
// re-read so that a metadata.rb computing its version, which the Ruby engine
// has already evaluated, is not refused.
func ComputeIdentifier(cookbookDir, version string) (CookbookIdentifier, error) {
	cb, err := cinc.LocalCookbookFromDir(cookbookDir, version)
	if err != nil {
		return CookbookIdentifier{}, err
	}
	content, dotted := cb.Identifiers()
	return CookbookIdentifier{Content: content, DottedDecimal: dotted}, nil
}
