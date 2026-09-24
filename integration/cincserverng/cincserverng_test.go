// Package cincserverng runs the shared integration suite against an in-memory
// cinc-server-ng on every pull request and before every release. Signature
// checks and ACL enforcement are both on, so the server verifies every request
// the cinc binary sends and refuses what its actor may not do, as erchef does.
package cincserverng

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cinc-project/cinc-server-ng/server"

	"github.com/cinc-project/cinc-cli/integration/suite"
)

const (
	org      = "cinccli"
	otherOrg = "cinccli-other"
)

// target is set by TestMain once the server is up.
var target suite.Target

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

// run starts one server for the whole package: every case gives its objects
// unique names, so they share it safely and in parallel.
func run(m *testing.M) int {
	srv, err := server.New(server.Options{
		Addr:       "127.0.0.1:0",
		Orgs:       []string{org, otherOrg},
		EnforceACL: true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "server.New: %v\n", err)
		return 1
	}
	if err := srv.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "server.Start: %v\n", err)
		return 1
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	}()

	dir, err := os.MkdirTemp("", "cinc-server-ng-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "temp dir: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)
	keyPath := filepath.Join(dir, "admin.pem")
	if err := os.WriteFile(keyPath, srv.AdminKey(), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "write admin key: %v\n", err)
		return 1
	}

	target = suite.Target{
		Name:      "cinc-server-ng",
		ServerURL: srv.URL(),
		Org:       org,
		OtherOrg:  otherOrg,
		Admin:     srv.AdminName(),
		KeyPath:   keyPath,
		// Every entry names an upstream cinc-server-ng issue and is removed
		// once that issue is fixed.
		Gaps: map[string]string{
			"databags/edit-missing":            "PUT on a missing data bag item creates it instead of returning 404: https://github.com/cinc-project/cinc-server-ng/issues/166",
			"databags/invalid-names":           "data bag names and item ids are not validated, erchef answers 400 (https://github.com/cinc-project/cinc-server-ng/issues/167), and a percent-escaped path fails signature verification (https://github.com/cinc-project/cinc-server-ng/issues/168)",
			"cookbooks/invalid-name":           "cookbook version PUT accepts a name erchef rejects: https://github.com/cinc-project/cinc-server-ng/issues/181",
			"cookbooks/escaped-name-not-found": "the signature is checked over the decoded path, so a percent-escaped path gets a 401: https://github.com/cinc-project/cinc-server-ng/issues/168",
			"users/edit-missing":               "PUT on a missing user creates it instead of returning 404: https://github.com/cinc-project/cinc-server-ng/issues/166",
			"users/create-invalid-name":        "user names are not validated against ^[a-z0-9_-]+$: https://github.com/cinc-project/cinc-server-ng/issues/176",
			"users/create-missing-fields":      "user create does not require display_name, a valid email or a password: https://github.com/cinc-project/cinc-server-ng/issues/176",
			"users/password-too-short":         "a password under 6 characters is accepted: https://github.com/cinc-project/cinc-server-ng/issues/176",
			"users/email-lowercased":           "a user's email is stored as sent, not lower-cased: https://github.com/cinc-project/cinc-server-ng/issues/176",
			"users/edit-keeps-omitted-fields":  "a user PUT replaces the record instead of merging into it: https://github.com/cinc-project/cinc-server-ng/issues/176",
			"users/delete-member":              "deleting a user leaves its org membership behind: https://github.com/cinc-project/cinc-server-ng/issues/178",
			"users/delete-invited":             "deleting a user leaves its pending invitations behind: https://github.com/cinc-project/cinc-server-ng/issues/178",
			"orgs/create-invalid-name":         "org names are not validated: https://github.com/cinc-project/cinc-server-ng/issues/177",
			"orgs/member-remove-admin":         "a member of the admins group can be removed from the org: https://github.com/cinc-project/cinc-server-ng/issues/180",
			"orgs/member-add-forbidden":        "any member may force-add a user to the org: https://github.com/cinc-project/cinc-server-ng/issues/179",
			"orgs/member-remove-forbidden":     "any member may remove another member: https://github.com/cinc-project/cinc-server-ng/issues/179",
			"orgs/invite-create-forbidden":     "any member may invite a user: https://github.com/cinc-project/cinc-server-ng/issues/179",
			"orgs/invite-rescind-forbidden":    "any member may rescind an invitation: https://github.com/cinc-project/cinc-server-ng/issues/179",
			"nodes/edit-missing":               "PUT on a missing node creates it instead of returning 404: https://github.com/cinc-project/cinc-server-ng/issues/166",

			"roles/edit-missing":                       "PUT on a missing role creates it instead of returning 404: https://github.com/cinc-project/cinc-server-ng/issues/166",
			"roles/invalid-name":                       "role create accepts names erchef rejects: https://github.com/cinc-project/cinc-server-ng/issues/184",
			"roles/invalid-run-list":                   "role create and update accept run lists erchef rejects: https://github.com/cinc-project/cinc-server-ng/issues/184",
			"environments/edit-missing":                "PUT on a missing environment creates it instead of returning 404: https://github.com/cinc-project/cinc-server-ng/issues/166",
			"environments/invalid-name":                "environment create accepts names erchef rejects: https://github.com/cinc-project/cinc-server-ng/issues/184",
			"environments/invalid-cookbook-constraint": "environment create and update accept cookbook constraints erchef rejects: https://github.com/cinc-project/cinc-server-ng/issues/184",
			"search/escaped-query":                     "search queries do not support Lucene backslash escapes: https://github.com/cinc-project/cinc-server-ng/issues/185",

			"clients/edit-missing":                "PUT on a missing client creates it instead of returning 404: https://github.com/cinc-project/cinc-server-ng/issues/166",
			"clients/create-invalid-name":         "client names are not validated: https://github.com/cinc-project/cinc-server-ng/issues/175",
			"clients/create-invalid-public-key":   "a client's public_key is not validated: https://github.com/cinc-project/cinc-server-ng/issues/175",
			"clients/reregister-new-key-signs":    "a re-created default key never authenticates: https://github.com/cinc-project/cinc-server-ng/issues/172",
			"keys/client-added-key-signs":         "keys added through the keys API never authenticate: https://github.com/cinc-project/cinc-server-ng/issues/172",
			"keys/user-added-key-signs":           "keys added through the keys API never authenticate: https://github.com/cinc-project/cinc-server-ng/issues/172",
			"keys/client-invalid-expiration":      "a key's expiration_date is not validated: https://github.com/cinc-project/cinc-server-ng/issues/175",
			"keys/client-default-already-exists":  "a second key named default is accepted: https://github.com/cinc-project/cinc-server-ng/issues/173",
			"keys/client-edit-default-expiration": "PUT on the default key ignores expiration_date: https://github.com/cinc-project/cinc-server-ng/issues/173",
			"keys/client-edit-partial":            "key PUT drops fields the body omits: https://github.com/cinc-project/cinc-server-ng/issues/163",
			"keys/client-edit-rename":             "key PUT ignores a new name: https://github.com/cinc-project/cinc-server-ng/issues/163",
			"keys/client-edit-create-key":         "key PUT stores create_key instead of regenerating the key: https://github.com/cinc-project/cinc-server-ng/issues/174",

			"groups/edit-missing":   "PUT on a missing group creates it instead of returning 404: https://github.com/cinc-project/cinc-server-ng/issues/166",
			"groups/member-unknown": "group PUT stores members that do not exist, which erchef drops: https://github.com/cinc-project/cinc-server-ng/issues/186",
			"groups/invalid-name":   "POST /groups accepts names erchef rejects with a 400: https://github.com/cinc-project/cinc-server-ng/issues/171",
			"acls/org":              "the org ACL is served at /organizations/O/_acl, not erchef's /organizations/O/organizations/_acl: https://github.com/cinc-project/cinc-server-ng/issues/161",
			"acls/missing-object":   "the _acl of a missing object answers with a default ACL instead of 404: https://github.com/cinc-project/cinc-server-ng/issues/169",
			"acls/unknown-member":   "ACL PUT stores actors and groups that do not exist instead of a 400: https://github.com/cinc-project/cinc-server-ng/issues/170",

			"policies/push-rejects-invalid-lock": "policy revision PUT accepts revision ids and run lists erchef rejects: https://github.com/cinc-project/cinc-server-ng/issues/183",
		},
	}
	return m.Run()
}

func TestCincServerNG(t *testing.T) {
	suite.Run(t, target)
}
