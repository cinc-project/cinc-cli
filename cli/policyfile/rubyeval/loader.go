package rubyeval

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// The Policyfile evaluator runs CRuby compiled to WebAssembly (the official
// ruby/ruby.wasm WASI build) under wazero. We deliberately do NOT commit the
// multi-megabyte wasm blob to git. Instead we download a PINNED release the
// first time the engine runs, verify its SHA-256 against the constant below,
// and cache the extracted tree under the OS cache dir — mirroring how
// test/acceptance/helpers_test.go caches a pinned cinc-zero.
//
// Productionizing this would vendor the blob (go:embed of an LFS/release
// artifact) so an offline build still works; the download-cache is the
// deliberate tradeoff for this PR. The pinned constants are the single source
// of truth and are asserted by loader_test.go without touching the network.
const (
	// rubyWasmVersion is the pinned ruby/ruby.wasm release tag.
	rubyWasmVersion = "2.9.4"
	// rubyWasmAsset is the WASI "full" build (CRuby 3.4 + stdlib) we run. The
	// "full" build ships the standard library as host files we mount, not
	// packed into the module.
	rubyWasmAsset = "ruby-3.4-wasm32-unknown-wasip1-full.tar.gz"
	// rubyWasmURL is the release download URL for the pinned asset.
	rubyWasmURL = "https://github.com/ruby/ruby.wasm/releases/download/" + rubyWasmVersion + "/" + rubyWasmAsset
	// rubyWasmSHA256 is the verified SHA-256 of rubyWasmAsset. A mismatch is a
	// hard failure (a corrupted or tampered download is never used).
	rubyWasmSHA256 = "ccda86a375a4fe09849846d3b03a370172a4902a0c571087f48457388a2762c7"
	// rubyWasmBinarySHA256 is the SHA-256 of the CRuby wasm module extracted
	// from the pinned, checksum-verified archive (rubyWasmTreeBinary). It is
	// re-checked on every cache hit so a cached module tampered-with after
	// extraction is rejected and re-fetched, not executed. Because it's derived
	// deterministically from the pinned archive, anyone can reproduce it by
	// extracting rubyWasmAsset.
	rubyWasmBinarySHA256 = "ea1ccf46994cd2441812c75fb058136850149f2a472ff4472f7085b086fd1d1a"

	// rubyWasmTreeBinary is the path, within the extracted archive, of the
	// CRuby wasm module.
	rubyWasmTreeBinary = "ruby-3.4-wasm32-unknown-wasip1-full/usr/local/bin/ruby"
	// rubyWasmTreeUsr is the path, within the extracted archive, of the /usr
	// tree we mount at the guest's /usr so CRuby finds its standard library.
	rubyWasmTreeUsr = "ruby-3.4-wasm32-unknown-wasip1-full/usr"
)

// ErrRubyWasmUnavailable wraps any failure to obtain the pinned ruby.wasm
// (no network, GitHub unreachable, etc.). Tests and the CLI treat it as a
// "skip cleanly / explain" signal rather than a hard error, so a network-less
// CI still passes.
var ErrRubyWasmUnavailable = errors.New("policyfile: ruby.wasm runtime is unavailable")

// ErrRubyWasmMisconfigured marks the subset of ErrRubyWasmUnavailable
// caused by a runtime directory that was deliberately configured but
// cannot be used. Callers use it to avoid telling the user to get network
// access when the fix is to correct the directory.
var ErrRubyWasmMisconfigured = errors.New("configured ruby.wasm runtime is unusable")

// runtimeFiles points at the on-disk pieces of an extracted ruby.wasm release.
type runtimeFiles struct {
	// wasmPath is the CRuby wasm module.
	wasmPath string
	// usrDir is the host directory mounted at the guest /usr (CRuby stdlib).
	usrDir string
}

// fetcher retrieves the bytes at a URL. It is a field so tests can inject a
// canned archive and exercise the checksum/extract logic without a network.
type fetcher func(url string) ([]byte, error)

// httpGetBytes downloads url with a generous timeout, returning the body or an
// error wrapping ErrRubyWasmUnavailable so callers can skip rather than fail.
func httpGetBytes(url string) ([]byte, error) {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRubyWasmUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: GET %s: %s", ErrRubyWasmUnavailable, url, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRubyWasmUnavailable, err)
	}
	return body, nil
}

// cacheDir returns the directory the extracted ruby.wasm release is cached in.
func cacheDir() (string, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "cinc-cli", "ruby-wasm", rubyWasmVersion), nil
}

// packagedRuntimeDir optionally names a directory holding the pinned
// release already extracted, for distributions that ship the runtime inside
// their package so an installed cinc never downloads it. It is set at build
// time:
//
//	go build -ldflags "-X github.com/cinc-project/cinc-cli/cli/policyfile/rubyeval.packagedRuntimeDir=/opt/cinc/share/ruby-wasm"
//
// The directory holds the release as the archive extracts it, so it
// contains the top-level release directory (the same layout as the cache
// directory). It is only ever read, and its module is checksum-verified
// before it runs.
//
// Because a build-time default cannot know whether the payload shipped, an
// unusable packaged directory falls through to the cache and the download
// rather than failing: a package that omits or is mid-upgrade on the
// payload still works, just without the offline shortcut.
var packagedRuntimeDir string

// Environment overrides, for operators rather than packagers:
//
//   - CINC_RUBY_WASM_DIR names an already-extracted release, same layout as
//     packagedRuntimeDir. Unlike the packaged directory this is an explicit
//     instruction, so if it cannot be used that is reported rather than
//     quietly replaced by a download.
//   - CINC_RUBY_WASM_URL replaces the download URL, so an air-gapped site
//     can serve the pinned archive from a mirror. The checksum is still
//     enforced, so the mirror has to serve the exact pinned release.
const (
	envRuntimeDir = "CINC_RUBY_WASM_DIR"
	envRuntimeURL = "CINC_RUBY_WASM_URL"
)

// seed is a pre-extracted release to use instead of downloading.
type seed struct {
	dir string
	// required says an unusable directory is an error rather than a
	// fall-through. It is set for an operator's explicit override and
	// clear for the build-time packaged default.
	required bool
}

// runtimeSource says where ensureRuntime looks for the pinned release: the
// seeds in order, then the per-user cache, which is populated from url on a
// miss. cacheErr defers a cache-directory failure so a usable seed is still
// reachable on a machine with no home directory.
type runtimeSource struct {
	seeds      []seed
	cacheDir   string
	cacheErr   error
	url        string
	archiveSHA string
	binarySHA  string
}

// defaultRuntimeSource builds the source from the pinned constants, the
// build-time packaged directory, and the environment overrides.
func defaultRuntimeSource() runtimeSource {
	src := runtimeSource{
		url:        rubyWasmURL,
		archiveSHA: rubyWasmSHA256,
		binarySHA:  rubyWasmBinarySHA256,
	}
	if dir := os.Getenv(envRuntimeDir); dir != "" {
		src.seeds = append(src.seeds, seed{dir: dir, required: true})
	}
	if packagedRuntimeDir != "" {
		src.seeds = append(src.seeds, seed{dir: packagedRuntimeDir})
	}
	if url := os.Getenv(envRuntimeURL); url != "" {
		src.url = url
	}
	// Resolved, not returned: a seeded runtime never needs the cache, and
	// the environments a packaged runtime exists for (systemd units,
	// containers, cron) are exactly the ones with no HOME.
	src.cacheDir, src.cacheErr = cacheDir()
	return src
}

// filesIn returns the runtime file locations for an extracted release
// rooted at dir.
func filesIn(dir string) runtimeFiles {
	return runtimeFiles{
		wasmPath: filepath.Join(dir, rubyWasmTreeBinary),
		usrDir:   filepath.Join(dir, rubyWasmTreeUsr),
	}
}

// ensureRuntime returns the on-disk wasm + stdlib for the pinned release,
// downloading, verifying, and extracting it once per machine unless a
// seeded copy is available. fetch defaults to httpGetBytes; tests may pass
// their own.
func ensureRuntime(fetch fetcher) (runtimeFiles, error) {
	return ensureRuntimeFrom(defaultRuntimeSource(), fetch)
}

// seedError reports why a seed directory could not be used. Callers treat
// it as a runtime-acquisition failure, so it wraps ErrRubyWasmUnavailable
// like every other failure in this file: a broken install is not the
// user's Policyfile being wrong.
func seedError(dir, reason string) error {
	return fmt.Errorf("%w: %w: the ruby.wasm runtime at %s %s; it needs the extracted %s release, which contains %s and %s",
		ErrRubyWasmUnavailable, ErrRubyWasmMisconfigured, dir, reason, rubyWasmVersion, rubyWasmTreeBinary, rubyWasmTreeUsr)
}

// ensureRuntimeFrom is ensureRuntime with an injectable source, so the seed
// and cache paths are testable without the real multi-megabyte blob.
func ensureRuntimeFrom(src runtimeSource, fetch fetcher) (runtimeFiles, error) {
	if fetch == nil {
		fetch = httpGetBytes
	}
	for _, s := range src.seeds {
		rt := filesIn(s.dir)
		haveWasm, haveUsr := fileExists(rt.wasmPath), dirExists(rt.usrDir)
		if !haveWasm || !haveUsr {
			if s.required {
				// An operator named this directory, so a layout that
				// cannot be used is reported instead of being replaced
				// by the download they were trying to avoid.
				return runtimeFiles{}, seedError(s.dir, "is not a complete release")
			}
			continue
		}
		if err := verifyFileSHA256(rt.wasmPath, src.binarySHA); err != nil {
			if s.required {
				return runtimeFiles{}, seedError(s.dir, "does not match the pinned release")
			}
			// A packaged payload from a different release is skipped, not
			// executed, so a binary upgraded ahead of its payload still
			// works by falling back to the cache or a download.
			continue
		}
		return rt, nil
	}

	if src.cacheErr != nil {
		return runtimeFiles{}, fmt.Errorf("%w: %v", ErrRubyWasmUnavailable, src.cacheErr)
	}
	dir := src.cacheDir
	rt := filesIn(dir)
	// A usable release is complete and its module matches the pin. Checking
	// the module on a cache hit means one tampered with since extraction is
	// re-downloaded rather than executed.
	usable := func() bool {
		return fileExists(rt.wasmPath) && dirExists(rt.usrDir) &&
			verifyFileSHA256(rt.wasmPath, src.binarySHA) == nil
	}
	if usable() {
		return rt, nil
	}
	if err := materializeFrom(dir, fetch, src.url, src.archiveSHA, usable); err != nil {
		return runtimeFiles{}, err
	}
	if !fileExists(rt.wasmPath) || !dirExists(rt.usrDir) {
		return runtimeFiles{}, fmt.Errorf("policyfile: extracted ruby.wasm release missing expected files under %s", dir)
	}
	// Verify what we just extracted, so a drift between the archive and
	// module pins is a reported error rather than an invisible cache miss
	// that re-downloads on every run forever.
	if err := verifyFileSHA256(rt.wasmPath, src.binarySHA); err != nil {
		return runtimeFiles{}, fmt.Errorf("%w: %v", ErrRubyWasmUnavailable, err)
	}
	return rt, nil
}

// materializeFrom downloads the archive at url via fetch, verifies it
// against wantSHA, and extracts it into dir. The URL and checksum are
// injectable so the mirror override and the verified-extract path are
// testable without the real multi-megabyte blob. A checksum mismatch is
// rejected before anything is written into dir.
//
// Several processes can download at once (parallel first runs of `cinc
// policy install`, or parallel tests). The archive is extracted into a
// private staging directory and each of its top-level entries is renamed
// into dir, so a finished release appears atomically. usable reports
// whether dir already holds a good release: when another process got there
// first, its copy is used and this one discarded. dir itself is never
// removed, because it also holds the compiled-module cache that other
// processes may be using.
func materializeFrom(dir string, fetch fetcher, url, wantSHA string, usable func() bool) error {
	archive, err := fetch(url)
	if err != nil {
		return err
	}
	if err := verifySHA256(archive, wantSHA); err != nil {
		return err
	}

	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, "ruby-wasm-staging-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)

	if err := extractTarGz(archive, staging); err != nil {
		return err
	}
	if usable() {
		return nil // another process finished while this one downloaded
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(staging)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := moveIntoPlace(filepath.Join(staging, e.Name()), filepath.Join(dir, e.Name()), parent, usable); err != nil {
			return fmt.Errorf("policyfile: finalize ruby.wasm cache: %w", err)
		}
	}
	return nil
}

// moveIntoPlace renames src to dst. When dst is already there it is either
// another process's finished copy (usable reports true, and it is kept) or
// a broken earlier one, which is moved aside into scratch before src
// replaces it. It is never deleted in place, since a process may be reading
// it.
func moveIntoPlace(src, dst, scratch string, usable func() bool) error {
	err := os.Rename(src, dst)
	if err == nil || usable() {
		return nil
	}
	if _, statErr := os.Lstat(dst); statErr != nil {
		return err // nothing in the way, so the rename failed for another reason
	}
	aside, asideErr := os.MkdirTemp(scratch, "ruby-wasm-stale-*")
	if asideErr != nil {
		return asideErr
	}
	defer os.RemoveAll(aside)
	if err := os.Rename(dst, filepath.Join(aside, filepath.Base(dst))); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(src, dst); err != nil && !usable() {
		return err
	}
	return nil
}

// verifySHA256 confirms data hashes to wantHex, returning a clear error on a
// mismatch so a tampered or truncated download is never trusted.
func verifySHA256(data []byte, wantHex string) error {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if got != wantHex {
		return fmt.Errorf("policyfile: ruby.wasm checksum mismatch: got %s, want %s", got, wantHex)
	}
	return nil
}

// verifyFileSHA256 streams the file at path and confirms it hashes to wantHex,
// returning a clear error on a mismatch (or if the file can't be read). Used to
// re-verify a cached artifact on a cache hit without loading it fully into
// memory.
func verifyFileSHA256(path, wantHex string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }() // read handle
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != wantHex {
		return fmt.Errorf("policyfile: cached ruby.wasm checksum mismatch: got %s, want %s", got, wantHex)
	}
	return nil
}

// extractTarGz unpacks a .tar.gz archive under dest. It guards against path
// traversal (a "../" entry escaping dest is rejected) and writes through an
// os.Root handle, so the kernel refuses an escape the lexical check cannot
// see (a symlink already sitting in dest, say).
func extractTarGz(archive []byte, dest string) error {
	root, err := os.OpenRoot(dest)
	if err != nil {
		return fmt.Errorf("policyfile: open extraction dir %s: %w", dest, err)
	}
	defer func() { _ = root.Close() }()

	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }() // read handle
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dest, hdr.Name)
		if !withinDir(dest, target) {
			return fmt.Errorf("policyfile: archive entry %q escapes extraction dir", hdr.Name)
		}
		rel, err := filepath.Rel(dest, target)
		if err != nil {
			return fmt.Errorf("policyfile: archive entry %q escapes extraction dir", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(rel, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if dir := filepath.Dir(rel); dir != "." {
				if err := root.MkdirAll(dir, 0o755); err != nil {
					return err
				}
			}
			f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil { //nolint:gosec // pinned, checksum-verified archive
				_ = f.Close() // already returning an error
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink, tar.TypeLink:
			// The ruby.wasm release has no links; skip rather than risk an
			// unsafe link target.
			continue
		}
	}
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// withinDir reports whether target is base itself or lies beneath it.
func withinDir(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !hasDotDotPrefix(rel))
}

func hasDotDotPrefix(rel string) bool {
	return len(rel) >= 3 && rel[0] == '.' && rel[1] == '.' && (rel[2] == filepath.Separator)
}
