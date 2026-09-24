package cookbook

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cinc-project/cinc-cli/cli/internal/tarball"
)

// ExtractArchive unpacks a gzipped cookbook tarball read from r into
// destDir, which should be empty, and returns the path of the single
// top-level directory the archive creates (Supermarket tarballs are rooted
// at <cookbook>/...). Extraction is guarded as tarball.Extract describes.
func ExtractArchive(r io.Reader, destDir string) (string, error) {
	if err := tarball.Extract(r, destDir, tarball.Options{}); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(destDir)
	if err != nil {
		return "", err
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		return "", fmt.Errorf("the cookbook archive has %d top-level entries, but a cookbook archive holds exactly one directory", len(entries))
	}
	return filepath.Join(destDir, entries[0].Name()), nil
}
