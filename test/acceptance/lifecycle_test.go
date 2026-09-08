//go:build acceptance

package acceptance

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// TestClientLifecycleAgainstCincZero walks a client through
// create -> show -> edit -> show-reflects-edit -> delete -> list-omits.
// client is used (rather than environment) because it is the noun with a
// scripted `edit --file` path reachable from `go test`.
func TestClientLifecycleAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	// create via the BYO public-key path
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pubBytes, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	pubPath := filepath.Join(t.TempDir(), "life.pub")
	if err := os.WriteFile(pubPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes}), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := runCinc(t, env.binary, "client", "create", "lifecycle", "--public-key", pubPath, "--config", env.cfgPath); out != "Created client \"lifecycle\"\n" {
		t.Fatalf("create output = %q", out)
	}

	// show: a freshly created client is not a validator
	show := runCinc(t, env.binary, "client", "show", "lifecycle", "--config", env.cfgPath)
	if !strings.Contains(show, "\"name\": \"lifecycle\"") {
		t.Errorf("show after create missing name:\n%s", show)
	}

	// edit via --file: flip it to a validator
	edited := filepath.Join(t.TempDir(), "lifecycle.json")
	body, err := json.Marshal(cinc.APIClient{Name: "lifecycle", Validator: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(edited, body, 0o600); err != nil {
		t.Fatal(err)
	}
	runCinc(t, env.binary, "client", "edit", "lifecycle", "--file", edited, "--config", env.cfgPath)

	// show reflects the edit
	show2 := runCinc(t, env.binary, "client", "show", "lifecycle", "--config", env.cfgPath)
	if !strings.Contains(show2, "\"validator\": true") {
		t.Errorf("show after edit does not reflect validator=true:\n%s", show2)
	}

	// delete, then list omits it
	runCinc(t, env.binary, "client", "delete", "lifecycle", "--config", env.cfgPath)
	after := runCinc(t, env.binary, "client", "list", "--config", env.cfgPath)
	if strings.Contains(after, "lifecycle") {
		t.Errorf("client list still has lifecycle after delete:\n%s", after)
	}
}

// Note on node bootstrap: a server-side node-lifecycle journey
// (bootstrap -> show -> delete) is not testable here. `node bootstrap`
// only drives the remote install over SSH; the node object appears on
// the server only once cinc-client actually runs and registers it. The
// acceptance SSH server is a stub that never runs cinc-client, so no
// node is persisted. Bootstrap behavior itself is covered by
// TestNodeBootstrapAgainstCincZeroAndSSHServer.

// TestMultiOrgIsolationAgainstCincZero starts cinc-zero with two orgs,
// creates an environment in "acme", and confirms it is invisible from a
// config scoped to "other". --repo only seeds the first org, so "other"
// starts with just its auto-created defaults — exactly what isolation
// wants to assert against.
func TestMultiOrgIsolationAgainstCincZero(t *testing.T) {
	env, stop := startAcceptanceWith(t, acceptanceOptions{orgs: "acme,other"})
	defer stop()

	runCinc(t, env.binary, "environment", "create", "acme-only",
		"--description", "acme", "--config", env.cfgPath)

	otherCfg := writeAcceptanceConfig(t, env.port, "other", env.adminKey)
	otherList := runCinc(t, env.binary, "environment", "list", "--config", otherCfg)
	if strings.Contains(otherList, "acme-only") {
		t.Errorf("environment created in acme leaked into other:\n%s", otherList)
	}

	acmeList := runCinc(t, env.binary, "environment", "list", "--config", env.cfgPath)
	if !strings.Contains(acmeList, "acme-only") {
		t.Errorf("acme lost its own environment:\n%s", acmeList)
	}
}
