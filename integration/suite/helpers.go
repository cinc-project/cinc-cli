package suite

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// uniqueName returns "t-<kind>-<8 hex>". Every object a case creates gets one,
// so parallel cases, and reruns against a server that kept a failed run's
// objects, never collide. The characters are valid in every Chef object name.
func uniqueName(t *testing.T, kind string) string {
	t.Helper()
	return "t-" + kind + "-" + randomHex(t, 4)
}

// randomHex returns n random bytes as 2n lowercase hex characters.
func randomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("crypto/rand: %v", err)
	}
	return hex.EncodeToString(b)
}

// eventually retries check until it returns nil or timeout passes, then fails
// the case with check's last error. Search indexing on cinc-server-erlang is
// asynchronous; on cinc-server-ng the first attempt passes.
func eventually(t *testing.T, timeout time.Duration, check func() error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		err := check()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("still failing after %s: %v", timeout, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// searchTimeout bounds how long a case waits for an object to become
// searchable.
const searchTimeout = 30 * time.Second

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// writeJSON marshals v to a new temp file and returns its path, for the
// --file form of create and edit.
func writeJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "obj.json")
	writeFile(t, path, string(b))
	return path
}

// wantEqual fails the case unless got equals want.
func wantEqual[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

// wantSlice fails the case unless got equals want, element by element.
func wantSlice[T comparable](t *testing.T, what string, got, want []T) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}
