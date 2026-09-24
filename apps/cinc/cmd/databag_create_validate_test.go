package cmd

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// databagNoRequestServer fails the test on any request: the commands under
// test must reject their input before they talk to the server.
func databagNoRequestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s: the input should be rejected first", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestDataBagCreateWithItemValidatesBeforeCreatingBag pins validate-then-act
// for `databag create BAG ITEM`: a bad --file, or an editor session the user
// abandons, must fail before the bag is created, not leave an empty bag
// behind.
func TestDataBagCreateWithItemValidatesBeforeCreatingBag(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for name, file := range map[string]string{
		"invalid JSON":   write("bad.json", `{"id": "x",`),
		"missing id":     write("noid.json", `{"password": "hunter2"}`),
		"file not found": filepath.Join(dir, "missing.json"),
	} {
		t.Run(name, func(t *testing.T) {
			srv := databagNoRequestServer(t)
			root := newRootCmd()
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			root.SetArgs([]string{"databag", "create", "secrets", "db-password", "--file", file,
				"--config", writeDataBagConfig(t, srv.URL)})
			if err := root.Execute(); err == nil {
				t.Fatal("expected an error for a bad item file")
			}
		})
	}

	t.Run("editor abandoned", func(t *testing.T) {
		srv := databagNoRequestServer(t)
		withStubDataBagItemEditor(t, func(cinc.DataBagItem) (cinc.DataBagItem, error) {
			return nil, errors.New("edit cancelled")
		})
		root := newRootCmd()
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs([]string{"databag", "create", "secrets", "db-password", "--config", writeDataBagConfig(t, srv.URL)})
		if err := root.Execute(); err == nil {
			t.Fatal("expected the editor's error")
		}
	})
}

// TestDataBagCreateRejectsFileWithoutItem checks that --file, which only
// means something in the two-argument form, is refused rather than silently
// ignored when no item is named.
func TestDataBagCreateRejectsFileWithoutItem(t *testing.T) {
	srv := databagNoRequestServer(t)
	file := filepath.Join(t.TempDir(), "item.json")
	if err := os.WriteFile(file, []byte(`{"id": "x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"databag", "create", "secrets", "--file", file, "--config", writeDataBagConfig(t, srv.URL)})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error for --file without an item")
	}
	if !strings.Contains(err.Error(), "--file") || !strings.Contains(err.Error(), "item") {
		t.Errorf("error should explain that --file needs an item: %v", err)
	}
}

// TestDataBagFileWithAnotherItemsIDIsRefused pins what happens when --file
// names a different item than the command line: every command that reads an
// item from a file refuses, naming both ids, before it sends anything. It
// used to store the file under the argument's id, which for an edit
// overwrites one item with the content meant for another.
func TestDataBagFileWithAnotherItemsIDIsRefused(t *testing.T) {
	secret := writeSecretFile(t, "s3cr3t")
	for name, args := range map[string][]string{
		"databag create":      {"databag", "create", "users", "alice"},
		"databag item create": {"databag", "item", "create", "users", "alice"},
		"databag item edit":   {"databag", "item", "edit", "users", "alice"},
		"secret create":       {"databag", "secret", "create", "users", "alice", "--secret-file", secret},
		"secret edit":         {"databag", "secret", "edit", "users", "alice", "--secret-file", secret},
	} {
		t.Run(name, func(t *testing.T) {
			srv := databagNoRequestServer(t)
			file := writeItemFile(t, cinc.DataBagItem{"id": "bob", "role": "admin"})
			root := newRootCmd()
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			root.SetArgs(append(args, "--file", file, "--config", writeDataBagConfig(t, srv.URL)))
			err := root.Execute()
			if err == nil {
				t.Fatal("expected the mismatched id to be refused")
			}
			if !strings.Contains(err.Error(), `"bob"`) || !strings.Contains(err.Error(), `"alice"`) {
				t.Errorf("error should name both ids: %v", err)
			}
		})
	}
}

// TestDataBagItemEditorKeepsTheID checks the editor's save validation: an
// edit that drops or changes the id is refused in the editor, where the user
// can fix it, rather than renaming the item.
func TestDataBagItemEditorKeepsTheID(t *testing.T) {
	validate := dataBagItemValidator("alice")
	for body, ok := range map[string]bool{
		`{"id": "alice", "role": "admin"}`: true,
		`{"id": "bob", "role": "admin"}`:   false,
		`{"role": "admin"}`:                false,
		`{"id": "alice",`:                  false,
		`null`:                             false,
	} {
		if err := validate([]byte(body)); (err == nil) != ok {
			t.Errorf("validate(%s) = %v, want ok=%v", body, err, ok)
		}
	}
}
