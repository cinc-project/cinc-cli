package policyfile

import (
	"context"
	"errors"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"
)

func wantAll(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got no error, want one mentioning %q", wants)
	}
	msg := err.Error()
	for _, want := range wants {
		if !strings.Contains(msg, want) {
			t.Errorf("error should mention %q:\n%s", want, msg)
		}
	}
}

func ensure(t *testing.T, f *Fetcher, name string, lock cinc.CookbookLock) error {
	t.Helper()
	_, err := f.EnsureCookbook(context.Background(), name, lock)
	return err
}

func TestEnsureCookbookExplainsAnUnknownSource(t *testing.T) {
	f := &Fetcher{CacheRoot: t.TempDir(), LockDir: t.TempDir()}
	err := ensure(t, f, "mystery", cinc.CookbookLock{SourceOptions: map[string]any{"mystery": "x"}})
	wantAll(t, err, `cookbook "mystery"`, "Policyfile.lock.json", "path, git, artifactserver or chef_server")
}

func TestEnsureCookbookExplainsAMissingPathSource(t *testing.T) {
	lockDir := t.TempDir()
	f := &Fetcher{CacheRoot: t.TempDir(), LockDir: lockDir}
	err := ensure(t, f, "gone", cinc.CookbookLock{SourceOptions: map[string]any{"path": "cookbooks/gone"}})
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("errors.Is(err, fs.ErrNotExist) = false for %v", err)
	}
	wantAll(t, err, `cookbook "gone"`, filepath.Join(lockDir, "cookbooks", "gone"), "relative to", "policy install")
}

func TestEnsureCookbookExplainsAMissingCacheKey(t *testing.T) {
	f := &Fetcher{CacheRoot: t.TempDir(), LockDir: t.TempDir()}
	err := ensure(t, f, "nginx", cinc.CookbookLock{
		SourceOptions: map[string]any{"artifactserver": "https://x/d", "version": "1.0.0"},
	})
	wantAll(t, err, `cookbook "nginx"`, "cache_key", "regenerate")
}

func TestEnsureCookbookExplainsAnUnsafeCacheKey(t *testing.T) {
	f := &Fetcher{CacheRoot: t.TempDir(), LockDir: t.TempDir()}
	err := ensure(t, f, "cb", cinc.CookbookLock{
		CacheKey:      "../../evil",
		SourceOptions: map[string]any{"artifactserver": "https://x/d", "version": "1.0.0"},
	})
	wantAll(t, err, `cookbook "cb"`, `"../../evil"`, "cache_key", "slash")
}

func TestEnsureCookbookExplainsAnArtifactserverLockWithNoVersion(t *testing.T) {
	f := &Fetcher{CacheRoot: t.TempDir(), LockDir: t.TempDir()}
	err := ensure(t, f, "nginx", cinc.CookbookLock{
		CacheKey:      "nginx-x",
		SourceOptions: map[string]any{"artifactserver": "https://supermarket.example.test/api/v1/cookbooks/nginx/versions/1.0.0/download"},
	})
	wantAll(t, err, `cookbook "nginx"`, "version", "supermarket.example.test")
}

func TestEnsureCookbookExplainsAChefServerSourceWithoutAConnection(t *testing.T) {
	f := &Fetcher{CacheRoot: t.TempDir(), LockDir: t.TempDir()}
	err := ensure(t, f, "srvcb", cinc.CookbookLock{
		CacheKey:      "srvcb-1.0.0",
		SourceOptions: map[string]any{"chef_server": "https://x", "version": "1.0.0"},
	})
	wantAll(t, err, `cookbook "srvcb"`, "server connection", "config create")
}

func TestEnsureCookbookExplainsAGitSourceWithNoRevision(t *testing.T) {
	f := &Fetcher{CacheRoot: t.TempDir(), LockDir: t.TempDir()}
	err := ensure(t, f, "gitcb", cinc.CookbookLock{
		CacheKey:      "gitcb",
		SourceOptions: map[string]any{"git": "https://example.test/cb.git"},
	})
	wantAll(t, err, `cookbook "gitcb"`, "https://example.test/cb.git", "revision", "branch:", "tag:")
}

func TestEnsureCookbookExplainsRefusedGitArguments(t *testing.T) {
	f := &Fetcher{CacheRoot: t.TempDir(), LockDir: t.TempDir()}
	cases := []struct {
		name  string
		opts  map[string]any
		wants []string
	}{
		{"flag-like URL", map[string]any{"git": "--upload-pack=x", "revision": "abc"},
			[]string{`"--upload-pack=x"`, "option"}},
		{"flag-like revision", map[string]any{"git": "https://example.test/cb.git", "revision": "--orphan"},
			[]string{`"--orphan"`, "option"}},
		{"transport helper", map[string]any{"git": "ext::sh -c x", "revision": "abc"},
			[]string{"ext::", "transport helper", "commands", "https://"}},
		{"unknown scheme", map[string]any{"git": "gopher://host/cb", "revision": "abc"},
			[]string{"gopher://host/cb", "https://", "ssh://"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ensure(t, f, "cb", cinc.CookbookLock{CacheKey: "cb", SourceOptions: tc.opts})
			wantAll(t, err, append([]string{`cookbook "cb"`}, tc.wants...)...)
		})
	}
}

func TestEnsureCookbookExplainsGitNotInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	f := &Fetcher{CacheRoot: t.TempDir(), LockDir: t.TempDir()}
	err := ensure(t, f, "cb", cinc.CookbookLock{
		CacheKey:      "cb",
		SourceOptions: map[string]any{"git": "https://example.test/cb.git", "revision": "abc"},
	})
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("errors.Is(err, exec.ErrNotFound) = false for %v", err)
	}
	wantAll(t, err, `cookbook "cb"`, "git isn't installed", "PATH")
}

func TestEnsureCookbookExplainsAMissingRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	missing := filepath.Join(t.TempDir(), "no-such-repo")
	f := &Fetcher{CacheRoot: t.TempDir(), LockDir: t.TempDir()}
	err := ensure(t, f, "cb", cinc.CookbookLock{
		CacheKey:      "cb",
		SourceOptions: map[string]any{"git": missing, "revision": "abc"},
	})
	wantAll(t, err, `cookbook "cb"`, "couldn't clone "+missing, "git said:")
}

func TestEnsureCookbookExplainsAMissingRevision(t *testing.T) {
	repo, _ := initGitCookbookRepo(t)
	f := &Fetcher{CacheRoot: t.TempDir(), LockDir: t.TempDir()}
	err := ensure(t, f, "gitcb", cinc.CookbookLock{
		CacheKey:      "gitcb",
		SourceOptions: map[string]any{"git": repo, "revision": "no-such-branch"},
	})
	wantAll(t, err, `cookbook "gitcb"`, `"no-such-branch"`, repo, "deleted")
}

func TestEnsureCookbookExplainsARepositoryWithNoCookbook(t *testing.T) {
	repo, sha := initNestedGitCookbookRepo(t)
	f := &Fetcher{CacheRoot: t.TempDir(), LockDir: t.TempDir()}
	err := ensure(t, f, "nested", cinc.CookbookLock{
		CacheKey:      "nested",
		SourceOptions: map[string]any{"git": repo, "revision": sha},
	})
	wantAll(t, err, `cookbook "nested"`, repo, "top level", "rel:")
	if strings.Contains(err.Error(), "cinc-git-") {
		t.Errorf("error names the temporary clone instead of the repository: %s", err)
	}
}

func TestEnsureCookbookExplainsARelOutsideTheRepository(t *testing.T) {
	repo, sha := initNestedGitCookbookRepo(t)
	f := &Fetcher{CacheRoot: t.TempDir(), LockDir: t.TempDir()}
	err := ensure(t, f, "nested", cinc.CookbookLock{
		CacheKey:      "nested",
		SourceOptions: map[string]any{"git": repo, "revision": sha, "rel": "../outside"},
	})
	wantAll(t, err, `cookbook "nested"`, `"../outside"`, "outside the repository")
}

func TestGitCloneHint(t *testing.T) {
	cases := map[string]string{
		"fatal: Authentication failed for 'https://example.test/cb.git/'":            "credentials",
		"git@example.test: Permission denied (publickey).":                           "SSH key",
		"fatal: could not read Username for 'https://example.test': terminal":        "credentials",
		"remote: Repository not found.\nfatal: repository 'x' not found":             "URL",
		"fatal: repository '/tmp/x' does not exist":                                  "URL",
		"ssh: Could not resolve hostname example.test: nodename nor servname":        "network",
		"fatal: unable to access 'https://x/': Could not resolve host: example.test": "network",
	}
	for out, want := range cases {
		if got := gitCloneHint(out); !strings.Contains(got, want) {
			t.Errorf("gitCloneHint(%q) = %q, want it to mention %q", out, got, want)
		}
	}
	if got := gitCloneHint("something else"); got != "" {
		t.Errorf("gitCloneHint of unknown output = %q, want empty", got)
	}
}
