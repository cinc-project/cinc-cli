package policyfile

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

// gzTarball builds a gzipped tar whose entries are prefixed with topDir/, as a
// Supermarket cookbook tarball is.
func gzTarball(t *testing.T, topDir string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		hdr := &tar.Header{Name: topDir + "/" + name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
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

// Source classification now lives in cinc-api (CookbookLock.Origin); the CLI
// only needs to surface its error. An unrecognized source has no usable origin.
func TestEnsureCookbookUnknownSource(t *testing.T) {
	f := &Fetcher{CacheRoot: t.TempDir(), LockDir: t.TempDir()}
	_, err := f.EnsureCookbook(context.Background(), "mystery", cinc.CookbookLock{
		SourceOptions: map[string]any{"mystery": "x"},
	})
	if err == nil {
		t.Error("expected an error for an unrecognized cookbook source")
	}
}

func TestEnsureCookbookPathReadInPlace(t *testing.T) {
	lockDir := t.TempDir()
	cbDir := filepath.Join(lockDir, "cookbooks", "mycb")
	if err := os.MkdirAll(cbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	f := &Fetcher{CacheRoot: filepath.Join(t.TempDir(), "cache"), LockDir: lockDir}
	got, err := f.EnsureCookbook(context.Background(), "mycb", cinc.CookbookLock{
		Version:       "0.1.0",
		SourceOptions: map[string]any{"path": "cookbooks/mycb"},
	})
	if err != nil {
		t.Fatalf("EnsureCookbook: %v", err)
	}
	if got != cbDir {
		t.Errorf("path source dir = %q, want %q (read in place)", got, cbDir)
	}
}

func TestEnsureCookbookPathMissing(t *testing.T) {
	f := &Fetcher{CacheRoot: t.TempDir(), LockDir: t.TempDir()}
	_, err := f.EnsureCookbook(context.Background(), "gone", cinc.CookbookLock{
		SourceOptions: map[string]any{"path": "nope"},
	})
	if err == nil {
		t.Error("expected an error for a missing path-source cookbook")
	}
}

func TestEnsureCookbookArtifactserverFetchesAndCaches(t *testing.T) {
	tarball := gzTarball(t, "nginx", map[string]string{
		"metadata.rb":        "name 'nginx'\nversion '1.2.3'\n",
		"recipes/default.rb": "package 'nginx'\n",
	})
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/cookbooks/nginx/versions/1_2_3/download" {
			t.Errorf("path = %q", r.URL.Path)
		}
		hits++
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(tarball)
	}))
	t.Cleanup(srv.Close)

	cacheRoot := filepath.Join(t.TempDir(), "cache")
	f := &Fetcher{CacheRoot: cacheRoot, LockDir: t.TempDir()}
	lock := cinc.CookbookLock{
		Version:       "1.2.3",
		CacheKey:      "nginx-1.2.3-supermarket",
		SourceOptions: map[string]any{"artifactserver": srv.URL + "/api/v1/cookbooks/nginx/versions/1.2.3/download", "version": "1.2.3"},
	}

	dir, err := f.EnsureCookbook(context.Background(), "nginx", lock)
	if err != nil {
		t.Fatalf("EnsureCookbook: %v", err)
	}
	if dir != filepath.Join(cacheRoot, "nginx-1.2.3-supermarket") {
		t.Errorf("cache dir = %q", dir)
	}
	// The tarball's leading "nginx/" is stripped — files land at the cache root.
	if got, err := os.ReadFile(filepath.Join(dir, "metadata.rb")); err != nil || string(got) == "" {
		t.Errorf("metadata.rb not extracted to cache root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "recipes", "default.rb")); err != nil {
		t.Errorf("recipe not extracted: %v", err)
	}

	// Second call is a cache hit — the server must not be contacted again.
	if _, err := f.EnsureCookbook(context.Background(), "nginx", lock); err != nil {
		t.Fatalf("EnsureCookbook (cached): %v", err)
	}
	if hits != 1 {
		t.Errorf("server hit %d times, want 1 (second call should hit cache)", hits)
	}
}

func TestEnsureCookbookArtifactserverNeedsCacheKey(t *testing.T) {
	f := &Fetcher{CacheRoot: t.TempDir(), LockDir: t.TempDir()}
	_, err := f.EnsureCookbook(context.Background(), "nginx", cinc.CookbookLock{
		SourceOptions: map[string]any{"artifactserver": "https://x/d", "version": "1.0.0"},
	})
	if err == nil {
		t.Error("expected an error when an artifactserver lock has no cache_key")
	}
}

// initGitCookbookRepo creates a local git repo containing a cookbook and
// returns the repo path and the HEAD commit sha. It skips the test if git is
// not installed.
func initGitCookbookRepo(t *testing.T) (repo, sha string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo = t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "recipes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "metadata.rb"), []byte("name 'gitcb'\nversion '2.0.0'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "recipes", "default.rb"), []byte("log 'git'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"config", "user.email", "t@example.test"},
		{"config", "user.name", "Test"},
		{"add", "-A"},
		{"commit", "--quiet", "-m", "cookbook"},
	} {
		if out, err := runGit(context.Background(), repo, args...); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	out, err := runGit(context.Background(), repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("git rev-parse: %v: %s", err, out)
	}
	return repo, strings.TrimSpace(out)
}

func TestEnsureCookbookGitClonesAndCaches(t *testing.T) {
	repo, sha := initGitCookbookRepo(t)
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	f := &Fetcher{CacheRoot: cacheRoot, LockDir: t.TempDir()}
	lock := cinc.CookbookLock{
		Version:       "2.0.0",
		CacheKey:      "gitcb-2.0.0-git",
		SourceOptions: map[string]any{"git": repo, "revision": sha},
	}

	dir, err := f.EnsureCookbook(context.Background(), "gitcb", lock)
	if err != nil {
		t.Fatalf("EnsureCookbook (git): %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "metadata.rb")); err != nil || !strings.Contains(string(got), "gitcb") {
		t.Errorf("git cookbook metadata not cached: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "recipes", "default.rb")); err != nil {
		t.Errorf("git cookbook recipe not cached: %v", err)
	}
	// The .git directory must not be copied into the cache.
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		t.Error("the .git directory was copied into the cache")
	}
	// Second call is a cache hit (no error even though we don't re-clone).
	if _, err := f.EnsureCookbook(context.Background(), "gitcb", lock); err != nil {
		t.Fatalf("EnsureCookbook (git, cached): %v", err)
	}
}

func TestEnsureCookbookGitRejectsFlagInjection(t *testing.T) {
	f := &Fetcher{CacheRoot: t.TempDir(), LockDir: t.TempDir()}
	cases := []struct {
		name string
		opts map[string]any
	}{
		{"flag-like repo URL", map[string]any{"git": "--upload-pack=touch /tmp/pwn", "revision": "abc"}},
		{"flag-like revision", map[string]any{"git": "https://example.test/cb.git", "revision": "--orphan"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.EnsureCookbook(context.Background(), "cb", cinc.CookbookLock{
				CacheKey: "cb", SourceOptions: tc.opts,
			})
			if err == nil || !contains(err.Error(), "refusing") {
				t.Errorf("error = %v, want a refusal of the '-'-prefixed git argument", err)
			}
		})
	}
}

func TestValidateGitURL(t *testing.T) {
	ok := []string{
		"https://github.com/acme/cb.git",
		"http://git.internal/cb.git",
		"git://host/cb.git",
		"ssh://git@host/cb.git",
		"git@github.com:acme/cb.git",
		"file:///srv/mirror/cb.git",
		"/srv/mirror/cb.git",
		"../local/cb",
	}
	for _, u := range ok {
		if err := validateGitURL(u); err != nil {
			t.Errorf("validateGitURL(%q) = %v, want nil", u, err)
		}
	}
	bad := []string{
		"ext::sh -c 'touch /tmp/pwn'",
		"fd::17/foo",
		"transport::whatever",
		"unknownscheme://host/x",
	}
	for _, u := range bad {
		if err := validateGitURL(u); err == nil {
			t.Errorf("validateGitURL(%q) = nil, want refusal", u)
		}
	}
}

func TestEnsureCookbookGitRejectsTransportHelper(t *testing.T) {
	f := &Fetcher{CacheRoot: t.TempDir(), LockDir: t.TempDir()}
	_, err := f.EnsureCookbook(context.Background(), "cb", cinc.CookbookLock{
		CacheKey:      "cb",
		SourceOptions: map[string]any{"git": "ext::sh -c 'touch /tmp/pwn'", "revision": "abc"},
	})
	if err == nil || !contains(err.Error(), "transport helper") {
		t.Errorf("error = %v, want refusal of the ext:: transport helper", err)
	}
}

func TestEnsureCookbookRejectsCacheKeyTraversal(t *testing.T) {
	f := &Fetcher{CacheRoot: t.TempDir(), LockDir: t.TempDir()}
	_, err := f.EnsureCookbook(context.Background(), "cb", cinc.CookbookLock{
		CacheKey:      "../../../../tmp/evil",
		SourceOptions: map[string]any{"artifactserver": "https://x/d", "version": "1.0.0"},
	})
	if err == nil || !contains(err.Error(), "unsafe path component") {
		t.Errorf("error = %v, want rejection of a traversal cache_key", err)
	}
}

// TestFetchGitRejectsPathEscapingTheClone covers the git source's "path"
// sub-option, which selects a subdirectory of the clone and comes from the
// untrusted lock. Today EnsureCookbook cannot deliver such a lock here,
// because cinc-api's CookbookLock.Origin prefers the "path" key over "git"
// and classifies the lock as a path source before fetchGit is reached. That
// makes this containment check defence in depth rather than a live fix: the
// guard belongs next to the join, not in another module's key precedence.
//
// Without it, a path of "../.." reaches any directory on the machine holding
// a metadata.rb, and copyTree would pull it into the cookbook cache.
func TestFetchGitRejectsPathEscapingTheClone(t *testing.T) {
	repo, sha := initGitCookbookRepo(t)
	f := &Fetcher{CacheRoot: t.TempDir(), LockDir: t.TempDir()}

	for _, sub := range []string{"../outside", "nested/../../outside"} {
		t.Run(sub, func(t *testing.T) {
			lock := cinc.CookbookLock{
				SourceOptions: map[string]any{"git": repo, "revision": sha, "path": sub},
			}
			err := f.fetchGit(context.Background(), lock, repo, t.TempDir())
			if err == nil {
				t.Fatalf("path %q was accepted, want it rejected for escaping the clone", sub)
			}
			if !contains(err.Error(), "escapes") {
				t.Errorf("error = %v, want it to say the path escapes the repository", err)
			}
		})
	}
}

// TestFetchGitAcceptsNestedPath is the other half: the common
// "cookbooks/<name>" monorepo layout still resolves.
func TestFetchGitAcceptsNestedPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	nested := filepath.Join(repo, "cookbooks", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "metadata.rb"), []byte("name 'nested'\nversion '1.0.0'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"config", "user.email", "t@example.test"},
		{"config", "user.name", "Test"},
		{"add", "-A"},
		{"commit", "--quiet", "-m", "cookbook"},
	} {
		if out, err := runGit(context.Background(), repo, args...); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	out, err := runGit(context.Background(), repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("git rev-parse: %v: %s", err, out)
	}

	f := &Fetcher{CacheRoot: t.TempDir(), LockDir: t.TempDir()}
	dest := t.TempDir()
	lock := cinc.CookbookLock{
		SourceOptions: map[string]any{"git": repo, "revision": strings.TrimSpace(out), "path": "cookbooks/nested"},
	}
	if err := f.fetchGit(context.Background(), lock, repo, dest); err != nil {
		t.Fatalf("fetchGit with a nested path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "metadata.rb")); err != nil {
		t.Errorf("nested cookbook not fetched: %v", err)
	}
}

func TestEnsureCookbookGitNeedsRevision(t *testing.T) {
	f := &Fetcher{CacheRoot: t.TempDir(), LockDir: t.TempDir()}
	_, err := f.EnsureCookbook(context.Background(), "gitcb", cinc.CookbookLock{
		CacheKey:      "gitcb",
		SourceOptions: map[string]any{"git": "https://example.test/cb.git"},
	})
	if err == nil || !contains(err.Error(), "revision") {
		t.Errorf("git-without-revision error = %v, want a clear message about a missing revision", err)
	}
}

func TestEnsureCookbookChefServer(t *testing.T) {
	var base string
	mux := http.NewServeMux()
	mux.HandleFunc("/organizations/acme/cookbooks/srvcb/1.0.0", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"cookbook_name":"srvcb","name":"srvcb-1.0.0","version":"1.0.0",
			"root_files":[{"name":"metadata.rb","path":"metadata.rb","specificity":"default","checksum":"x","url":"%s/f/metadata.rb"}]}`, base)
	})
	mux.HandleFunc("/f/metadata.rb", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("name 'srvcb'\nversion '1.0.0'\n"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	base = srv.URL

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	c, err := cinc.NewClient(cinc.Config{ServerURL: srv.URL, Org: "acme", ClientName: "tim", Key: key})
	if err != nil {
		t.Fatal(err)
	}

	cacheRoot := filepath.Join(t.TempDir(), "cache")
	f := &Fetcher{CacheRoot: cacheRoot, LockDir: t.TempDir(), Chef: c}
	dir, err := f.EnsureCookbook(context.Background(), "srvcb", cinc.CookbookLock{
		Version:       "1.0.0",
		CacheKey:      "srvcb-1.0.0-chefserver",
		SourceOptions: map[string]any{"chef_server": srv.URL, "version": "1.0.0"},
	})
	if err != nil {
		t.Fatalf("EnsureCookbook (chef_server): %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "metadata.rb")); err != nil || !strings.Contains(string(got), "srvcb") {
		t.Errorf("chef_server cookbook not cached: %v", err)
	}
}

func TestEnsureCookbookChefServerNeedsClient(t *testing.T) {
	f := &Fetcher{CacheRoot: t.TempDir(), LockDir: t.TempDir()} // no Chef client
	_, err := f.EnsureCookbook(context.Background(), "srvcb", cinc.CookbookLock{
		CacheKey:      "srvcb-1.0.0",
		SourceOptions: map[string]any{"chef_server": "https://x", "version": "1.0.0"},
	})
	if err == nil || !strings.Contains(err.Error(), "server connection") {
		t.Errorf("error = %v, want a clear 'needs server connection' message", err)
	}
}

func TestDefaultCacheRoot(t *testing.T) {
	root, err := DefaultCacheRoot()
	if err != nil {
		t.Fatalf("DefaultCacheRoot: %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(root), ".cinc/cache/cookbooks") {
		t.Errorf("DefaultCacheRoot = %q, want it under ~/.cinc/cache/cookbooks", root)
	}
}

func contains(s, sub string) bool { return bytes.Contains([]byte(s), []byte(sub)) }
