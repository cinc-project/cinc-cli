//go:build acceptance

package acceptance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// TestDataBagSecretRoundTripAgainstCincZero creates an encrypted item,
// reads it back decrypted with the same secret, and confirms that a plain
// `databag item show` still sees the encrypted-at-rest wrapper.
func TestDataBagSecretRoundTripAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	// The bag must already exist, just like `databag item create`.
	runCinc(t, env.binary, "databag", "create", "secrets", "--config", env.cfgPath)

	secretPath := filepath.Join(t.TempDir(), "encrypted_data_bag_secret")
	if err := os.WriteFile(secretPath, []byte("open-sesame"), 0o600); err != nil {
		t.Fatal(err)
	}

	itemPath := filepath.Join(t.TempDir(), "item.json")
	body, _ := json.Marshal(cinc.DataBagItem{"id": "db-password", "password": "hunter2"})
	if err := os.WriteFile(itemPath, body, 0o600); err != nil {
		t.Fatal(err)
	}

	out := runCinc(t, env.binary, "databag", "secret", "create", "secrets", "db-password",
		"--file", itemPath, "--secret-file", secretPath, "--config", env.cfgPath)
	if out != "Created encrypted item \"db-password\" in data bag \"secrets\"\n" {
		t.Errorf("databag secret create output = %q", out)
	}

	// Reading it back with the same secret decrypts the plaintext.
	shown := runCinc(t, env.binary, "databag", "secret", "show", "secrets", "db-password",
		"--secret-file", secretPath, "--config", env.cfgPath, "--format", "json")
	var plain cinc.DataBagItem
	if err := json.Unmarshal([]byte(shown), &plain); err != nil {
		t.Fatalf("databag secret show output not valid JSON: %v\n%s", err, shown)
	}
	if plain["id"] != "db-password" || plain["password"] != "hunter2" {
		t.Errorf("decrypted item = %+v, want id=db-password password=hunter2", plain)
	}

	// A plain `databag item show` sees the still-encrypted wrapper at rest.
	atRest := runCinc(t, env.binary, "databag", "item", "show", "secrets", "db-password",
		"--config", env.cfgPath, "--format", "json")
	var encrypted cinc.DataBagItem
	if err := json.Unmarshal([]byte(atRest), &encrypted); err != nil {
		t.Fatalf("databag item show output not valid JSON: %v\n%s", err, atRest)
	}
	if encrypted["id"] != "db-password" {
		t.Errorf("encrypted item id = %v, want cleartext db-password", encrypted["id"])
	}
	if pw, ok := encrypted["password"].(string); ok {
		t.Errorf("password stored in cleartext at rest: %q", pw)
	}
	if !encrypted.IsEncrypted() {
		t.Errorf("item is not encrypted at rest: %+v", encrypted)
	}
	// The plaintext must not appear anywhere in the at-rest payload.
	if strings.Contains(atRest, "hunter2") {
		t.Errorf("plaintext leaked into encrypted-at-rest payload:\n%s", atRest)
	}
}

// TestDataBagSecretEditRoundTripAgainstCincZero creates an encrypted item,
// edits it via --file (the scripted path that re-encrypts the supplied
// plaintext), and confirms the new value decrypts back while staying
// encrypted at rest.
func TestDataBagSecretEditRoundTripAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	runCinc(t, env.binary, "databag", "create", "vault", "--config", env.cfgPath)

	secretPath := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secretPath, []byte("open-sesame"), 0o600); err != nil {
		t.Fatal(err)
	}

	createPath := writeJSONFile(t, cinc.DataBagItem{"id": "api-key", "token": "v1-original"})
	runCinc(t, env.binary, "databag", "secret", "create", "vault", "api-key",
		"--file", createPath, "--secret-file", secretPath, "--config", env.cfgPath)

	// Edit via --file: the new plaintext is re-encrypted and PUT back.
	editPath := writeJSONFile(t, cinc.DataBagItem{"id": "api-key", "token": "v2-rotated"})
	edit := runCinc(t, env.binary, "databag", "secret", "edit", "vault", "api-key",
		"--file", editPath, "--secret-file", secretPath, "--config", env.cfgPath)
	if edit != "Updated encrypted item \"api-key\" in bag \"vault\"\n" {
		t.Errorf("databag secret edit output = %q", edit)
	}

	// The updated value decrypts back with the same secret.
	shown := runCinc(t, env.binary, "databag", "secret", "show", "vault", "api-key",
		"--secret-file", secretPath, "--config", env.cfgPath, "--format", "json")
	var plain cinc.DataBagItem
	if err := json.Unmarshal([]byte(shown), &plain); err != nil {
		t.Fatalf("databag secret show output not valid JSON: %v\n%s", err, shown)
	}
	if plain["token"] != "v2-rotated" {
		t.Errorf("decrypted token = %v, want v2-rotated", plain["token"])
	}

	// It is still encrypted at rest and the plaintext never leaked.
	atRest := runCinc(t, env.binary, "databag", "item", "show", "vault", "api-key",
		"--config", env.cfgPath, "--format", "json")
	if strings.Contains(atRest, "v2-rotated") {
		t.Errorf("plaintext leaked into encrypted-at-rest payload:\n%s", atRest)
	}
}

// TestDataBagSecretEditWrongSecretAgainstCincZero confirms `databag secret
// edit` (interactive path) fails clearly when handed the wrong secret: the
// item is fetched and decryption fails before the editor is ever launched.
func TestDataBagSecretEditWrongSecretAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	runCinc(t, env.binary, "databag", "create", "vault", "--config", env.cfgPath)

	rightSecret := filepath.Join(t.TempDir(), "right")
	if err := os.WriteFile(rightSecret, []byte("correct-horse"), 0o600); err != nil {
		t.Fatal(err)
	}
	wrongSecret := filepath.Join(t.TempDir(), "wrong")
	if err := os.WriteFile(wrongSecret, []byte("battery-staple"), 0o600); err != nil {
		t.Fatal(err)
	}

	itemPath := writeJSONFile(t, cinc.DataBagItem{"id": "db-password", "password": "hunter2"})
	runCinc(t, env.binary, "databag", "secret", "create", "vault", "db-password",
		"--file", itemPath, "--secret-file", rightSecret, "--config", env.cfgPath)

	// Editing with the wrong secret (no --file) must fail at the decrypt step.
	_, stderr, err := runCincRaw(env.binary, "databag", "secret", "edit", "vault", "db-password",
		"--secret-file", wrongSecret, "--config", env.cfgPath)
	if err == nil {
		t.Fatalf("databag secret edit with the wrong secret unexpectedly succeeded")
	}
	if !strings.Contains(strings.ToLower(stderr), "wrong secret") &&
		!strings.Contains(strings.ToLower(stderr), "couldn't decrypt") {
		t.Errorf("expected a wrong-secret error, got stderr:\n%s", stderr)
	}
}

// TestDataBagSecretEditNotEncryptedAgainstCincZero confirms `databag secret
// edit` points the user at the plaintext command when the item isn't an
// encrypted data bag item at all.
func TestDataBagSecretEditNotEncryptedAgainstCincZero(t *testing.T) {
	env, stop := startAcceptance(t)
	defer stop()

	runCinc(t, env.binary, "databag", "create", "vault", "--config", env.cfgPath)

	secretPath := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secretPath, []byte("open-sesame"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Create a plain (unencrypted) item with the regular item command.
	plainPath := writeJSONFile(t, cinc.DataBagItem{"id": "plain", "note": "cleartext"})
	runCinc(t, env.binary, "databag", "item", "create", "vault", "plain",
		"--file", plainPath, "--config", env.cfgPath)

	_, stderr, err := runCincRaw(env.binary, "databag", "secret", "edit", "vault", "plain",
		"--secret-file", secretPath, "--config", env.cfgPath)
	if err == nil {
		t.Fatalf("databag secret edit on a plaintext item unexpectedly succeeded")
	}
	if !strings.Contains(strings.ToLower(stderr), "isn't encrypted") {
		t.Errorf("expected a not-encrypted error, got stderr:\n%s", stderr)
	}
}
