package cmd

import (
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/cinc-project/cinc-cli/cli/progname"
)

// readPublicKey reads the PEM public key a --public-key flag points at.
// stem names the example files in the "make one" hints (worker-01 gives
// worker-01.pem and worker-01.pub). It refuses what the server would
// reject anyway (no PEM block, an OpenSSH key), and refuses a private key
// outright, since that must never leave the machine. Other PEM types
// pass through for the server to judge. Read errors keep wrapping the
// fs error, so errors.Is(err, fs.ErrNotExist) still works.
func readPublicKey(path, stem string) (string, error) {
	data, err := os.ReadFile(path)
	makeOne := fmt.Sprintf(`If you don't have a key pair yet, leave --public-key off and the server will make one for you. Or make one yourself:
  openssl genrsa -out %[1]s.pem 2048
  openssl rsa -in %[1]s.pem -pubout -out %[1]s.pub
then pass --public-key %[1]s.pub. Keep %[1]s.pem private: it's the key you'll sign requests with.`, stem)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("we can't find the public key file you gave --public-key, %s. Check the path.\n\n%s\n(%w)", path, makeOne, err)
	case errors.Is(err, fs.ErrPermission):
		return "", fmt.Errorf("we can't read the public key file %s: you don't have permission to open it. Make it readable with `chmod 644 %s`, or copy it somewhere you can read. (%w)", path, path, err)
	case err != nil:
		return "", fmt.Errorf("we couldn't read the public key file %s that you gave --public-key: %w", path, err)
	}

	text := strings.TrimSpace(string(data))
	block, _ := pem.Decode(data)
	switch {
	case block != nil && strings.Contains(block.Type, "PRIVATE KEY"):
		return "", fmt.Errorf(`%s is a private key, not a public key, so we didn't send it. A private key should never leave your machine.

Pull the public half out of it and pass that instead:
  openssl rsa -in %s -pubout -out %s.pub
  %s ... --public-key %s.pub`, path, path, stem, progname.Get(), stem)
	case block != nil:
		return string(data), nil
	case strings.HasPrefix(text, "ssh-rsa "):
		return "", fmt.Errorf(`%s is an OpenSSH public key, but the Cinc server only takes keys in PEM format, the kind that starts with -----BEGIN PUBLIC KEY-----.

Convert it, then pass the new file to --public-key:
  ssh-keygen -e -m PKCS8 -f %s > %s.pub`, path, path, stem)
	case strings.HasPrefix(text, "ssh-") || strings.HasPrefix(text, "ecdsa-"):
		return "", fmt.Errorf("%s is an OpenSSH key that isn't RSA, and the Cinc server only accepts RSA keys in PEM format (a file that starts with -----BEGIN PUBLIC KEY-----).\n\n%s", path, makeOne)
	}
	return "", fmt.Errorf("%s doesn't look like a public key. The Cinc server expects an RSA public key in PEM format, a text file that starts with -----BEGIN PUBLIC KEY-----.\n\n%s", path, makeOne)
}

// checkKeyFileDest checks, before anything reaches the server, that a
// private key can be saved at path (named by flag, such as --key-file).
// The server sends a new private key exactly once, so finding out after
// it made the key means the key is lost.
func checkKeyFileDest(flag, path string) error {
	if path == "" {
		return nil
	}
	const unchanged = "We checked before asking the server for anything, so nothing has changed yet."
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return fmt.Errorf("%s %s is a folder. Give it the path of the file to save the private key in, like %s. %s",
			flag, path, filepath.Join(path, "key.pem"), unchanged)
	}
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("we can't save the private key to %s, because the folder %s doesn't exist. Create it with `mkdir -p %s`, or give %s a different path. %s",
			path, dir, dir, flag, unchanged)
	}
	return nil
}

// keyWriteError explains a private key the server already made but that
// couldn't be written to path. recovery finishes the sentence "To get a
// usable key, fix that and ...".
func keyWriteError(done, label, path, recovery string, err error) error {
	reason := err.Error()
	switch {
	case errors.Is(err, fs.ErrNotExist):
		reason = fmt.Sprintf("the folder %s doesn't exist", filepath.Dir(path))
	case errors.Is(err, fs.ErrPermission):
		reason = "you don't have permission to write there"
	}
	return fmt.Errorf(`%s, but we couldn't save its %s to %s: %s.

The server only sends a new private key once, so that copy is gone. To get a usable key, fix that and %s.
(%w)`, done, label, path, reason, recovery, err)
}

// writeKeyFile writes a private key to path, mode 0600. It opens with
// O_TRUNC (not O_EXCL) so reregister and friends can overwrite an existing
// key, then forces 0600 explicitly: a pre-existing file would otherwise keep
// its looser mode, and the create mode is masked by umask. A private key
// must never be group- or world-readable.
func writeKeyFile(path, priv string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close() // already returning an error
		return err
	}
	if _, err := f.WriteString(priv); err != nil {
		_ = f.Close() // already returning an error
		return err
	}
	return f.Close()
}
