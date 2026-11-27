package cmd

import (
	"bytes"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeTemp writes body to name in a fresh temp dir and returns its path.
func writeTemp(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func wantAll(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error mentioning %q", wants)
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got:\n%s", want, err)
		}
	}
	if strings.Contains(err.Error(), "—") {
		t.Errorf("error should not use an em dash, got:\n%s", err)
	}
}

func TestReadPublicKeyAcceptsPEM(t *testing.T) {
	pem := "-----BEGIN PUBLIC KEY-----\nMIIBIjAN\n-----END PUBLIC KEY-----\n"
	got, err := readPublicKey(writeTemp(t, "k.pub", pem), "laptop")
	if err != nil || got != pem {
		t.Fatalf("readPublicKey = %q, %v; want the file as-is", got, err)
	}
}

func TestReadPublicKeyMissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.pub")
	_, err := readPublicKey(missing, "laptop")
	wantAll(t, err, missing, "--public-key", "openssl rsa -in laptop.pem -pubout -out laptop.pub", "leave --public-key off")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error should still wrap fs.ErrNotExist: %v", err)
	}
}

func TestReadPublicKeyUnreadableFile(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("file modes don't block reads here")
	}
	path := writeTemp(t, "k.pub", "x")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	_, err := readPublicKey(path, "laptop")
	wantAll(t, err, path, "permission")
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("error should still wrap fs.ErrPermission: %v", err)
	}
}

func TestReadPublicKeyRefusesAPrivateKey(t *testing.T) {
	path := writeTemp(t, "k.pem", "-----BEGIN RSA PRIVATE KEY-----\nMIIE\n-----END RSA PRIVATE KEY-----\n")
	_, err := readPublicKey(path, "laptop")
	wantAll(t, err, path, "private key", "didn't send it", "openssl rsa -in "+path+" -pubout -out laptop.pub")
}

func TestReadPublicKeyExplainsOpenSSHFormat(t *testing.T) {
	path := writeTemp(t, "id_rsa.pub", "ssh-rsa AAAAB3NzaC1yc2E me@laptop\n")
	_, err := readPublicKey(path, "laptop")
	wantAll(t, err, path, "OpenSSH", "-----BEGIN PUBLIC KEY-----", "ssh-keygen -e -m PKCS8 -f "+path+" > laptop.pub")
}

func TestReadPublicKeyExplainsNonRSASSHKey(t *testing.T) {
	path := writeTemp(t, "id_ed25519.pub", "ssh-ed25519 AAAAC3Nza me@laptop\n")
	_, err := readPublicKey(path, "laptop")
	wantAll(t, err, path, "RSA")
}

func TestReadPublicKeyRefusesGarbage(t *testing.T) {
	path := writeTemp(t, "bad.pub", "this is not a public key\n")
	_, err := readPublicKey(path, "laptop")
	wantAll(t, err, path, "doesn't look like a public key", "-----BEGIN PUBLIC KEY-----", "openssl")
}

// A bad --public-key must fail before anything reaches the server.
func TestClientCreateBadPublicKeyNeverCallsServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	bad := writeTemp(t, "bad.pub", "nope")
	root := newRootCmd()
	root.SetOut(new(bytes.Buffer))
	root.SetArgs([]string{"client", "create", "worker-01", "--public-key", bad, "--config", writeCreateConfig(t, srv.URL)})
	err := root.Execute()
	wantAll(t, err, bad, "doesn't look like a public key", "worker-01.pub")
}

func TestKeyFileDestinationCheckedBeforeTheServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	cfg := writeCreateConfig(t, srv.URL)

	noDir := filepath.Join(t.TempDir(), "missing", "worker-01.pem")
	isDir := t.TempDir()
	for _, tc := range []struct {
		args  []string
		wants []string
	}{
		{[]string{"client", "create", "worker-01", "--key-file", noDir},
			[]string{noDir, "doesn't exist", "mkdir -p " + filepath.Dir(noDir), "nothing has changed"}},
		{[]string{"client", "reregister", "worker-01", "--key-file", isDir},
			[]string{isDir, "is a folder", "nothing has changed"}},
		{[]string{"client", "key", "create", "worker-01", "k", "--key-file", noDir}, []string{noDir}},
		{[]string{"user", "create", "alice", "--key-file", noDir}, []string{noDir}},
		{[]string{"org", "create", "acme", "Acme", "--filename", noDir}, []string{noDir, "--filename"}},
	} {
		root := newRootCmd()
		root.SetOut(new(bytes.Buffer))
		root.SetArgs(append(tc.args, "--config", cfg))
		wantAll(t, root.Execute(), tc.wants...)
	}
}

// When the key can't be saved after the server made it, the user must
// learn the key is lost and how to get a new one.
func TestEmitPrivateKeyWriteFailureExplainsRecovery(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gone")
	cmd := fakeCmd("", "", "", new(bytes.Buffer))
	err := emitPrivateKey(cmd, `Created client "worker-01"`, "key", "PRIVATE", filepath.Join(dir, "k.pem"),
		"run `cinc client reregister worker-01 --key-file <path>` to make a new one")
	wantAll(t, err, `Created client "worker-01"`, filepath.Join(dir, "k.pem"), "doesn't exist",
		"only sends a new private key once", "cinc client reregister worker-01 --key-file <path>")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error should still wrap fs.ErrNotExist: %v", err)
	}
}

func TestClientReregisterWithoutAKeyExplains(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"worker-01"}`))
	}))
	defer srv.Close()
	root := newRootCmd()
	root.SetOut(new(bytes.Buffer))
	root.SetArgs([]string{"client", "reregister", "worker-01", "--config", writeCreateConfig(t, srv.URL)})
	wantAll(t, root.Execute(), `"worker-01"`, "didn't send back a new private key", "cinc client key list worker-01")
}

func TestUserPasswordEmptyExplains(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	root := newRootCmd()
	root.SetOut(new(bytes.Buffer))
	root.SetIn(strings.NewReader("\n"))
	root.SetArgs([]string{"user", "password", "alice", "--config", writeCreateConfig(t, srv.URL)})
	wantAll(t, root.Execute(), "password can't be empty", `"alice"`, "--password")
}
