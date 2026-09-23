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
			"nodes/edit-missing": "PUT on a missing node creates it instead of returning 404: https://github.com/cinc-project/cinc-server-ng/issues/166",

			"users/edit-missing":              "PUT on a missing user creates it instead of returning 404: https://github.com/cinc-project/cinc-server-ng/issues/166",
			"users/create-invalid-name":       "user names are not validated against ^[a-z0-9_-]+$: https://github.com/cinc-project/cinc-server-ng/issues/176",
			"users/create-missing-fields":     "user create does not require display_name, a valid email or a password: https://github.com/cinc-project/cinc-server-ng/issues/176",
			"users/password-too-short":        "a password under 6 characters is accepted: https://github.com/cinc-project/cinc-server-ng/issues/176",
			"users/email-lowercased":          "a user's email is stored as sent, not lower-cased: https://github.com/cinc-project/cinc-server-ng/issues/176",
			"users/edit-keeps-omitted-fields": "a user PUT replaces the record instead of merging into it: https://github.com/cinc-project/cinc-server-ng/issues/176",
			"users/delete-member":             "deleting a user leaves its org membership behind: https://github.com/cinc-project/cinc-server-ng/issues/178",
			"users/delete-invited":            "deleting a user leaves its pending invitations behind: https://github.com/cinc-project/cinc-server-ng/issues/178",
			"orgs/create-invalid-name":        "org names are not validated: https://github.com/cinc-project/cinc-server-ng/issues/177",
			"orgs/member-remove-admin":        "a member of the admins group can be removed from the org: https://github.com/cinc-project/cinc-server-ng/issues/180",
			"orgs/member-add-forbidden":       "any member may force-add a user to the org: https://github.com/cinc-project/cinc-server-ng/issues/179",
			"orgs/member-remove-forbidden":    "any member may remove another member: https://github.com/cinc-project/cinc-server-ng/issues/179",
			"orgs/invite-create-forbidden":    "any member may invite a user: https://github.com/cinc-project/cinc-server-ng/issues/179",
			"orgs/invite-rescind-forbidden":   "any member may rescind an invitation: https://github.com/cinc-project/cinc-server-ng/issues/179",
		},
	}
	return m.Run()
}

func TestCincServerNG(t *testing.T) {
	suite.Run(t, target)
}
