package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// TestDataBagSecretFileIsStrippedLikeChef pins knife compatibility: Chef's
// EncryptedDataBagItem.load_secret strips leading and trailing whitespace
// from a secret file, so a file ending in a newline holds the same secret as
// one without. Every file source reads the file the same way.
func TestDataBagSecretFileIsStrippedLikeChef(t *testing.T) {
	for name, content := range map[string]string{
		"trailing newline": "s3cr3t\n",
		"CRLF":             "s3cr3t\r\n",
		"surrounding":      "  \ts3cr3t \n\n",
	} {
		for _, source := range []string{"--secret-file", "CINC_SECRET_FILE", "CHEF_SECRET_FILE", "secret_file"} {
			t.Run(name+" via "+source, func(t *testing.T) {
				var gotPut cinc.DataBagItem
				var gotPath string
				current := encryptedItem(t, cinc.DataBagItem{"id": "mysql", "password": "hunter2"}, "s3cr3t")
				srv := databagItemServer(t, "passwords", "mysql", current, &gotPut, &gotPath)
				secretPath := writeSecretFile(t, content)
				t.Setenv("CINC_SECRET_FILE", "")
				t.Setenv("CHEF_SECRET_FILE", "")

				args := []string{"databag", "secret", "show", "passwords", "mysql", "--format", "json"}
				switch source {
				case "--secret-file":
					args = append(args, "--secret-file", secretPath, "--config", writeDataBagConfig(t, srv.URL))
				case "secret_file":
					args = append(args, "--config", writeDataBagConfigWithSecret(t, srv.URL, secretPath))
				default:
					t.Setenv(source, secretPath)
					args = append(args, "--config", writeDataBagConfig(t, srv.URL))
				}

				root := newRootCmd()
				var buf bytes.Buffer
				root.SetOut(&buf)
				root.SetErr(&bytes.Buffer{})
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatalf("databag secret show: %v", err)
				}
				var got cinc.DataBagItem
				if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
					t.Fatalf("show output not valid JSON: %v\n%s", err, buf.String())
				}
				if got["password"] != "hunter2" {
					t.Errorf("decrypted password = %v, want hunter2", got["password"])
				}
			})
		}
	}
}

// TestDataBagSecretFileKeepsInnerWhitespace checks that only the ends are
// stripped, as Ruby's String#strip does: whitespace inside the secret (a
// base64 secret wrapped over several lines, say) is part of the key.
func TestDataBagSecretFileKeepsInnerWhitespace(t *testing.T) {
	var gotItem cinc.DataBagItem
	srv := databagItemCreateServer(t, "passwords", &gotItem)
	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"databag", "secret", "create", "passwords", "mysql",
		"--file", writeItemFile(t, cinc.DataBagItem{"id": "mysql", "password": "p"}),
		"--secret-file", writeSecretFile(t, "line one\nline two\n"), "--config", writeDataBagConfig(t, srv.URL)})
	if err := root.Execute(); err != nil {
		t.Fatalf("databag secret create: %v", err)
	}
	if _, err := gotItem.Decrypt([]byte("line one\nline two")); err != nil {
		t.Errorf("item should be encrypted with the inner newline kept: %v", err)
	}
}

// TestDataBagSecretLiteralIsVerbatim pins the other half of knife's
// behaviour: a secret given on the command line (knife's --secret) is used
// exactly as typed.
func TestDataBagSecretLiteralIsVerbatim(t *testing.T) {
	var gotItem cinc.DataBagItem
	srv := databagItemCreateServer(t, "passwords", &gotItem)
	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"databag", "secret", "create", "passwords", "mysql",
		"--file", writeItemFile(t, cinc.DataBagItem{"id": "mysql", "password": "p"}),
		"--secret", " spaced ", "--config", writeDataBagConfig(t, srv.URL)})
	if err := root.Execute(); err != nil {
		t.Fatalf("databag secret create --secret: %v", err)
	}
	if _, err := gotItem.Decrypt([]byte(" spaced ")); err != nil {
		t.Errorf("item should be encrypted with the literal secret, spaces included: %v", err)
	}
}

// TestDataBagSecretFileRejectsEmpty mirrors Chef, which refuses a
// zero-length secret: an empty or whitespace-only file must not silently
// become an empty encryption key.
func TestDataBagSecretFileRejectsEmpty(t *testing.T) {
	for name, content := range map[string]string{"empty": "", "whitespace only": " \n\t\n"} {
		t.Run(name, func(t *testing.T) {
			var gotItem cinc.DataBagItem
			srv := databagItemCreateServer(t, "passwords", &gotItem)
			secretPath := writeSecretFile(t, content)

			root := newRootCmd()
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			root.SetArgs([]string{"databag", "secret", "create", "passwords", "mysql",
				"--file", writeItemFile(t, cinc.DataBagItem{"id": "mysql", "password": "p"}),
				"--secret-file", secretPath, "--config", writeDataBagConfig(t, srv.URL)})
			err := root.Execute()
			if err == nil {
				t.Fatal("expected an error for an empty secret file")
			}
			if !strings.Contains(err.Error(), "empty") || !strings.Contains(err.Error(), secretPath) {
				t.Errorf("error should say the secret file at %s is empty: %v", secretPath, err)
			}
			if gotItem != nil {
				t.Errorf("an item was posted despite the empty secret: %v", gotItem)
			}
		})
	}
}

// writeItemFile writes item as JSON to a temp file and returns the path.
func writeItemFile(t *testing.T, item cinc.DataBagItem) string {
	t.Helper()
	body, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "item.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestDataBagSecretCreateRefusesEncryptedItem covers feeding `secret create`
// an item copied from the server, already encrypted. Encrypting it again
// would store an item that decrypts to ciphertext, so it's refused before
// anything is sent, with a pointer at what to do instead.
func TestDataBagSecretCreateRefusesEncryptedItem(t *testing.T) {
	srv := databagNoRequestServer(t)
	enc := encryptedItem(t, cinc.DataBagItem{"id": "mysql", "password": "p"}, "s3cr3t")
	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"databag", "secret", "create", "passwords", "mysql",
		"--file", writeItemFile(t, enc),
		"--secret-file", writeSecretFile(t, "s3cr3t"), "--config", writeDataBagConfig(t, srv.URL)})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected an already-encrypted item to be refused")
	}
	if !strings.Contains(err.Error(), "already holds encrypted values") || !strings.Contains(err.Error(), "plaintext") {
		t.Errorf("error should explain the item is already encrypted: %v", err)
	}
}

// TestDataBagSecretFileRejectsInvalidUTF8 follows Chef, which reads a secret
// file as UTF-8 text and can't use one that isn't, so knife and chef-client
// could never decrypt what such a secret encrypted.
func TestDataBagSecretFileRejectsInvalidUTF8(t *testing.T) {
	srv := databagNoRequestServer(t)
	secretPath := writeSecretFile(t, "\xff\xfesecret")
	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"databag", "secret", "create", "passwords", "mysql",
		"--file", writeItemFile(t, cinc.DataBagItem{"id": "mysql", "password": "p"}),
		"--secret-file", secretPath, "--config", writeDataBagConfig(t, srv.URL)})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected a non-UTF-8 secret file to be refused")
	}
	if !strings.Contains(err.Error(), secretPath) || !strings.Contains(err.Error(), "UTF-8") {
		t.Errorf("error should name the secret file and say it isn't UTF-8: %v", err)
	}
}
