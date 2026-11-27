// Package policyfile deploys an existing Policyfile.lock.json to a Cinc/Chef
// Server. It fetches and caches the cookbooks the lock pins (from the sources
// the lock records), then drives the server-side push via cinc-api, and
// assembles a standalone export bundle. It owns the local, multi-source
// concerns; every server call goes through cinc-api or cinc-supermarket.
package policyfile

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	cinc "github.com/cinc-project/cinc-api"
	sm "github.com/cinc-project/cinc-supermarket-api"

	"github.com/cinc-project/cinc-cli/cli/internal/tarball"
	"github.com/cinc-project/cinc-cli/cli/progname"
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
		return "", fmt.Errorf("the Policyfile.lock.json entry for cookbook %q doesn't record a source we know how to fetch from (%w). "+
			"Each cookbook lock needs a path, git, artifactserver or chef_server source; regenerate the lock from its Policyfile with `%s policy install`", name, err, progname.Get())
	}

	if kind == cinc.SourcePath {
		dir := value
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(f.LockDir, dir)
		}
		if _, err := os.Stat(dir); err != nil {
			return "", fmt.Errorf("the lock reads cookbook %q from the directory %s, but we can't open it (%w). "+
				"A lock's path: source is relative to the lock file's directory (%s). If the cookbook moved, update the path: line in the Policyfile and run `%s policy install` again",
				name, dir, err, f.LockDir, progname.Get())
		}
		return dir, nil
	}

	if lock.CacheKey == "" {
		return "", fmt.Errorf("the lock entry for cookbook %q has no cache_key. A %s source needs one so we know where to keep the download; "+
			"the lock looks incomplete or hand-edited, so regenerate it from its Policyfile", name, kind)
	}
	// cache_key comes from the (untrusted) lock; keep it from escaping the cache.
	dest, err := safeJoin(f.CacheRoot, lock.CacheKey)
	if err != nil {
		return "", fmt.Errorf("the lock entry for cookbook %q has a cache_key we won't use: %w. "+
			"A real Policyfile lock never has one like that, so the lock is damaged or hand-edited; regenerate it from its Policyfile", name, err)
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
		return "", fmt.Errorf("we couldn't fetch cookbook %q from its %s source: %w", name, kind, err)
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
		return fmt.Errorf("the lock doesn't record which version to download from %s. Regenerate the lock from its Policyfile so it pins one", artifactURL)
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
	// Supermarket wraps the cookbook in a <name>/ directory; the cache entry
	// is the cookbook root itself.
	if err := tarball.Extract(body, dest, tarball.Options{StripTopLevel: true}); err != nil {
		return fmt.Errorf("unpack the download from %s: %w", base, err)
	}
	return nil
}

// fetchChefServer downloads a cookbook from the Cinc/Chef server via cinc-api,
// which writes the cookbook's files into dest.
func (f *Fetcher) fetchChefServer(ctx context.Context, name, version, dest string) error {
	if f.Chef == nil {
		return fmt.Errorf("it comes from a Cinc Server, and we don't have a server connection to download it with. Run `%s config create` to set one up", progname.Get())
	}
	if version == "" {
		version = cinc.LatestVersion
	}
	return f.Chef.Cookbooks.Download(ctx, name, version, dest)
}

// fetchGit clones the lock's git repository, checks out the pinned revision,
// and copies the cookbook (the repo root, or the source_options "rel"
// subdirectory) into dest, omitting the .git directory. It shells out to the
// `git` binary, which must be installed.
func (f *Fetcher) fetchGit(ctx context.Context, lock cinc.CookbookLock, repoURL, dest string) error {
	revision := lock.GitRef()
	if revision == "" {
		return fmt.Errorf("the lock doesn't say which commit of %s to check out. A git source needs a revision, ref, tag or branch; "+
			"set one on the Policyfile's git: line (for example branch: 'main' or tag: 'v1.2.0') and regenerate the lock", repoURL)
	}
	// repoURL and revision come from the (potentially untrusted) lock. Reject
	// values that begin with "-" so they cannot be smuggled in as git flags
	// (argv injection). clone additionally gets a "--" separator; checkout
	// cannot use "--" (it would treat the revision as a pathspec), so the
	// leading-dash rejection is its guard.
	if strings.HasPrefix(repoURL, "-") {
		return fmt.Errorf("we won't clone the git URL %q: it starts with \"-\", so git would read it as a command-line option instead of a repository. Check the git URL in the lock", repoURL)
	}
	if strings.HasPrefix(revision, "-") {
		return fmt.Errorf("we won't check out the git revision %q: it starts with \"-\", so git would read it as a command-line option instead of a branch, tag or commit. Check the revision in the lock", revision)
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
		return gitCloneErr(repoURL, out, err)
	}
	if out, err := runGit(ctx, clone, "checkout", "--quiet", revision); err != nil {
		return fmt.Errorf("we cloned %s but couldn't check out %q in it (%w). The branch, tag or commit may have been deleted, renamed or never pushed.\n\ngit said: %s",
			repoURL, revision, err, strings.TrimSpace(out))
	}

	root := clone
	// chef (cookbook-omnifetch) records a cookbook in a subdirectory of its
	// repository as "rel". A lock that also carries "path" never gets here:
	// CookbookLock.Origin classifies it as a path source first.
	if sub := lock.GitSubdir(); sub != "" {
		// The subdirectory comes from the untrusted lock, so it has to stay
		// inside the clone. Nested paths are legitimate (the
		// "cookbooks/<name>" monorepo layout), so this is a containment
		// check rather than the stricter safeJoin used for single-segment
		// names like cache keys.
		root = filepath.Join(clone, sub)
		if !tarball.Within(clone, root) {
			return fmt.Errorf("we won't use the lock's rel %q for %s: it points outside the repository, and a cookbook has to be inside it", sub, repoURL)
		}
	}
	if !hasCookbookMetadata(root) {
		if sub := lock.GitSubdir(); sub != "" {
			return fmt.Errorf("there's no cookbook in %q of the git repository %s at %s: that directory has no metadata.rb or metadata.json. "+
				"Check the rel: option on the Policyfile's git: line names the cookbook's directory", sub, repoURL, revision)
		}
		return fmt.Errorf("there's no cookbook at the top level of the git repository %s at %s: it has no metadata.rb or metadata.json there. "+
			"If the cookbook lives in a subdirectory, add rel: to the Policyfile's git: line, for example rel: 'cookbooks/<name>'", repoURL, revision)
	}
	return copyTree(root, dest)
}

// gitCloneErr explains a failed `git clone` of repoURL: git missing, then
// the likely cause read from git's output, then git's own words.
func gitCloneErr(repoURL, out string, err error) error {
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("git isn't installed, or isn't on your PATH (%w). %s runs git to fetch cookbooks from git sources, so install git and try again", err, progname.Get())
	}
	msg := fmt.Sprintf("we couldn't clone %s", repoURL)
	if hint := gitCloneHint(out); hint != "" {
		msg += ". " + hint
	}
	return fmt.Errorf("%s (%w).\n\ngit said: %s", msg, err, strings.TrimSpace(out))
}

// gitCloneHint reads git's clone output for the common failures and says
// what to do about each; it returns "" when it doesn't recognize the output.
func gitCloneHint(out string) string {
	low := strings.ToLower(out)
	switch {
	case strings.Contains(low, "permission denied (publickey"):
		return "The server refused your SSH key: check that `ssh -T` works for that host and that your key has access to the repository"
	case strings.Contains(low, "authentication failed"), strings.Contains(low, "could not read username"),
		strings.Contains(low, "could not read password"), strings.Contains(low, "terminal prompts disabled"):
		return "The server wants credentials we couldn't supply: set up a git credential helper or an access token for that host, or use an SSH URL"
	case strings.Contains(low, "repository not found"), strings.Contains(low, "does not exist"),
		strings.Contains(low, "does not appear to be a git repository"), strings.Contains(low, "not found"):
		return "The repository isn't there, or you don't have access to it: check the URL, and that your account can see the repository"
	case strings.Contains(low, "could not resolve host"), strings.Contains(low, "could not resolve hostname"),
		strings.Contains(low, "connection refused"), strings.Contains(low, "timed out"):
		return "We couldn't reach the git server: check the host name in the URL and your network connection"
	}
	return ""
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
	if before, _, ok := strings.Cut(repoURL, "://"); ok {
		switch strings.ToLower(before) {
		case "https", "http", "git", "ssh", "file":
			return nil
		default:
			return fmt.Errorf("we won't clone %q: git sources have to use https://, http://, ssh://, git:// or file:// URLs (or the user@host:path form), and %s:// isn't one of those", repoURL, before)
		}
	}
	if m := transportHelperRe.FindString(repoURL); m != "" {
		return fmt.Errorf("we won't clone %q: the %q prefix asks git to run a transport helper program, which can execute arbitrary commands, so a lock can't use one. Use an https:// or ssh:// URL instead", repoURL, m)
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
			return "", fmt.Errorf("%q isn't a safe file name: it's empty, \".\" or \"..\", or contains a slash", c)
		}
	}
	joined := filepath.Join(append([]string{base}, components...)...)
	if !tarball.Within(base, joined) {
		return "", fmt.Errorf("%s would land outside %s", joined, base)
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

func baseURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("the lock's artifactserver URL %q isn't a full URL with a scheme and host (like https://supermarket.example.com/...)", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
