package rubyeval

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPinnedConstants guards the download contract: a version, a URL that
// references the pinned version and asset, and a 64-hex-char SHA-256. These are
// the single source of truth for which ruby.wasm we run.
func TestPinnedConstants(t *testing.T) {
	if rubyWasmVersion == "" {
		t.Error("rubyWasmVersion must be pinned")
	}
	if !strings.Contains(rubyWasmURL, rubyWasmVersion) || !strings.Contains(rubyWasmURL, rubyWasmAsset) {
		t.Errorf("rubyWasmURL %q must reference the pinned version and asset", rubyWasmURL)
	}
	if len(rubyWasmSHA256) != 64 {
		t.Errorf("rubyWasmSHA256 must be 64 hex chars, got %d", len(rubyWasmSHA256))
	}
	if _, err := hex.DecodeString(rubyWasmSHA256); err != nil {
		t.Errorf("rubyWasmSHA256 is not valid hex: %v", err)
	}
}

func TestVerifySHA256(t *testing.T) {
	data := []byte("hello cinc")
	sum := sha256.Sum256(data)
	good := hex.EncodeToString(sum[:])

	if err := verifySHA256(data, good); err != nil {
		t.Errorf("matching checksum should pass: %v", err)
	}
	if err := verifySHA256(data, strings.Repeat("0", 64)); err == nil {
		t.Error("mismatched checksum must be rejected")
	}
}

// TestVerifyFileSHA256 proves the cache-hit re-verification helper accepts a
// file whose contents hash to the expected value and rejects a corrupted one —
// this is what guards against a cached ruby.wasm tampered-with after extraction
// (finding 5). It exercises the helper directly, no network or real blob.
func TestVerifyFileSHA256(t *testing.T) {
	content := []byte("\x00asm pretend this is the cached module")
	sum := sha256.Sum256(content)
	want := hex.EncodeToString(sum[:])

	path := filepath.Join(t.TempDir(), "ruby")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := verifyFileSHA256(path, want); err != nil {
		t.Errorf("a matching cached file should verify: %v", err)
	}

	// Corrupt the cached file in place; re-verification must now fail so the
	// caller re-downloads instead of executing a tampered module.
	if err := os.WriteFile(path, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := verifyFileSHA256(path, want)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch on a corrupted cache, got %v", err)
	}

	// A missing file is an error (so ensureRuntime falls through to re-fetch).
	if err := verifyFileSHA256(filepath.Join(t.TempDir(), "nope"), want); err == nil {
		t.Error("verifying a missing file should error")
	}
}

// TestPinnedBinaryChecksum guards the pinned module checksum is a well-formed
// SHA-256, distinct from the archive checksum.
func TestPinnedBinaryChecksum(t *testing.T) {
	if len(rubyWasmBinarySHA256) != 64 {
		t.Errorf("rubyWasmBinarySHA256 must be 64 hex chars, got %d", len(rubyWasmBinarySHA256))
	}
	if _, err := hex.DecodeString(rubyWasmBinarySHA256); err != nil {
		t.Errorf("rubyWasmBinarySHA256 is not valid hex: %v", err)
	}
	if rubyWasmBinarySHA256 == rubyWasmSHA256 {
		t.Error("rubyWasmBinarySHA256 (extracted module) must differ from rubyWasmSHA256 (archive)")
	}
}

// TestMaterializeRejectsBadChecksum proves a download whose bytes do not match
// the expected SHA is rejected and nothing is written into the cache dir — all
// without touching the network (the fetcher is injected).
func TestMaterializeRejectsBadChecksum(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "release")
	fetch := func(url string) ([]byte, error) { return []byte("not the real ruby.wasm"), nil }

	err := materializeFrom(dir, fetch, rubyWasmURL, rubyWasmSHA256)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch error, got %v", err)
	}
	if _, statErr := os.Stat(dir); statErr == nil {
		t.Error("cache dir must not be created on a checksum failure")
	}
}

// TestMaterializeExtractsVerifiedArchive proves the happy path: a tar.gz whose
// SHA matches is extracted into the cache dir. Uses a tiny crafted archive and
// its real checksum, so no network and no multi-MB blob are needed.
func TestMaterializeExtractsVerifiedArchive(t *testing.T) {
	archive := makeTarGz(t, map[string]string{
		"ruby-3.4-wasm32-unknown-wasip1-full/usr/local/bin/ruby":  "\x00asm fake",
		"ruby-3.4-wasm32-unknown-wasip1-full/usr/local/lib/x.txt": "lib",
	})
	sum := sha256.Sum256(archive)
	wantSHA := hex.EncodeToString(sum[:])

	dir := filepath.Join(t.TempDir(), "release")
	fetch := func(url string) ([]byte, error) { return archive, nil }
	if err := materializeFrom(dir, fetch, rubyWasmURL, wantSHA); err != nil {
		t.Fatalf("materialize: %v", err)
	}

	if !fileExists(filepath.Join(dir, rubyWasmTreeBinary)) {
		t.Error("expected the wasm binary to be extracted")
	}
	if !dirExists(filepath.Join(dir, rubyWasmTreeUsr)) {
		t.Error("expected the usr tree to be extracted")
	}
}

// TestExtractTarGzRejectsTraversal ensures a malicious "../" entry cannot
// escape the extraction directory.
func TestExtractTarGzRejectsTraversal(t *testing.T) {
	archive := makeTarGz(t, map[string]string{"../escape.txt": "pwned"})
	dir := t.TempDir()
	err := extractTarGz(archive, dir)
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("expected traversal rejection, got %v", err)
	}
}

func makeTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Typeflag: tar.TypeReg,
			Mode:     0o644,
			Size:     int64(len(content)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestExtractTarGzDoesNotFollowSymlinkOutOfDest covers the case the lexical
// check misses: "usr/evil" stays inside dest by string comparison, so a "usr"
// symlink already in dest redirects the write outside it.
func TestExtractTarGzDoesNotFollowSymlinkOutOfDest(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "usr")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	archive := makeTarGz(t, map[string]string{"usr/evil": "pwned"})
	if err := extractTarGz(archive, dir); err == nil {
		t.Error("extractTarGz wrote through a symlink in dest without complaint")
	}
	if _, err := os.Stat(filepath.Join(outside, "evil")); err == nil {
		t.Fatal("archive entry escaped dest through a pre-existing symlink")
	}
}

// seededRelease writes a fake extracted release under dir and returns the
// SHA-256 of its wasm module, so seed tests need no network and no real blob.
func seededRelease(t *testing.T, dir, module string) string {
	t.Helper()
	wasm := filepath.Join(dir, rubyWasmTreeBinary)
	if err := os.MkdirAll(filepath.Dir(wasm), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wasm, []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, rubyWasmTreeUsr, "local", "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(module))
	return hex.EncodeToString(sum[:])
}

func neverFetch(t *testing.T) fetcher {
	t.Helper()
	return func(url string) ([]byte, error) {
		t.Errorf("unexpected download of %s: a usable seed should never fetch", url)
		return nil, ErrRubyWasmUnavailable
	}
}

func TestEnsureRuntimeSeedPaths(t *testing.T) {
	const good = "\x00asm packaged"
	goodSHA := func(t *testing.T, dir string) string { return seededRelease(t, dir, good) }

	cases := []struct {
		name string
		// setup returns the seeds and the binary SHA to expect.
		setup       func(t *testing.T, base string) ([]seed, string)
		wantWasmIn  func(base string) string
		wantErr     string
		wantFetched bool
	}{
		{
			name: "explicit override is used without downloading",
			setup: func(t *testing.T, base string) ([]seed, string) {
				dir := filepath.Join(base, "override")
				return []seed{{dir: dir, required: true}}, goodSHA(t, dir)
			},
			wantWasmIn: func(base string) string { return filepath.Join(base, "override") },
		},
		{
			name: "packaged default is used without downloading",
			setup: func(t *testing.T, base string) ([]seed, string) {
				dir := filepath.Join(base, "packaged")
				return []seed{{dir: dir}}, goodSHA(t, dir)
			},
			wantWasmIn: func(base string) string { return filepath.Join(base, "packaged") },
		},
		{
			name: "an absent packaged default yields to the next source",
			setup: func(t *testing.T, base string) ([]seed, string) {
				dir := filepath.Join(base, "packaged")
				return []seed{{dir: filepath.Join(base, "absent")}, {dir: dir}}, goodSHA(t, dir)
			},
			wantWasmIn: func(base string) string { return filepath.Join(base, "packaged") },
		},
		{
			name: "a packaged default from another release is skipped, not executed",
			setup: func(t *testing.T, base string) ([]seed, string) {
				stale := filepath.Join(base, "packaged")
				seededRelease(t, stale, "\x00asm from an older release")
				fresh := filepath.Join(base, "fresh")
				return []seed{{dir: stale}, {dir: fresh}}, goodSHA(t, fresh)
			},
			wantWasmIn: func(base string) string { return filepath.Join(base, "fresh") },
		},
		{
			name: "an explicit override that is not a release is reported",
			setup: func(t *testing.T, base string) ([]seed, string) {
				dir := filepath.Join(base, "typo")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				return []seed{{dir: dir, required: true}}, rubyWasmBinarySHA256
			},
			wantErr: "is not a complete release",
		},
		{
			name: "an explicit override from another release is reported",
			setup: func(t *testing.T, base string) ([]seed, string) {
				dir := filepath.Join(base, "stale")
				seededRelease(t, dir, "\x00asm from an older release")
				return []seed{{dir: dir, required: true}}, rubyWasmBinarySHA256
			},
			wantErr: "does not match the pinned release",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			seeds, binarySHA := tc.setup(t, base)
			cache := filepath.Join(base, "cache")
			src := runtimeSource{seeds: seeds, cacheDir: cache, binarySHA: binarySHA}

			rt, err := ensureRuntimeFrom(src, neverFetch(t))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want one containing %q", err, tc.wantErr)
				}
				// A broken install is a runtime problem, not a Policyfile one.
				if !IsUnavailable(err) {
					t.Errorf("seed error should wrap ErrRubyWasmUnavailable, got %v", err)
				}
				// And a configured-but-wrong directory is not a network
				// problem, so the user is not told to get online.
				if !IsMisconfigured(err) {
					t.Errorf("seed error should wrap ErrRubyWasmMisconfigured, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ensureRuntimeFrom: %v", err)
			}
			if want := filepath.Join(tc.wantWasmIn(base), rubyWasmTreeBinary); rt.wasmPath != want {
				t.Errorf("wasmPath = %s, want %s", rt.wasmPath, want)
			}
			if dirExists(cache) {
				t.Error("a seeded runtime must not populate the per-user cache")
			}
		})
	}
}

// A packaged runtime has to work where there is no home directory, which
// is most of what a packaged runtime is for.
func TestEnsureRuntimeUsesSeedWhenTheCacheDirIsUnavailable(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "packaged")
	binarySHA := seededRelease(t, dir, "\x00asm packaged")

	src := runtimeSource{
		seeds:     []seed{{dir: dir}},
		cacheErr:  errors.New("neither $XDG_CACHE_HOME nor $HOME are defined"),
		binarySHA: binarySHA,
	}
	if _, err := ensureRuntimeFrom(src, neverFetch(t)); err != nil {
		t.Fatalf("a seeded runtime should not need a cache directory: %v", err)
	}
}

func TestEnsureRuntimeReportsCacheDirFailureWhenNoSeedApplies(t *testing.T) {
	src := runtimeSource{cacheErr: errors.New("neither $XDG_CACHE_HOME nor $HOME are defined")}
	_, err := ensureRuntimeFrom(src, neverFetch(t))
	if err == nil || !strings.Contains(err.Error(), "$HOME") {
		t.Fatalf("error = %v, want the cache-directory failure", err)
	}
	if !IsUnavailable(err) {
		t.Errorf("should wrap ErrRubyWasmUnavailable, got %v", err)
	}
	if IsMisconfigured(err) {
		t.Error("a missing cache directory is not a configured-runtime problem")
	}
}

func TestEnsureRuntimeDownloadsFromOverriddenURL(t *testing.T) {
	module := "\x00asm mirrored"
	archive := makeTarGz(t, map[string]string{
		rubyWasmTreeBinary:                  module,
		rubyWasmTreeUsr + "/local/lib/x.rb": "lib",
	})
	archiveSum := sha256.Sum256(archive)
	moduleSum := sha256.Sum256([]byte(module))

	var fetched string
	fetch := func(url string) ([]byte, error) {
		fetched = url
		return archive, nil
	}
	src := runtimeSource{
		cacheDir:   filepath.Join(t.TempDir(), "cache"),
		url:        "https://mirror.example.test/ruby.wasm.tar.gz",
		archiveSHA: hex.EncodeToString(archiveSum[:]),
		binarySHA:  hex.EncodeToString(moduleSum[:]),
	}
	if _, err := ensureRuntimeFrom(src, fetch); err != nil {
		t.Fatalf("ensureRuntimeFrom: %v", err)
	}
	if fetched != src.url {
		t.Errorf("fetched %q, want the overridden URL %q", fetched, src.url)
	}
}

// Drift between the archive pin and the module pin must be reported, not
// turned into a cache that misses on every run and re-downloads forever.
func TestEnsureRuntimeReportsModuleMismatchAfterDownload(t *testing.T) {
	archive := makeTarGz(t, map[string]string{
		rubyWasmTreeBinary:                  "\x00asm downloaded",
		rubyWasmTreeUsr + "/local/lib/x.rb": "lib",
	})
	archiveSum := sha256.Sum256(archive)

	src := runtimeSource{
		cacheDir:   filepath.Join(t.TempDir(), "cache"),
		url:        rubyWasmURL,
		archiveSHA: hex.EncodeToString(archiveSum[:]),
		binarySHA:  rubyWasmBinarySHA256, // deliberately not the module above
	}
	_, err := ensureRuntimeFrom(src, func(string) ([]byte, error) { return archive, nil })
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("error = %v, want a checksum mismatch", err)
	}
	if !IsUnavailable(err) {
		t.Errorf("should wrap ErrRubyWasmUnavailable, got %v", err)
	}
}

func TestDefaultRuntimeSourceHonorsEnvironment(t *testing.T) {
	t.Setenv(envRuntimeDir, "/srv/ruby-wasm")
	t.Setenv(envRuntimeURL, "https://mirror.example.test/ruby.wasm.tar.gz")
	prev := packagedRuntimeDir
	packagedRuntimeDir = "/opt/cinc/share/ruby-wasm"
	t.Cleanup(func() { packagedRuntimeDir = prev })

	src := defaultRuntimeSource()
	want := []seed{{dir: "/srv/ruby-wasm", required: true}, {dir: "/opt/cinc/share/ruby-wasm"}}
	if len(src.seeds) != len(want) {
		t.Fatalf("seeds = %+v, want %+v", src.seeds, want)
	}
	for i := range want {
		if src.seeds[i] != want[i] {
			t.Errorf("seeds[%d] = %+v, want %+v", i, src.seeds[i], want[i])
		}
	}
	if src.url != "https://mirror.example.test/ruby.wasm.tar.gz" {
		t.Errorf("url = %q, want the env override", src.url)
	}
	if src.archiveSHA != rubyWasmSHA256 || src.binarySHA != rubyWasmBinarySHA256 {
		t.Error("overrides must never relax the pinned checksums")
	}
}

func TestDefaultRuntimeSourceWithoutOverrides(t *testing.T) {
	t.Setenv(envRuntimeDir, "")
	t.Setenv(envRuntimeURL, "")
	prev := packagedRuntimeDir
	packagedRuntimeDir = ""
	t.Cleanup(func() { packagedRuntimeDir = prev })

	src := defaultRuntimeSource()
	if len(src.seeds) != 0 {
		t.Errorf("seeds = %+v, want none", src.seeds)
	}
	if src.url != rubyWasmURL {
		t.Errorf("url = %q, want the pinned release URL", src.url)
	}
}
