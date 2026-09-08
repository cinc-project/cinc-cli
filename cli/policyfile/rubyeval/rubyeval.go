// Package rubyeval evaluates arbitrary Policyfile.rb files by running CRuby
// compiled to WebAssembly (the official ruby/ruby.wasm WASI build) under the
// pure-Go wazero runtime — no system Ruby and no CGo required.
//
// The user's Policyfile.rb is instance_eval'd, inside a real Ruby VM, against a
// faithful reimplementation of chef-cli's Policyfile DSL (shim.rb). Because the
// file runs in genuine CRuby, ANY valid Ruby works — loops, conditionals,
// helper methods, ENV, string interpolation, the stdlib, and require_relative
// against sibling files. Only the Policyfile DSL *methods* are reimplemented;
// the language is real. The shim captures every declaration and serializes it
// to canonical JSON, which Evaluate unmarshals into an EvaluatedPolicy.
//
// See doc.go for the canonical JSON schema, the chef semantics it mirrors, and
// the evaluation-vs-resolution boundary.
package rubyeval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// Sandbox bounds applied to every guest evaluation. The wasm jail itself is
// enforced by wazero/WASI; these caps bound what an attacker-controlled
// Policyfile.rb / metadata.rb can cost the host.
// defaultEvalTimeout (which bounds a single guest evaluation — the cold wasm
// compile on first run plus the actual evaluation) is defined in the build-
// tagged files timeout.go / timeout_race.go: the production default is tight,
// but a `-race` test build runs the wasm engine ~30-50x slower, so the bound
// is scaled up there to avoid false timeouts on legitimate Policyfiles.

const (
	// maxMemoryPages caps guest linear memory at 512 MiB (8192 * 64 KiB pages),
	// bounding a memory-bomb guest rather than letting it exhaust host RAM.
	maxMemoryPages = 8192

	// maxResultBytes caps how many bytes of the shim's out.json the host will
	// read back, so a guest cannot make the host buffer an unbounded result.
	maxResultBytes = 64 << 20 // 64 MiB
)

// policyfileRoot is the notional directory a Policyfile "lives in", used only to
// expand `default_source :chef_repo` paths deterministically. It is a fixed
// sentinel (not the real temp dir) so results are portable across machines, and
// generate_goldens.rb configures chef with the same value.
const policyfileRoot = "/cinc/policyfile"

// stdlibIncludes are the guest load paths CRuby needs to find its standard
// library. The "full" wasm build ships the stdlib as host files (mounted at
// /usr) but does not put these on $LOAD_PATH automatically, so we add them.
var stdlibIncludes = []string{
	"/usr/local/lib/ruby/3.4.0",
	"/usr/local/lib/ruby/3.4.0/wasm32-wasi",
}

// Options tunes a single Evaluate call.
type Options struct {
	// Filename is the basename the Policyfile is written under in the work dir
	// and the filename used for Ruby backtraces. Defaults to "Policyfile.rb".
	Filename string
	// Dir, if set, is a directory whose files are copied next to the Policyfile
	// in the work dir, so the Policyfile can require_relative / File.read its
	// siblings. The Policyfile source passed to Evaluate always takes
	// precedence over a same-named file in Dir.
	Dir string
	// Env is the set of environment variables exposed to the Policyfile (so
	// ENV[...] works). When nil, the Policyfile sees no host environment.
	Env map[string]string
	// Timeout bounds this single evaluation (compile + run). When zero or
	// negative, defaultEvalTimeout is used. A timed-out guest is interrupted.
	Timeout time.Duration
}

// Engine owns the wazero runtime and the compiled CRuby module, which are
// expensive to build (compilation takes a few seconds) and so are created once
// and reused across Evaluate calls.
type Engine struct {
	fetch fetcher // overridable in tests; nil means download over HTTP

	mu       sync.Mutex
	prepared bool
	prepErr  error
	runtime  wazero.Runtime
	compiled wazero.CompiledModule
	rt       runtimeFiles
}

// NewEngine returns an Engine that downloads ruby.wasm over HTTP on first use.
func NewEngine() *Engine { return &Engine{} }

// defaultEngine backs the package-level Evaluate.
var defaultEngine = NewEngine()

// Evaluate runs source as a Policyfile.rb against the embedded DSL using the
// default engine. See Engine.Evaluate.
func Evaluate(ctx context.Context, source string, opts Options) (*EvaluatedPolicy, error) {
	return defaultEngine.Evaluate(ctx, source, opts)
}

// Available reports whether the pinned ruby.wasm runtime can be obtained
// (downloaded or already cached). It returns an error wrapping
// ErrRubyWasmUnavailable when the runtime cannot be fetched, which callers use
// to skip cleanly rather than fail. It does not compile the module.
func (e *Engine) Available() error {
	_, err := ensureRuntime(e.fetch)
	return err
}

// prepare downloads/extracts the runtime and compiles the module once.
func (e *Engine) prepare(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.prepared {
		return e.prepErr
	}
	e.prepared = true

	rt, err := ensureRuntime(e.fetch)
	if err != nil {
		e.prepErr = err
		return err
	}
	e.rt = rt

	wasm, err := os.ReadFile(rt.wasmPath)
	if err != nil {
		e.prepErr = fmt.Errorf("policyfile: read ruby.wasm: %w", err)
		return e.prepErr
	}

	cfg := wazero.NewRuntimeConfig().
		// Honor context cancellation/timeout inside a running guest so a
		// runaway evaluation (loop {}) is actually interrupted, not just
		// abandoned, and cap guest linear memory so a memory-bomb guest cannot
		// exhaust host RAM.
		WithCloseOnContextDone(true).
		WithMemoryLimitPages(maxMemoryPages)
	// Persist the (slow) wasm compilation across processes so repeated runs —
	// e.g. the test suite — pay the cost once. Best-effort: fall back to an
	// in-memory cache if the dir cannot be created.
	if dir, derr := cacheDir(); derr == nil {
		ccDir := filepath.Join(dir, "wazero-compilation-cache")
		if os.MkdirAll(ccDir, 0o755) == nil {
			if cache, cerr := wazero.NewCompilationCacheWithDir(ccDir); cerr == nil {
				cfg = cfg.WithCompilationCache(cache)
			}
		}
	}

	r := wazero.NewRuntimeWithConfig(ctx, cfg)
	wasi_snapshot_preview1.MustInstantiate(ctx, r)

	compiled, err := r.CompileModule(ctx, wasm)
	if err != nil {
		_ = r.Close(ctx)
		e.prepErr = fmt.Errorf("policyfile: compile ruby.wasm: %w", err)
		return e.prepErr
	}
	e.runtime = r
	e.compiled = compiled
	return nil
}

// Evaluate runs source as a Policyfile.rb and returns the captured declarations.
//
// Evaluation mirrors chef: problems the file would surface (an empty run_list,
// invalid run_list item names, conflicting sources, a syntax error, or an
// exception raised by the file) are collected into EvaluatedPolicy.Errors
// rather than raised. When that slice is non-empty Evaluate also returns a
// non-nil error summarizing it, while still returning the (partial) policy, so
// callers can both detect the failure and inspect what was captured.
//
// A returned error wrapping ErrRubyWasmUnavailable means the runtime could not
// be obtained (e.g. offline); callers should skip/degrade rather than treat it
// as a Policyfile problem.
func (e *Engine) Evaluate(ctx context.Context, source string, opts Options) (*EvaluatedPolicy, error) {
	policy, _, err := e.evaluateRaw(ctx, source, opts)
	return policy, err
}

// evaluateRaw runs the Policyfile shim and returns both the decoded
// EvaluatedPolicy and the raw canonical JSON bytes the shim emitted. The raw
// bytes preserve attribute key ordering exactly as chef wrote them, which the
// resolver needs to reproduce a byte-identical lock (Go maps would otherwise
// lose that order).
func (e *Engine) evaluateRaw(ctx context.Context, source string, opts Options) (*EvaluatedPolicy, []byte, error) {
	raw, err := e.run(ctx, shimSource, source, opts)
	if err != nil {
		return nil, nil, err
	}
	var policy EvaluatedPolicy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return nil, nil, fmt.Errorf("policyfile: decode engine output: %w", err)
	}
	if len(policy.Errors) > 0 {
		return &policy, raw, fmt.Errorf("policyfile: %s", strings.Join(policy.Errors, "; "))
	}
	return &policy, raw, nil
}

// run lays down shimSrc and the user's source in a scratch work dir, runs the
// shim inside ruby.wasm, and returns the raw bytes the shim wrote to out.json.
// Both the Policyfile shim and the metadata shim share this machinery; they
// differ only in the shim source and the schema of the JSON they emit.
func (e *Engine) run(ctx context.Context, shimSrc, source string, opts Options) ([]byte, error) {
	// Bound the whole evaluation (first-run compile + guest execution). With
	// WithCloseOnContextDone, a deadline reached mid-guest interrupts it.
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultEvalTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := e.prepare(ctx); err != nil {
		return nil, err
	}

	filename := opts.Filename
	if filename == "" {
		filename = "Policyfile.rb"
	}

	work, err := os.MkdirTemp("", "cinc-policyfile-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)

	engineDir := filepath.Join(work, ".cinc_engine")
	if err := os.MkdirAll(engineDir, 0o755); err != nil {
		return nil, err
	}

	// Copy any sibling files first, then the Policyfile source (so the source
	// passed to Evaluate wins over a same-named file in Dir).
	if opts.Dir != "" {
		if err := copySiblings(opts.Dir, work, filename); err != nil {
			return nil, err
		}
	}
	policyfilePath := filepath.Join(work, filename)
	if err := os.WriteFile(policyfilePath, []byte(source), 0o644); err != nil {
		return nil, err
	}

	// Lay down the shim and its vendored Ruby libraries.
	if err := os.WriteFile(filepath.Join(engineDir, "shim.rb"), []byte(shimSrc), 0o644); err != nil {
		return nil, err
	}
	rubyLibDir := filepath.Join(engineDir, "rubylib")
	if err := writeEmbeddedDir(rubyLib, "rubylib", rubyLibDir); err != nil {
		return nil, err
	}

	guestPath := func(host string) string {
		rel, _ := filepath.Rel(work, host)
		return "/work/" + filepath.ToSlash(rel)
	}
	outHost := filepath.Join(engineDir, "out.json")

	args := []string{"ruby"}
	for _, inc := range stdlibIncludes {
		args = append(args, "-I", inc)
	}
	args = append(args,
		guestPath(filepath.Join(engineDir, "shim.rb")),
		guestPath(policyfilePath),
		guestPath(outHost),
		guestPath(rubyLibDir),
		policyfileRoot,
	)

	fsConfig := wazero.NewFSConfig().
		WithReadOnlyDirMount(e.rt.usrDir, "/usr").
		WithDirMount(work, "/work")

	var stderr strings.Builder
	modCfg := wazero.NewModuleConfig().
		WithName("").
		WithArgs(args...).
		WithStdout(io.Discard). // the Policyfile's own puts output is ignored
		WithStderr(&stderr).
		WithFSConfig(fsConfig)
	for k, v := range opts.Env {
		modCfg = modCfg.WithEnv(k, v)
	}

	mod, err := e.runtime.InstantiateModule(ctx, e.compiled, modCfg)
	if mod != nil {
		_ = mod.Close(ctx)
	}
	if err != nil {
		if ctxErr := ctx.Err(); errors.Is(ctxErr, context.DeadlineExceeded) {
			return nil, fmt.Errorf("policyfile: evaluation timed out after %s (the file may loop forever): %w", timeout, ctxErr)
		}
		return nil, fmt.Errorf("policyfile: ruby.wasm evaluation failed: %w\n%s", err, stderr.String())
	}

	raw, err := readCapped(outHost, maxResultBytes)
	if err != nil {
		return nil, fmt.Errorf("policyfile: engine produced no result%s: %w",
			stderrSuffix(stderr.String()), err)
	}
	return raw, nil
}

// readCapped reads at most max bytes from path, rejecting a result larger than
// the cap rather than buffering it. It bounds how much an attacker-controlled
// guest can make the host allocate via the shim's out.json.
func readCapped(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read handle
	if info, err := f.Stat(); err == nil && info.Size() > max {
		return nil, fmt.Errorf("policyfile: engine result is %d bytes, over the %d-byte cap", info.Size(), max)
	}
	// Read up to max+1 so a file that grows past the stat is still caught.
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("policyfile: engine result exceeds the %d-byte cap", max)
	}
	return data, nil
}

// EvaluateFile reads a Policyfile.rb from disk and evaluates it, using its
// directory for sibling resolution and its basename for backtraces. A
// denylist-filtered view of the host environment is passed through so dynamic
// Policyfiles can still read benign vars (ENV[...] behaves as it would for
// chef), while obviously-sensitive vars (credentials, tokens, keys) are
// withheld from attacker-controllable Ruby — see sanitizedHostEnv.
func (e *Engine) EvaluateFile(ctx context.Context, path string) (*EvaluatedPolicy, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return e.Evaluate(ctx, string(source), Options{
		Filename: filepath.Base(path),
		Dir:      filepath.Dir(path),
		Env:      sanitizedHostEnv(),
	})
}

// EvaluateFileWithRaw is EvaluateFile but also returns the raw canonical JSON
// the shim emitted. The resolver uses the raw bytes to reproduce chef's exact
// attribute key ordering in the lock.
func (e *Engine) EvaluateFileWithRaw(ctx context.Context, path string) (*EvaluatedPolicy, []byte, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	return e.evaluateRaw(ctx, string(source), Options{
		Filename: filepath.Base(path),
		Dir:      filepath.Dir(path),
		Env:      sanitizedHostEnv(),
	})
}

// hostEnv snapshots the process environment as a map for Options.Env.
func hostEnv() map[string]string {
	out := map[string]string{}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			out[kv[:i]] = kv[i+1:]
		}
	}
	return out
}

// sanitizedHostEnv is hostEnv with obviously-sensitive variables stripped, for
// the Policyfile.rb evaluation path. A Policyfile is attacker-controllable
// (it's fetched/edited like any cookbook artifact) but chef-compat allows
// dynamic Policyfiles to read benign env, so we keep the bulk of the
// environment and only deny names that look like credentials. cookbook
// metadata.rb, by contrast, gets NO env at all (see EvaluateMetadataFile).
func sanitizedHostEnv() map[string]string {
	out := hostEnv()
	for k := range out {
		if isSensitiveEnvName(k) {
			delete(out, k)
		}
	}
	return out
}

// isSensitiveEnvName reports whether an env var name looks like it carries a
// secret and so must not be exposed to attacker-controllable Ruby. The match is
// case-insensitive: it denies the CINC_/CHEF_/AWS_ families outright (server
// URLs, keys, profiles) plus anything whose name contains a credential-ish
// word.
func isSensitiveEnvName(name string) bool {
	u := strings.ToUpper(name)
	for _, prefix := range []string{"CINC_", "CHEF_", "AWS_"} {
		if strings.HasPrefix(u, prefix) {
			return true
		}
	}
	for _, frag := range []string{"SECRET", "TOKEN", "PASSWORD", "PRIVATE", "KEY"} {
		if strings.Contains(u, frag) {
			return true
		}
	}
	return false
}

// copySiblings copies the files directly under src into dst, skipping the
// Policyfile name (which Evaluate writes itself) and any nested directories'
// dotfiles is allowed. Only regular files at the top level are copied, which
// covers the require_relative / File.read sibling use cases without pulling in
// large trees.
func copySiblings(src, dst, skip string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, ent := range entries {
		if ent.Name() == skip || ent.Name() == ".cinc_engine" {
			continue
		}
		// Don't follow a symlinked sibling into the sandbox: a malicious
		// Policyfile dir could point evil.rb at /etc/passwd or a host secret
		// and then File.read it from inside the guest. Lstat (via DirEntry's
		// type) sees the link itself, not its target.
		if ent.Type()&os.ModeSymlink != 0 {
			continue
		}
		if ent.IsDir() {
			if err := copyDir(filepath.Join(src, ent.Name()), filepath.Join(dst, ent.Name())); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(filepath.Join(src, ent.Name()), filepath.Join(dst, ent.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Skip symlinks (and don't descend into symlinked directories) so a
		// link inside a copied sibling tree can't dereference a host file into
		// the sandbox. WalkDir does not follow symlinked dirs, but an entry can
		// still itself be a symlink; reject those explicitly.
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(p, target)
	})
}

// copyFile copies a regular file from src to dst. It Lstat's src and refuses to
// copy anything that is not a regular file (symlinks, devices, sockets), so the
// sandbox only ever receives real file contents from within the source tree.
func copyFile(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// writeEmbeddedDir copies an embedded directory tree (rooted at root within
// efs) to dst on disk.
func writeEmbeddedDir(efs fs.FS, root, dst string) error {
	return fs.WalkDir(efs, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(efs, p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func stderrSuffix(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return " (ruby stderr: " + s + ")"
}

// IsUnavailable reports whether err indicates the ruby.wasm runtime could not
// be obtained (so callers can skip rather than fail).
func IsUnavailable(err error) bool {
	return errors.Is(err, ErrRubyWasmUnavailable)
}
