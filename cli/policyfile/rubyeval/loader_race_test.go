package rubyeval

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// fakeRelease is a tiny archive laid out like the pinned release, with the
// runtimeSource that pins it.
func fakeRelease(t *testing.T, module string) ([]byte, runtimeSource) {
	t.Helper()
	archive := makeTarGz(t, map[string]string{
		rubyWasmTreeBinary:                  module,
		rubyWasmTreeUsr + "/local/lib/x.rb": "lib",
	})
	archiveSum := sha256.Sum256(archive)
	moduleSum := sha256.Sum256([]byte(module))
	return archive, runtimeSource{
		cacheDir:   filepath.Join(t.TempDir(), "cache", rubyWasmVersion),
		url:        rubyWasmURL,
		archiveSHA: hex.EncodeToString(archiveSum[:]),
		binarySHA:  hex.EncodeToString(moduleSum[:]),
	}
}

// TestEnsureRuntimeConcurrentFirstRuns is several first runs at once (two
// `cinc policy install`s, or parallel tests): every one downloads, and all
// but the first to finish find the release already in place. Each must use
// that release rather than fail with "file exists" when moving its own copy
// in.
func TestEnsureRuntimeConcurrentFirstRuns(t *testing.T) {
	const n = 8
	archive, src := fakeRelease(t, "\x00asm concurrent")
	// Hold every download until all have started, so they finish together.
	var started sync.WaitGroup
	started.Add(n)
	fetch := func(string) ([]byte, error) {
		started.Done()
		started.Wait()
		return archive, nil
	}

	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rt, err := ensureRuntimeFrom(src, fetch)
			if err == nil && !fileExists(rt.wasmPath) {
				err = os.ErrNotExist
			}
			errs[i] = err
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("run %d: %v", i, err)
		}
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(src.cacheDir), "ruby-wasm-*"))
	if len(leftovers) != 0 {
		t.Errorf("staging directories left behind: %v", leftovers)
	}
}

// TestEnsureRuntimeKeepsTheCompilationCache: the compiled-module cache lives
// in the same version directory, and a process that already has it (a run
// that used CINC_RUBY_WASM_DIR, or one compiling right now) must not lose it
// when another run downloads the release.
func TestEnsureRuntimeKeepsTheCompilationCache(t *testing.T) {
	archive, src := fakeRelease(t, "\x00asm keep")
	compiled := filepath.Join(src.cacheDir, "wazero-compilation-cache", "entry")
	if err := os.MkdirAll(filepath.Dir(compiled), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(compiled, []byte("compiled"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ensureRuntimeFrom(src, func(string) ([]byte, error) { return archive, nil }); err != nil {
		t.Fatalf("ensureRuntimeFrom: %v", err)
	}
	if !fileExists(compiled) {
		t.Error("downloading the release deleted the compilation cache")
	}
}

// TestEnsureRuntimeReplacesABrokenCachedRelease keeps the self-healing a
// cache hit relies on: a cached module that no longer matches its pin is
// replaced by a fresh download, not executed and not left in the way.
func TestEnsureRuntimeReplacesABrokenCachedRelease(t *testing.T) {
	archive, src := fakeRelease(t, "\x00asm good")
	stale := filepath.Join(src.cacheDir, rubyWasmTreeBinary)
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("\x00asm tampered"), 0o600); err != nil {
		t.Fatal(err)
	}

	rt, err := ensureRuntimeFrom(src, func(string) ([]byte, error) { return archive, nil })
	if err != nil {
		t.Fatalf("ensureRuntimeFrom: %v", err)
	}
	if err := verifyFileSHA256(rt.wasmPath, src.binarySHA); err != nil {
		t.Errorf("the broken module is still in place: %v", err)
	}
}
