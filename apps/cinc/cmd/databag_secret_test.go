package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// writeSecretFile writes secret bytes to a temp file and returns the path.
func writeSecretFile(t *testing.T, secret string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "encrypted_data_bag_secret")
	if err := os.WriteFile(path, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeDataBagConfigWithSecret writes a credentials file whose default
// profile carries a secret_file key pointing at secretFile.
func writeDataBagConfigWithSecret(t *testing.T, serverURL, secretFile string) string {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "credentials")
	cfg := fmt.Sprintf(`[default]
cinc_server_url = "%s/organizations/acme"
client_name     = "tim"
client_key      = %q
secret_file     = %q
`, serverURL, writeTestKey(t), secretFile)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

// encryptedItem encrypts a plaintext item with secret, failing the test on
// error. It is used to seed servers with an item that's encrypted at rest.
func encryptedItem(t *testing.T, plain cinc.DataBagItem, secret string) cinc.DataBagItem {
	t.Helper()
	enc, err := plain.Encrypt([]byte(secret))
	if err != nil {
		t.Fatalf("seed encrypt: %v", err)
	}
	return enc
}

// assertEncryptedWrapper asserts v is a v3 encryption wrapper object.
func assertEncryptedWrapper(t *testing.T, key string, v any) {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("value for %q is not a wrapper object: %T", key, v)
	}
	for _, field := range []string{"version", "cipher", "encrypted_data"} {
		if _, ok := m[field]; !ok {
			t.Errorf("wrapper for %q missing %q field: %+v", key, field, m)
		}
	}
}

func TestDataBagSecretCreateEncryptsBeforePost(t *testing.T) {
	var gotItem cinc.DataBagItem
	srv := databagItemCreateServer(t, "passwords", &gotItem)

	withStubDataBagItemEditor(t, func(in cinc.DataBagItem) (cinc.DataBagItem, error) {
		if in["id"] != "mysql" {
			t.Errorf("editor seed id = %v, want mysql", in["id"])
		}
		return cinc.DataBagItem{"id": "mysql", "password": "hunter2"}, nil
	})

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"databag", "secret", "create", "passwords", "mysql",
		"--secret", "s3cr3t", "--config", writeDataBagConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("databag secret create: %v", err)
	}

	// id stays cleartext; password is an encryption wrapper.
	if gotItem["id"] != "mysql" {
		t.Errorf("POST body id = %v, want cleartext mysql", gotItem["id"])
	}
	if _, isString := gotItem["password"].(string); isString {
		t.Errorf("password was posted in cleartext: %v", gotItem["password"])
	}
	assertEncryptedWrapper(t, "password", gotItem["password"])

	// And it actually decrypts back to the plaintext with the same secret.
	plain, err := gotItem.Decrypt([]byte("s3cr3t"))
	if err != nil {
		t.Fatalf("decrypt posted item: %v", err)
	}
	if plain["password"] != "hunter2" {
		t.Errorf("decrypted password = %v, want hunter2", plain["password"])
	}
	if got := buf.String(); got != "Created encrypted item \"mysql\" in data bag \"passwords\"\n" {
		t.Errorf("create output = %q", got)
	}
}

func TestDataBagSecretCreateReadsSecretFromFile(t *testing.T) {
	var gotItem cinc.DataBagItem
	srv := databagItemCreateServer(t, "passwords", &gotItem)

	withStubDataBagItemEditor(t, func(cinc.DataBagItem) (cinc.DataBagItem, error) {
		t.Fatal("editor was invoked despite --file")
		return nil, nil
	})

	filePath := filepath.Join(t.TempDir(), "item.json")
	body, _ := json.Marshal(cinc.DataBagItem{"id": "ignored", "password": "from-file"})
	if err := os.WriteFile(filePath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	secretPath := writeSecretFile(t, "file-secret")

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"databag", "secret", "create", "passwords", "mysql",
		"--file", filePath, "--secret-file", secretPath, "--config", writeDataBagConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("databag secret create --secret-file: %v", err)
	}
	if gotItem["id"] != "mysql" {
		t.Errorf("POST body id = %v, want mysql (path arg wins over file)", gotItem["id"])
	}
	plain, err := gotItem.Decrypt([]byte("file-secret"))
	if err != nil {
		t.Fatalf("decrypt posted item: %v", err)
	}
	if plain["password"] != "from-file" {
		t.Errorf("decrypted password = %v, want from-file", plain["password"])
	}
}

func TestDataBagSecretShowDecryptsWithSecretFile(t *testing.T) {
	var gotPut cinc.DataBagItem
	var gotPath string
	current := encryptedItem(t, cinc.DataBagItem{"id": "mysql", "password": "hunter2"}, "s3cr3t")
	srv := databagItemServer(t, "passwords", "mysql", current, &gotPut, &gotPath)
	secretPath := writeSecretFile(t, "s3cr3t")

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"databag", "secret", "show", "passwords", "mysql",
		"--secret-file", secretPath, "--config", writeDataBagConfig(t, srv.URL), "--format", "json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("databag secret show: %v", err)
	}
	if gotPath != "" {
		t.Errorf("show issued a PUT at %q, want read-only", gotPath)
	}
	var got cinc.DataBagItem
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("show output not valid JSON: %v\n%s", err, buf.String())
	}
	if got["id"] != "mysql" || got["password"] != "hunter2" {
		t.Errorf("decrypted show = %+v, want id=mysql password=hunter2", got)
	}
}

func TestDataBagSecretShowReadsSecretFromEnv(t *testing.T) {
	var gotPut cinc.DataBagItem
	var gotPath string
	current := encryptedItem(t, cinc.DataBagItem{"id": "mysql", "password": "hunter2"}, "env-secret")
	srv := databagItemServer(t, "passwords", "mysql", current, &gotPut, &gotPath)

	t.Setenv("CHEF_SECRET_FILE", "")
	t.Setenv("CINC_SECRET_FILE", writeSecretFile(t, "env-secret"))

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"databag", "secret", "show", "passwords", "mysql",
		"--config", writeDataBagConfig(t, srv.URL), "--format", "json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("databag secret show (env secret): %v", err)
	}
	var got cinc.DataBagItem
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("show output not valid JSON: %v\n%s", err, buf.String())
	}
	if got["password"] != "hunter2" {
		t.Errorf("decrypted password = %v, want hunter2", got["password"])
	}
}

func TestDataBagSecretShowReadsSecretFromProfile(t *testing.T) {
	var gotPut cinc.DataBagItem
	var gotPath string
	current := encryptedItem(t, cinc.DataBagItem{"id": "mysql", "password": "hunter2"}, "profile-secret")
	srv := databagItemServer(t, "passwords", "mysql", current, &gotPut, &gotPath)

	t.Setenv("CINC_SECRET_FILE", "")
	t.Setenv("CHEF_SECRET_FILE", "")
	cfgPath := writeDataBagConfigWithSecret(t, srv.URL, writeSecretFile(t, "profile-secret"))

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"databag", "secret", "show", "passwords", "mysql",
		"--config", cfgPath, "--format", "json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("databag secret show (profile secret): %v", err)
	}
	var got cinc.DataBagItem
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("show output not valid JSON: %v\n%s", err, buf.String())
	}
	if got["password"] != "hunter2" {
		t.Errorf("decrypted password = %v, want hunter2", got["password"])
	}
}

func TestDataBagSecretShowFriendlyErrorWhenNotEncrypted(t *testing.T) {
	var gotPut cinc.DataBagItem
	var gotPath string
	// Plaintext item — never encrypted.
	current := cinc.DataBagItem{"id": "mysql", "password": "hunter2"}
	srv := databagItemServer(t, "passwords", "mysql", current, &gotPut, &gotPath)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"databag", "secret", "show", "passwords", "mysql",
		"--secret", "whatever", "--config", writeDataBagConfig(t, srv.URL)})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error showing a plaintext item as a secret")
	}
	if !strings.Contains(err.Error(), "isn't encrypted") || !strings.Contains(err.Error(), "databag item show") {
		t.Errorf("error should point at the plaintext command: %v", err)
	}
}

func TestDataBagSecretShowFriendlyErrorOnWrongSecret(t *testing.T) {
	var gotPut cinc.DataBagItem
	var gotPath string
	current := encryptedItem(t, cinc.DataBagItem{"id": "mysql", "password": "hunter2"}, "right-secret")
	srv := databagItemServer(t, "passwords", "mysql", current, &gotPut, &gotPath)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"databag", "secret", "show", "passwords", "mysql",
		"--secret", "wrong-secret", "--config", writeDataBagConfig(t, srv.URL)})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error decrypting with the wrong secret")
	}
	if !strings.Contains(err.Error(), "couldn't decrypt") || !strings.Contains(err.Error(), "wrong secret") {
		t.Errorf("error should mention a wrong secret: %v", err)
	}
}

func TestDataBagSecretEditRoundTrips(t *testing.T) {
	var gotPut cinc.DataBagItem
	var gotPath string
	current := encryptedItem(t, cinc.DataBagItem{"id": "mysql", "password": "old"}, "s3cr3t")
	srv := databagItemServer(t, "passwords", "mysql", current, &gotPut, &gotPath)

	withStubDataBagItemEditor(t, func(in cinc.DataBagItem) (cinc.DataBagItem, error) {
		// The editor sees decrypted plaintext.
		if in["password"] != "old" {
			t.Errorf("editor saw password = %v, want decrypted 'old'", in["password"])
		}
		out := cinc.DataBagItem{}
		for k, v := range in {
			out[k] = v
		}
		out["password"] = "new"
		return out, nil
	})

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"databag", "secret", "edit", "passwords", "mysql",
		"--secret", "s3cr3t", "--config", writeDataBagConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("databag secret edit: %v", err)
	}
	if gotPath != "/organizations/acme/data/passwords/mysql" {
		t.Errorf("PUT path = %q", gotPath)
	}
	if gotPut["id"] != "mysql" {
		t.Errorf("PUT id = %v, want cleartext mysql", gotPut["id"])
	}
	assertEncryptedWrapper(t, "password", gotPut["password"])
	plain, err := gotPut.Decrypt([]byte("s3cr3t"))
	if err != nil {
		t.Fatalf("decrypt PUT body: %v", err)
	}
	if plain["password"] != "new" {
		t.Errorf("round-tripped password = %v, want new", plain["password"])
	}
	if got := buf.String(); got != "Updated encrypted item \"mysql\" in bag \"passwords\"\n" {
		t.Errorf("edit output = %q", got)
	}
}

func TestDataBagSecretEditSkipsPutWhenUnchanged(t *testing.T) {
	var gotPut cinc.DataBagItem
	var gotPath string
	current := encryptedItem(t, cinc.DataBagItem{"id": "mysql", "password": "old"}, "s3cr3t")
	srv := databagItemServer(t, "passwords", "mysql", current, &gotPut, &gotPath)

	withStubDataBagItemEditor(t, func(in cinc.DataBagItem) (cinc.DataBagItem, error) {
		out := cinc.DataBagItem{}
		for k, v := range in {
			out[k] = v
		}
		return out, nil
	})

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"databag", "secret", "edit", "passwords", "mysql",
		"--secret", "s3cr3t", "--config", writeDataBagConfig(t, srv.URL)})

	if err := root.Execute(); err != nil {
		t.Fatalf("databag secret edit (unchanged): %v", err)
	}
	if gotPath != "" {
		t.Errorf("server saw a PUT at %q for an unchanged edit", gotPath)
	}
	if got := buf.String(); got != "Encrypted item \"mysql\" in bag \"passwords\" unchanged\n" {
		t.Errorf("edit output = %q", got)
	}
}

func TestDataBagSecretRejectsBothSecretFlags(t *testing.T) {
	srv := databagItemCreateServer(t, "passwords", &cinc.DataBagItem{})

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"databag", "secret", "create", "passwords", "mysql",
		"--secret", "a", "--secret-file", writeSecretFile(t, "b"), "--config", writeDataBagConfig(t, srv.URL)})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error when both --secret and --secret-file are set")
	}
	if !strings.Contains(err.Error(), "can't use --secret and --secret-file together") {
		t.Errorf("error = %v, want mutual-exclusion message", err)
	}
}

func TestDataBagSecretErrorsWhenNoSecret(t *testing.T) {
	srv := databagItemCreateServer(t, "passwords", &cinc.DataBagItem{})
	t.Setenv("CINC_SECRET_FILE", "")
	t.Setenv("CHEF_SECRET_FILE", "")

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"databag", "secret", "create", "passwords", "mysql",
		"--config", writeDataBagConfig(t, srv.URL)})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error when no secret is configured")
	}
	if !strings.Contains(err.Error(), "--secret-file") || !strings.Contains(err.Error(), "secret_file") {
		t.Errorf("error should point at --secret-file and the secret_file key: %v", err)
	}
}
