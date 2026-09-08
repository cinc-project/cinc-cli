package resolver

import (
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"

	cinc "github.com/cinc-project/cinc-api"
)

// cookbookFile is one file that contributes to a cookbook's content identifier:
// its cookbook-relative path (forward slashes) and the hex MD5 of its bytes.
type cookbookFile struct {
	Path     string
	Checksum string
}

// CookbookIdentifier holds the two content-addressed identifiers chef computes
// for a Policyfile cookbook lock.
type CookbookIdentifier struct {
	// Content is the SHA1 hex of the cookbook fingerprint (chef's
	// CookbookProfiler::Identifiers#content_identifier).
	Content string
	// DottedDecimal is the X.Y.Z reinterpretation of the SHA1 used as the
	// cookbook_artifacts/<name>/<id> slug for Chef Infra Server 11.x
	// compatibility (#dotted_decimal_identifier).
	DottedDecimal string
}

// ComputeIdentifier computes a cookbook's content identifier from its on-disk
// directory, replicating chef's CookbookProfiler::Identifiers exactly:
//
//   - Gather every cookbook file the way Chef::Cookbook::CookbookVersionLoader
//     does: recurse the directory, but skip top-level dot-directories, the
//     .uploaded-cookbook-version.json sentinel, and symlinks, then drop files
//     ignored by a chefignore in the cookbook root.
//   - The fingerprint is each file's "relative/path:md5\n", sorted by path and
//     concatenated; the content identifier is its SHA1 hex.
//   - The dotted-decimal identifier splits that 40-char hex into 14/14/12-char
//     chunks and reads each as a base-16 integer.
//
// It is verified byte-for-byte against real `chef install` output.
func ComputeIdentifier(cookbookDir string) (CookbookIdentifier, error) {
	files, err := cookbookFiles(cookbookDir)
	if err != nil {
		return CookbookIdentifier{}, err
	}
	content := contentIdentifier(files)
	return CookbookIdentifier{
		Content:       content,
		DottedDecimal: dottedDecimal(content),
	}, nil
}

// uploadedCookbookSentinel is the chef-zero-managed file the loader excludes.
const uploadedCookbookSentinel = ".uploaded-cookbook-version.json"

// cookbookFiles returns the cookbook's content files (path + MD5) following
// Chef::Cookbook::CookbookVersionLoader#load_all_files and #remove_ignored_files.
func cookbookFiles(dir string) ([]cookbookFile, error) {
	ignore, err := cinc.LoadChefignore(dir)
	if err != nil {
		return nil, err
	}

	topEntries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("cinc: read cookbook dir %s: %w", dir, err)
	}

	var files []cookbookFile
	for _, top := range topEntries {
		name := top.Name()
		// Skip top-level directories beginning with "." (chef backcompat).
		if strings.HasPrefix(name, ".") && top.IsDir() {
			continue
		}
		topPath := filepath.Join(dir, name)
		walkErr := filepath.Walk(topPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			// Skip symlinks (matches Find.find's behavior) and the sentinel.
			if info.Mode()&os.ModeSymlink != 0 {
				return nil
			}
			if filepath.Base(path) == uploadedCookbookSentinel {
				return nil
			}
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if ignore.Ignores(rel) {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := md5.Sum(data)
			files = append(files, cookbookFile{Path: rel, Checksum: hex.EncodeToString(sum[:])})
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}
	return files, nil
}

// contentIdentifier builds the sorted "path:checksum\n" fingerprint and returns
// its SHA1 hex.
func contentIdentifier(files []cookbookFile) string {
	sorted := append([]cookbookFile(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	var fp strings.Builder
	for _, f := range sorted {
		fp.WriteString(f.Path)
		fp.WriteByte(':')
		fp.WriteString(f.Checksum)
		fp.WriteByte('\n')
	}
	sum := sha1.Sum([]byte(fp.String()))
	return hex.EncodeToString(sum[:])
}

// dottedDecimal reinterprets a 40-char SHA1 hex string as chef's
// dotted_decimal_identifier: major = hex[0:14], minor = hex[14:28],
// patch = hex[28:40], each parsed as a base-16 integer.
func dottedDecimal(hexID string) string {
	if len(hexID) < 40 {
		return ""
	}
	major := new(big.Int)
	major.SetString(hexID[0:14], 16)
	minor := new(big.Int)
	minor.SetString(hexID[14:28], 16)
	patch := new(big.Int)
	patch.SetString(hexID[28:40], 16)
	return fmt.Sprintf("%s.%s.%s", major, minor, patch)
}
