package resolver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cinc-project/cinc-cli/cli/policyfile/rubyeval"
)

// TestResolveMatchesChefGoldens is the core compatibility evidence: for every
// fixture under testdata/, cinc resolves the Policyfile.rb to a lock and asserts
// it is BYTE-IDENTICAL to the Policyfile.lock.json that real `chef install`
// wrote (see testdata/generate_goldens.sh). Any divergence — a different
// identifier, a different key order, a different byte of JSON formatting — fails
// the test, because such a lock would not interoperate with a Chef Infra Server.
func TestResolveMatchesChefGoldens(t *testing.T) {
	eng := rubyeval.NewEngine()
	if err := eng.Available(); err != nil {
		t.Skipf("ruby.wasm runtime unavailable (offline?): %v", err)
	}

	fixtures, err := filepath.Glob("testdata/*/Policyfile.rb")
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no fixtures found under testdata/")
	}

	for _, pf := range fixtures {
		dir := filepath.Dir(pf)
		name := filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			golden, err := os.ReadFile(filepath.Join(dir, "Policyfile.lock.json"))
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}

			eval, raw, err := eng.EvaluateFileWithRaw(context.Background(), pf)
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			res, err := Resolve(context.Background(), eng, eval, raw, dir)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}

			if string(res.LockJSON) != string(golden) {
				// Write cinc's output next to the golden for inspection.
				out := filepath.Join(t.TempDir(), "cinc.lock.json")
				_ = os.WriteFile(out, res.LockJSON, 0o644)
				t.Errorf("lock does not match chef golden (cinc %d bytes, golden %d bytes)\n%s",
					len(res.LockJSON), len(golden), firstDiff(res.LockJSON, golden))
			}
		})
	}
}

// firstDiff returns a short description of where two byte slices first diverge,
// to make golden mismatches easy to debug.
func firstDiff(a, b []byte) string {
	n := min(len(b), len(a))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			lo := max(i-40, 0)
			return "first difference at byte " + itoa(i) + ":\n  cinc:   ..." + safeSlice(a, lo, i+40) +
				"\n  golden: ..." + safeSlice(b, lo, i+40)
		}
	}
	if len(a) != len(b) {
		return "one is a prefix of the other; lengths differ"
	}
	return ""
}

func safeSlice(b []byte, lo, hi int) string {
	if hi > len(b) {
		hi = len(b)
	}
	if lo > len(b) {
		lo = len(b)
	}
	return string(b[lo:hi])
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
