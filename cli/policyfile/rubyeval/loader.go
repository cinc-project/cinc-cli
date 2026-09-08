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

// ensureRuntime returns the on-disk wasm + stdlib for the pinned release,
// downloading, verifying, and extracting it once per machine. fetch defaults to
// httpGetBytes; tests may pass their own.
func ensureRuntime(fetch fetcher) (runtimeFiles, error) {
	if fetch == nil {
		fetch = httpGetBytes
	}
	dir, err := cacheDir()
	if err != nil {
		return runtimeFiles{}, err
	}
	rt := runtimeFiles{
		wasmPath: filepath.Join(dir, rubyWasmTreeBinary),
		usrDir:   filepath.Join(dir, rubyWasmTreeUsr),
	}
	if fileExists(rt.wasmPath) && dirExists(rt.usrDir) {
		// Cache hit — but re-verify the cached module hasn't been tampered with
		// since extraction before we hand it to the wasm runtime. On mismatch,
		// fall through and re-download/re-extract rather than execute it.
		if err := verifyFileSHA256(rt.wasmPath, rubyWasmBinarySHA256); err == nil {
			return rt, nil
		}
	}
	if err := materialize(dir, fetch); err != nil {
		return runtimeFiles{}, err
	}
	if !fileExists(rt.wasmPath) || !dirExists(rt.usrDir) {
		return runtimeFiles{}, fmt.Errorf("policyfile: extracted ruby.wasm release missing expected files under %s", dir)
	}
	return rt, nil
}

// materialize downloads the pinned archive via fetch, verifies its SHA-256
// against rubyWasmSHA256, and extracts it into dir atomically.
func materialize(dir string, fetch fetcher) error {
	return materializeWith(dir, fetch, rubyWasmSHA256)
}

// materializeWith is materialize with an injectable expected checksum, so the
// verified-extract path is testable without the real multi-megabyte blob. A
// checksum mismatch is rejected before anything is written into dir.
func materializeWith(dir string, fetch fetcher, wantSHA string) error {
	archive, err := fetch(rubyWasmURL)
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
	// Replace any partial previous attempt, then move staging into place.
	_ = os.RemoveAll(dir)
	if err := os.Rename(staging, dir); err != nil {
		return fmt.Errorf("policyfile: finalize ruby.wasm cache: %w", err)
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
// traversal (a "../" entry escaping dest is rejected).
func extractTarGz(archive []byte, dest string) error {
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
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
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
