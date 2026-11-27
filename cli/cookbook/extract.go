package cookbook

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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
		names := make([]string, 0, len(entries))
		for i, e := range entries {
			if i == 5 {
				names = append(names, "...")
				break
			}
			names = append(names, e.Name())
		}
		return "", fmt.Errorf("this doesn't look like a cookbook archive: a cookbook archive holds exactly one directory (named after the cookbook) at its top level, "+
			"but this one has %d entries there (%s)", len(entries), strings.Join(names, ", "))
	}
	return filepath.Join(destDir, entries[0].Name()), nil
}
