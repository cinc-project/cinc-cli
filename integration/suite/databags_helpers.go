package suite

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Helpers for the data bag family. Every name carries a databag prefix so
// they never collide with another family's helpers in this package.

// databagCreate creates an empty data bag and registers its deletion, which
// removes any items still in it.
func databagCreate(c *cli, bag string) {
	c.t.Helper()
	c.run("databag", "create", bag)
	c.cleanup("databag", "delete", bag)
}

// databagNew creates a uniquely named data bag and returns its name.
func databagNew(c *cli) string {
	c.t.Helper()
	bag := uniqueName(c.t, "bag")
	databagCreate(c, bag)
	return bag
}

// databagItemCreate creates item in bag through --file. The bag's cleanup
// removes the item.
func databagItemCreate(c *cli, bag string, item map[string]any) {
	c.t.Helper()
	id, _ := item["id"].(string)
	c.run("databag", "item", "create", bag, id, "--file", writeJSON(c.t, item))
}

// databagItemShow returns the item exactly as `databag item show --format
// json` prints it.
func databagItemShow(c *cli, bag, id string) map[string]any {
	c.t.Helper()
	var item map[string]any
	c.json(&item, "databag", "item", "show", bag, id)
	return item
}

// databagItemIDs returns `databag item list <bag> --format json`.
func databagItemIDs(c *cli, bag string) []string {
	c.t.Helper()
	var ids []string
	c.json(&ids, "databag", "item", "list", bag)
	return ids
}

// databagNames returns `databag list --format json`.
func databagNames(c *cli) []string {
	c.t.Helper()
	var names []string
	c.json(&names, "databag", "list")
	return names
}

// databagSecretFile writes secret to a new file and returns its path.
func databagSecretFile(t *testing.T, secret string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "encrypted_data_bag_secret")
	writeFile(t, path, secret)
	return path
}

// databagSecretShow decrypts an item with `databag secret show --format json`
// and the given extra flags, run with env.
func databagSecretShow(c *cli, env []string, bag, id string, flags ...string) map[string]any {
	c.t.Helper()
	args := append([]string{"databag", "secret", "show", bag, id, "--format", "json"}, flags...)
	out := c.runWith(runOpts{env: env}, args...)
	var item map[string]any
	if err := json.Unmarshal([]byte(out), &item); err != nil {
		c.t.Fatalf("databag secret show --format json: %v\n%s", err, out)
	}
	return item
}

// databagAddSecretProfile appends a profile signing as the target admin
// against its org whose secret_file key points at secretPath.
func databagAddSecretProfile(c *cli, profile, secretPath string) {
	c.t.Helper()
	f, err := os.OpenFile(c.credentialsPath(), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		c.t.Fatal(err)
	}
	_, err = fmt.Fprintf(f, "[%s]\ncinc_server_url = %q\nclient_name = %q\nclient_key = %q\nsecret_file = %q\n\n",
		profile, c.orgURL(c.tgt.Org), c.tgt.Admin, c.tgt.KeyPath, secretPath)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		c.t.Fatal(err)
	}
}

// databagClientActor creates an API client in the target org, which puts it
// in the org's "clients" group and nothing else, and adds a profile signing
// as it. It returns the profile name.
func databagClientActor(c *cli) string {
	c.t.Helper()
	name := uniqueName(c.t, "client")
	key := filepath.Join(c.t.TempDir(), name+".pem")
	c.run("client", "create", name, "--key-file", key)
	c.cleanup("client", "delete", name)
	c.addProfile(name, c.tgt.Org, name, key)
	return name
}

// databagWantConflict fails the case unless r is a failed run reporting that
// the object already exists (HTTP 409).
func databagWantConflict(t *testing.T, r result) {
	t.Helper()
	low := strings.ToLower(r.stderr)
	conflict := strings.Contains(low, "already exists") || hasStatus(low, 409)
	if r.exitCode == 0 || !conflict {
		t.Fatalf("want an already-exists error: %s", r)
	}
}

// databagWantForbidden fails the case unless r is a failed run reporting a
// permission refusal (HTTP 403).
func databagWantForbidden(t *testing.T, r result) {
	t.Helper()
	low := strings.ToLower(r.stderr)
	forbidden := hasStatus(low, 403) || strings.Contains(low, "forbidden") ||
		strings.Contains(low, "permission")
	if r.exitCode == 0 || !forbidden {
		t.Fatalf("want a permission error: %s", r)
	}
}

// databagWantStderr fails the case unless r failed and its stderr contains
// every one of wants, ignoring case.
func databagWantStderr(t *testing.T, r result, wants ...string) {
	t.Helper()
	if r.exitCode == 0 {
		t.Fatalf("expected a failure, got success: %s", r)
	}
	low := strings.ToLower(r.stderr)
	for _, w := range wants {
		if !strings.Contains(low, strings.ToLower(w)) {
			t.Fatalf("stderr should mention %q: %s", w, r)
		}
	}
}

// Encrypted values produced by Chef's own Ruby implementation
// (Chef::EncryptedDataBagItem::Encryptor::Version{1,2,3}Encryptor.new(
// "hello world", databagChefSecret).for_encrypted_item), the known-answer
// fixtures cinc-api's unit and integration tests use. Each is stored as a
// plain item through `databag item create`, then read back with `databag
// secret show`, proving the CLI reads every format knife and chef-client
// write.
const databagChefSecret = "opensesame-super-secret-key"

var databagChefHelloWorld = map[string]string{
	"v1": `{"encrypted_data":"MSa0fay80/gnrXL5WOHWRnI2/mFrc5VCp2VsCDJaMTk=\n","iv":"s2ElSTGBePtOJxPuaOdazA==\n","version":1,"cipher":"aes-256-cbc"}`,
	"v2": `{"encrypted_data":"YPayJpcFtqEphi40tnv8QWBwcvpakdxfctIeGOnakHE=\n","hmac":"2SRvwbdKdRejpDkWZfpmrzsG09Cj3QwijFlWMIN3Glw=\n","iv":"YMv0tjSdxQDOXjTHVFa/ew==\n","version":2,"cipher":"aes-256-cbc"}`,
	"v3": `{"encrypted_data":"tmPS0vwip+VU5tXd23ekJIGw0CrikuoKeZGm1mqD\n","iv":"ckznCKqh5nUXxHXB\n","auth_tag":"M5LttZ2UqwNvEWLVRwbAeA==\n","version":3,"cipher":"aes-256-gcm"}`,
}

// databagChefFixture decodes one of databagChefHelloWorld.
func databagChefFixture(t *testing.T, version string) map[string]any {
	t.Helper()
	var w map[string]any
	if err := json.Unmarshal([]byte(databagChefHelloWorld[version]), &w); err != nil {
		t.Fatalf("decode %s fixture: %v", version, err)
	}
	return w
}

// databagChefDecryptV3 decrypts one version 3 encrypted value the way Chef's
// Version3Decryptor does, written from Chef's algorithm and independent of
// cinc-api: the AES-256-GCM key is sha256(secret), the IV, auth tag and
// ciphertext are base64 (whitespace ignored, as Ruby's decode64 does), there
// is no additional authenticated data, and the plaintext is the JSON object
// {"json_wrapper": <value>}. It proves an item the CLI writes is one knife
// and chef-client can read.
func databagChefDecryptV3(wrapper any, secret string) (any, error) {
	w, ok := wrapper.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("value is not an encrypted wrapper: %v", wrapper)
	}
	if w["version"] != float64(3) || w["cipher"] != "aes-256-gcm" {
		return nil, fmt.Errorf("wrapper is not version 3 aes-256-gcm: %v", w)
	}
	field := func(k string) ([]byte, error) {
		s, _ := w[k].(string)
		b, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(s), ""))
		if err != nil {
			return nil, fmt.Errorf("%s: %v", k, err)
		}
		return b, nil
	}
	data, err := field("encrypted_data")
	if err != nil {
		return nil, err
	}
	iv, err := field("iv")
	if err != nil {
		return nil, err
	}
	tag, err := field("auth_tag")
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCMWithNonceSize(block, len(iv))
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, iv, append(data, tag...), nil)
	if err != nil {
		return nil, fmt.Errorf("authentication failed: %v", err)
	}
	var box map[string]any
	if err := json.Unmarshal(plain, &box); err != nil {
		return nil, fmt.Errorf("plaintext is not a json_wrapper object: %v", err)
	}
	v, ok := box["json_wrapper"]
	if !ok {
		return nil, fmt.Errorf("plaintext has no json_wrapper: %s", plain)
	}
	return v, nil
}
