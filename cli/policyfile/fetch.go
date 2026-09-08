package policyfile

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	cinc "github.com/cinc-project/cinc-api"
	sm "github.com/cinc-project/cinc-supermarket-api"
)

// Fetcher locates the cookbooks a Policyfile lock pins, fetching them from the
// source each lock records into a local cache and returning the on-disk
// directory that holds the cookbook root. It is safe to call repeatedly: a
// cached cookbook is returned without re-fetching.
type Fetcher struct {
	// CacheRoot is the directory holding one subdirectory per cache_key for
	// fetched (artifactserver/git) cookbooks.
	CacheRoot string
	// LockDir is the directory of the lock file, used to resolve relative
	// path: sources.
	LockDir string
	// Chef is used for chef_server-sourced cookbooks. It may be nil when no
	// lock uses a chef_server source.
	Chef *cinc.Client

	// newSupermarket builds a Supermarket client for a base URL. Overridable
	// in tests; defaults to a cinc-supermarket anonymous client.
	newSupermarket func(base string) (*sm.Client, error)
}

// DefaultCacheRoot is ~/.cinc/cache/cookbooks.
func DefaultCacheRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("policyfile: locate home directory: %w", err)
	}
	return filepath.Join(home, ".cinc", "cache", "cookbooks"), nil
}

func (f *Fetcher) supermarketClient(base string) (*sm.Client, error) {
	if f.newSupermarket != nil {
		return f.newSupermarket(base)
	}
	return sm.NewClient(sm.Config{BaseURL: base}, sm.WithUserAgent("cinc-cli"))
}

// EnsureCookbook returns the directory containing the cookbook for one lock,
// fetching and caching it first if necessary. path: cookbooks are read in
// place; artifactserver/chef_server cookbooks are cached under CacheRoot keyed
// by the lock's cache_key (a cache hit is a directory-exists check).
func (f *Fetcher) EnsureCookbook(ctx context.Context, name string, lock cinc.CookbookLock) (string, error) {
	kind, value, err := lock.Origin()
	if err != nil {
		return "", fmt.Errorf("cookbook %q: %w", name, err)
	}

	if kind == cinc.SourcePath {
		dir := value
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(f.LockDir, dir)
		}
		if _, err := os.Stat(dir); err != nil {
			return "", fmt.Errorf("cookbook %q: path source %s: %w", name, dir, err)
		}
		return dir, nil
	}

	if lock.CacheKey == "" {
		return "", fmt.Errorf("cookbook %q: %s source has no cache_key to cache under", name, kind)
	}
	// cache_key comes from the (untrusted) lock; keep it from escaping the cache.
	dest, err := safeJoin(f.CacheRoot, lock.CacheKey)
	if err != nil {
		return "", fmt.Errorf("cookbook %q: %w", name, err)
	}
	if isDir(dest) {
		return dest, nil // cache hit
	}

	staging := dest + ".partial"
	if err := os.RemoveAll(staging); err != nil {
		return "", err
	}
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return "", err
	}
	switch kind {
	case cinc.SourceArtifactserver:
		err = f.fetchArtifactserver(ctx, name, value, lock.PinnedVersion(), staging)
	case cinc.SourceChefServer:
		err = f.fetchChefServer(ctx, name, lock.PinnedVersion(), staging)
	case cinc.SourceGit:
		err = f.fetchGit(ctx, lock, value, staging)
	}
	if err != nil {
		_ = os.RemoveAll(staging)
		return "", fmt.Errorf("cookbook %q: %w", name, err)
	}
	if err := os.Rename(staging, dest); err != nil {
		_ = os.RemoveAll(staging)
		return "", fmt.Errorf("cookbook %q: finalize cache entry: %w", name, err)
	}
	return dest, nil
}

// fetchArtifactserver downloads a cookbook tarball from a Supermarket-style
// artifactserver and extracts it into dest. The lock records the full download
// URL; the cinc-supermarket client is pointed at its scheme://host base and
// asked for the cookbook by name and version, reconstructing the same request.
func (f *Fetcher) fetchArtifactserver(ctx context.Context, name, artifactURL, version, dest string) error {
	if version == "" {
		return fmt.Errorf("artifactserver source has no version")
	}
	base, err := baseURL(artifactURL)
	if err != nil {
		return err
	}
	client, err := f.supermarketClient(base)
	if err != nil {
		return err
	}
	body, _, err := client.Cookbooks.Download(ctx, name, version)
	if err != nil {
		return fmt.Errorf("download from %s: %w", base, err)
	}
	defer body.Close()
	return extractCookbookTarball(body, dest)
}

// fetchChefServer downloads a cookbook from the Cinc/Chef server via cinc-api,
// which writes the cookbook's files into dest.
func (f *Fetcher) fetchChefServer(ctx context.Context, name, version, dest string) error {
	if f.Chef == nil {
		return fmt.Errorf("chef_server source requires a configured server connection")
	}
	if version == "" {
		version = "_latest"
	}
	return f.Chef.Cookbooks.Download(ctx, name, version, dest)
}

// fetchGit clones the lock's git repository, checks out the pinned revision,
// and copies the cookbook (the repo root, or the source_options "path"
// subdirectory) into dest, omitting the .git directory. It shells out to the
// `git` binary, which must be installed.
func (f *Fetcher) fetchGit(ctx context.Context, lock cinc.CookbookLock, repoURL, dest string) error {
	revision := stringOption(lock.SourceOptions, "revision")
	if revision == "" {
		revision = stringOption(lock.SourceOptions, "branch")
	}
	if revision == "" {
		return fmt.Errorf("git source has no revision or branch to check out")
	}
	// repoURL and revision come from the (potentially untrusted) lock. Reject
	// values that begin with "-" so they cannot be smuggled in as git flags
	// (argv injection). clone additionally gets a "--" separator; checkout
	// cannot use "--" (it would treat the revision as a pathspec), so the
	// leading-dash rejection is its guard.
	if strings.HasPrefix(repoURL, "-") {
		return fmt.Errorf("refusing git source URL beginning with '-': %q", repoURL)
	}
	if strings.HasPrefix(revision, "-") {
		return fmt.Errorf("refusing git revision beginning with '-': %q", revision)
	}
	if err := validateGitURL(repoURL); err != nil {
		return err
	}

	clone, err := os.MkdirTemp("", "cinc-git-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(clone)

	if out, err := runGit(ctx, "", "clone", "--quiet", "--", repoURL, clone); err != nil {
		return fmt.Errorf("git clone %s: %w: %s", repoURL, err, out)
	}
	if out, err := runGit(ctx, clone, "checkout", "--quiet", revision); err != nil {
		return fmt.Errorf("git checkout %s: %w: %s", revision, err, out)
	}

	root := clone
	if sub := stringOption(lock.SourceOptions, "path"); sub != "" {
		root = filepath.Join(clone, sub)
	}
	if !hasCookbookMetadata(root) {
		return fmt.Errorf("no cookbook (metadata.rb or metadata.json) found at %q in the git repository", root)
	}
	return copyTree(root, dest)
}

// runGit runs `git args...` (optionally in dir) and returns its combined
// output, so failures can be reported with git's own message. It restricts the
// protocols git may use to a safe set, so a lock cannot drive git into a
// command-executing transport helper (ext::, fd::, …) even if validateGitURL is
// somehow bypassed.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_ALLOW_PROTOCOL=file:git:http:https:ssh")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

// transportHelperRe matches git's "<helper>::<address>" transport-helper
// syntax (e.g. ext::, fd::), which can execute arbitrary commands.
var transportHelperRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*::`)

// validateGitURL rejects git source URLs that could run code or reach unsafe
// transports. It allows the standard remote schemes (https/http/git/ssh/file),
// scp-style addresses (user@host:path), and local paths, but refuses
// transport-helper forms (ext::/fd::/…) and any other scheme.
func validateGitURL(repoURL string) error {
	if i := strings.Index(repoURL, "://"); i >= 0 {
		switch strings.ToLower(repoURL[:i]) {
		case "https", "http", "git", "ssh", "file":
			return nil
		default:
			return fmt.Errorf("refusing git source URL with unsupported scheme: %q", repoURL)
		}
	}
	if m := transportHelperRe.FindString(repoURL); m != "" {
		return fmt.Errorf("refusing git source URL using transport helper %q: %q", strings.TrimSuffix(m, "::"), repoURL)
	}
	return nil
}

// safeJoin joins a trusted base directory with one or more untrusted path
// components, rejecting any component that is empty, "." / "..", or contains a
// path separator — so the result can never escape base. It is the guard for
// lock-derived names (cache keys, cookbook names, identifiers) used to build
// filesystem paths.
func safeJoin(base string, components ...string) (string, error) {
	for _, c := range components {
		if c == "" || c == "." || c == ".." || strings.ContainsAny(c, `/\`) {
			return "", fmt.Errorf("unsafe path component %q", c)
		}
	}
	joined := filepath.Join(append([]string{base}, components...)...)
	if !withinDir(base, joined) {
		return "", fmt.Errorf("path %q escapes %q", joined, base)
	}
	return joined, nil
}

func hasCookbookMetadata(dir string) bool {
	for _, name := range []string{"metadata.rb", "metadata.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

func stringOption(opts map[string]any, key string) string {
	if v, ok := opts[key].(string); ok {
		return v
	}
	return ""
}

func baseURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid artifactserver URL %q", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
