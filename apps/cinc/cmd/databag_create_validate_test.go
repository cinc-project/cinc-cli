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
