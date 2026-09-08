// Package cookbook handles local cookbook discovery and packaging helpers.
package cookbook

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	gotoken "go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	rubyast "github.com/goruby/goruby/ast"
	rubyparser "github.com/goruby/goruby/parser"
	cinc "github.com/tas50/cinc-api"
)

// Metadata is the minimal cookbook metadata this CLI needs for upload flows.
type Metadata struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// MetadataFile is a loaded metadata.json payload and its minimal identity.
type MetadataFile struct {
	Metadata  Metadata
	JSON      []byte
	Generated bool
}

// Archive is an in-memory gzip-compressed tarball and its manifest.
type Archive struct {
	Name  string
	Bytes []byte
	Files []string
}

// Locate finds a cookbook by name. cookbookPath may be empty, a single path,
// or a filepath-list of parent directories.
func Locate(name, cookbookPath string) (string, error) {
	for _, base := range candidateBases(cookbookPath) {
		if dir, ok := cookbookDir(base, name); ok {
			return dir, nil
		}
	}
	if cookbookPath == "" {
		return "", fmt.Errorf("cookbook %q not found in current directory or ./<name>", name)
	}
	return "", fmt.Errorf("cookbook %q not found in cookbook path %q", name, cookbookPath)
}

// LoadMetadata reads metadata.json, or generates equivalent JSON from the
// common metadata.rb cookbook DSL when metadata.json is absent.
func LoadMetadata(dir string) (MetadataFile, error) {
	path := filepath.Join(dir, "metadata.json")
	data, err := os.ReadFile(path)
	if err == nil {
		md, err := parseMetadataJSON(data, "metadata.json")
		if err != nil {
			return MetadataFile{}, err
		}
		return MetadataFile{Metadata: md, JSON: data}, nil
	}
	if !os.IsNotExist(err) {
		return MetadataFile{}, fmt.Errorf("read metadata.json: %w", err)
	}

	data, err = os.ReadFile(filepath.Join(dir, "metadata.rb"))
	if err != nil {
		if os.IsNotExist(err) {
			return MetadataFile{}, fmt.Errorf("read metadata.json: %w", os.ErrNotExist)
		}
		return MetadataFile{}, fmt.Errorf("read metadata.rb: %w", err)
	}
	doc, err := parseMetadataRB(data, filepath.Base(dir))
	if err != nil {
		return MetadataFile{}, err
	}
	jsonData, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return MetadataFile{}, fmt.Errorf("generate metadata.json: %w", err)
	}
	jsonData = append(jsonData, '\n')
	md := Metadata{Name: doc.Name, Version: doc.Version}
	if err := validateMetadata(md, "generated metadata.json"); err != nil {
		return MetadataFile{}, err
	}
	return MetadataFile{Metadata: md, JSON: jsonData, Generated: true}, nil
}

// LoadMetadataJSON reads metadata.json or generates it from metadata.rb and
// returns the minimal metadata needed by upload flows.
func LoadMetadataJSON(dir string) (Metadata, error) {
	file, err := LoadMetadata(dir)
	if err != nil {
		return Metadata{}, err
	}
	return file.Metadata, nil
}

func parseMetadataJSON(data []byte, source string) (Metadata, error) {
	var md Metadata
	if err := json.Unmarshal(data, &md); err != nil {
		return Metadata{}, fmt.Errorf("parse %s: %w", source, err)
	}
	return md, validateMetadata(md, source)
}

func validateMetadata(md Metadata, source string) error {
	if md.Name == "" {
		return fmt.Errorf("%s is missing name", source)
	}
	if md.Version == "" {
		return fmt.Errorf("%s is missing version", source)
	}
	return nil
}

type metadataDocument struct {
	Name               string            `json:"name"`
	Description        string            `json:"description"`
	LongDescription    string            `json:"long_description"`
	Maintainer         string            `json:"maintainer"`
	MaintainerEmail    string            `json:"maintainer_email"`
	License            string            `json:"license"`
	Platforms          map[string]string `json:"platforms"`
	Dependencies       map[string]string `json:"dependencies"`
	Providing          map[string]string `json:"providing"`
	Recipes            map[string]string `json:"recipes"`
	Version            string            `json:"version"`
	SourceURL          string            `json:"source_url"`
	IssuesURL          string            `json:"issues_url"`
	Privacy            bool              `json:"privacy"`
	ChefVersions       [][]string        `json:"chef_versions"`
	OhaiVersions       [][]string        `json:"ohai_versions"`
	Gems               [][]string        `json:"gems"`
	EagerLoadLibraries interface{}       `json:"eager_load_libraries"`
}

func newMetadataDocument(cookbookName string) metadataDocument {
	return metadataDocument{
		Name:               cookbookName,
		Description:        "",
		LongDescription:    "",
		Maintainer:         "",
		MaintainerEmail:    "",
		License:            "All rights reserved",
		Platforms:          map[string]string{},
		Dependencies:       map[string]string{},
		Providing:          map[string]string{},
		Recipes:            map[string]string{},
		Version:            "0.0.0",
		SourceURL:          "",
		IssuesURL:          "",
		Privacy:            false,
		ChefVersions:       [][]string{},
		OhaiVersions:       [][]string{},
		Gems:               [][]string{},
		EagerLoadLibraries: true,
	}
}

func parseMetadataRB(data []byte, cookbookName string) (metadataDocument, error) {
	fset := gotoken.NewFileSet()
	program, err := rubyparser.ParseFile(fset, "metadata.rb", data, 0)
	if err != nil {
		return metadataDocument{}, fmt.Errorf("parse metadata.rb: %w", err)
	}

	doc := newMetadataDocument(cookbookName)
	for node := range rubyast.WalkEmit(program) {
		call, ok := node.(*rubyast.ContextCallExpression)
		if !ok {
			continue
		}
		if err := applyMetadataCall(call, fset, &doc); err != nil {
			return metadataDocument{}, err
		}
	}
	return doc, nil
}

func applyMetadataCall(call *rubyast.ContextCallExpression, fset *gotoken.FileSet, doc *metadataDocument) error {
	if call.Function == nil || call.Context != nil {
		return nil
	}
	method := call.Function.Value
	if !isMetadataMethod(method) {
		return nil
	}

	args, err := metadataCallArgs(call.Arguments, fset)
	if err != nil {
		return err
	}
	switch method {
	case "name":
		return setStringField(args, method, &doc.Name)
	case "description":
		return setStringField(args, method, &doc.Description)
	case "long_description":
		return setStringField(args, method, &doc.LongDescription)
	case "maintainer":
		return setStringField(args, method, &doc.Maintainer)
	case "maintainer_email":
		return setStringField(args, method, &doc.MaintainerEmail)
	case "license":
		return setStringField(args, method, &doc.License)
	case "version":
		return setStringField(args, method, &doc.Version)
	case "source_url":
		return setStringField(args, method, &doc.SourceURL)
	case "issues_url":
		return setStringField(args, method, &doc.IssuesURL)
	case "privacy":
		if len(args) != 1 || args[0].kind != metadataArgBool {
			return metadataArgError(method, "one boolean argument")
		}
		doc.Privacy = args[0].boolValue
	case "supports":
		key, constraint, err := versionedMetadataArgs(args, method)
		if err != nil {
			return err
		}
		doc.Platforms[key] = constraint
	case "depends":
		key, constraint, err := versionedMetadataArgs(args, method)
		if err != nil {
			return err
		}
		if key != doc.Name {
			doc.Dependencies[key] = constraint
		}
	case "provides":
		key, constraint, err := versionedMetadataArgs(args, method)
		if err != nil {
			return err
		}
		doc.Providing[key] = constraint
	case "chef_version":
		group, err := versionRequirementArgs(args, method)
		if err != nil {
			return err
		}
		doc.ChefVersions = append(doc.ChefVersions, group)
	case "ohai_version":
		group, err := versionRequirementArgs(args, method)
		if err != nil {
			return err
		}
		doc.OhaiVersions = append(doc.OhaiVersions, group)
	case "gem":
		group, err := stringArgs(args, method)
		if err != nil {
			return err
		}
		doc.Gems = append(doc.Gems, group)
	case "eager_load_libraries":
		if len(args) != 1 {
			return metadataArgError(method, "one literal argument")
		}
		doc.EagerLoadLibraries = args[0].value()
	case "recipe":
		values, err := stringArgs(args, method)
		if err != nil {
			return err
		}
		if len(values) != 2 {
			return metadataArgError(method, "two string arguments")
		}
		doc.Recipes[values[0]] = values[1]
	}
	return nil
}

func isMetadataMethod(method string) bool {
	switch method {
	case "name", "description", "long_description", "maintainer", "maintainer_email",
		"license", "version", "source_url", "issues_url", "privacy", "supports",
		"depends", "provides", "chef_version", "ohai_version", "gem",
		"eager_load_libraries", "recipe":
		return true
	default:
		return false
	}
}

type metadataArgKind int

const (
	metadataArgString metadataArgKind = iota
	metadataArgBool
	metadataArgStringArray
)

type metadataArg struct {
	kind      metadataArgKind
	stringVal string
	boolValue bool
	arrayVal  []string
}

func (a metadataArg) value() interface{} {
	switch a.kind {
	case metadataArgBool:
		return a.boolValue
	case metadataArgStringArray:
		return a.arrayVal
	default:
		return a.stringVal
	}
}

func metadataCallArgs(expressions []rubyast.Expression, fset *gotoken.FileSet) ([]metadataArg, error) {
	args := make([]metadataArg, 0, len(expressions))
	for _, expression := range expressions {
		arg, err := parseMetadataArg(expression, fset)
		if err != nil {
			line := fset.Position(gotoken.Pos(expression.Pos())).Line
			return nil, fmt.Errorf("metadata.rb:%d: %w", line, err)
		}
		args = append(args, arg)
	}
	return args, nil
}

func parseMetadataArg(expr rubyast.Expression, fset *gotoken.FileSet) (metadataArg, error) {
	switch value := expr.(type) {
	case *rubyast.StringLiteral:
		return metadataArg{kind: metadataArgString, stringVal: value.Value}, nil
	case *rubyast.SymbolLiteral:
		symbol, err := symbolValue(value)
		if err != nil {
			return metadataArg{}, err
		}
		return metadataArg{kind: metadataArgString, stringVal: symbol}, nil
	case *rubyast.Boolean:
		return metadataArg{kind: metadataArgBool, boolValue: value.Value}, nil
	case *rubyast.ArrayLiteral:
		values, err := parseRubyStringArray(value, fset)
		if err != nil {
			return metadataArg{}, err
		}
		return metadataArg{kind: metadataArgStringArray, arrayVal: values}, nil
	default:
		return metadataArg{}, fmt.Errorf("unsupported metadata.rb argument %q; use literal strings, symbols, booleans, or string arrays", expr.String())
	}
}

func symbolValue(symbol *rubyast.SymbolLiteral) (string, error) {
	switch value := symbol.Value.(type) {
	case *rubyast.Identifier:
		return value.Value, nil
	case *rubyast.StringLiteral:
		return value.Value, nil
	default:
		return "", fmt.Errorf("unsupported metadata.rb symbol %q", symbol.String())
	}
}

func parseRubyStringArray(array *rubyast.ArrayLiteral, fset *gotoken.FileSet) ([]string, error) {
	values := make([]string, 0, len(array.Elements))
	for _, element := range array.Elements {
		arg, err := parseMetadataArg(element, fset)
		if err != nil {
			return nil, err
		}
		if arg.kind != metadataArgString {
			return nil, fmt.Errorf("unsupported metadata.rb array element %q", element.String())
		}
		values = append(values, arg.stringVal)
	}
	return values, nil
}

func setStringField(args []metadataArg, method string, dst *string) error {
	values, err := stringArgs(args, method)
	if err != nil {
		return err
	}
	if len(values) != 1 {
		return metadataArgError(method, "one string argument")
	}
	*dst = values[0]
	return nil
}

func versionedMetadataArgs(args []metadataArg, method string) (string, string, error) {
	values, err := stringArgs(args, method)
	if err != nil {
		return "", "", err
	}
	if len(values) == 1 {
		return values[0], ">= 0.0.0", nil
	}
	if len(values) == 2 {
		return values[0], values[1], nil
	}
	return "", "", metadataArgError(method, "one name argument and optional version constraint")
}

func versionRequirementArgs(args []metadataArg, method string) ([]string, error) {
	values, err := stringArgs(args, method)
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, metadataArgError(method, "at least one version constraint")
	}
	slices.Sort(values)
	return values, nil
}

func stringArgs(args []metadataArg, method string) ([]string, error) {
	values := make([]string, 0, len(args))
	for _, arg := range args {
		if arg.kind != metadataArgString {
			return nil, metadataArgError(method, "string or symbol arguments")
		}
		values = append(values, arg.stringVal)
	}
	return values, nil
}

func metadataArgError(method, want string) error {
	return fmt.Errorf("metadata.rb %s expects %s", method, want)
}

// ReadVersion returns the cookbook version from metadata.json or from the
// common literal metadata.rb form: version "1.2.3".
func ReadVersion(dir string) (string, error) {
	if md, err := LoadMetadataJSON(dir); err == nil {
		return md.Version, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, "metadata.rb"))
	if err != nil {
		return "", fmt.Errorf("read cookbook metadata: %w", err)
	}
	m := metadataRBVersion.FindSubmatch(data)
	if len(m) != 2 {
		return "", fmt.Errorf("metadata.rb must contain a literal version for cookbook upload")
	}
	return string(m[1]), nil
}

var metadataRBVersion = regexp.MustCompile(`(?m)^\s*version\s+['"]([^'"]+)['"]`)

// ArchiveOptions configures cookbook archive construction.
type ArchiveOptions struct {
	// MetadataJSON, when non-empty, overlays this content at metadata.json.
	MetadataJSON []byte
	// IncludeFiles retains the manifest of archived files on the returned
	// Archive — useful for dry-run output, not needed for plain uploads.
	IncludeFiles bool
	// SkipChefignore disables reading and applying the cookbook's
	// chefignore file. By default (false), chefignore is honored.
	SkipChefignore bool
}

// BuildArchive builds a deterministic cookbook tarball rooted at cookbookName/.
func BuildArchive(dir, cookbookName string) (Archive, error) {
	return BuildArchiveWithOptions(dir, cookbookName, ArchiveOptions{IncludeFiles: true})
}

// BuildUploadArchive builds the upload tarball without retaining a separate
// file-name manifest, which is only needed for dry-run output.
func BuildUploadArchive(dir, cookbookName string) (Archive, error) {
	return BuildArchiveWithOptions(dir, cookbookName, ArchiveOptions{})
}

// BuildArchiveWithMetadata builds a tarball and overlays metadataJSON at
// metadata.json, which lets callers package generated metadata without
// modifying the cookbook directory.
func BuildArchiveWithMetadata(dir, cookbookName string, metadataJSON []byte) (Archive, error) {
	return BuildArchiveWithOptions(dir, cookbookName, ArchiveOptions{
		MetadataJSON: metadataJSON,
		IncludeFiles: true,
	})
}

// BuildUploadArchiveWithMetadata is BuildUploadArchive with a metadata overlay.
func BuildUploadArchiveWithMetadata(dir, cookbookName string, metadataJSON []byte) (Archive, error) {
	return BuildArchiveWithOptions(dir, cookbookName, ArchiveOptions{
		MetadataJSON: metadataJSON,
	})
}

// BuildArchiveWithOptions is the configurable form used when callers need to
// override defaults — most notably to skip chefignore.
func BuildArchiveWithOptions(dir, cookbookName string, opts ArchiveOptions) (Archive, error) {
	return buildArchive(dir, cookbookName, opts)
}

func metadataOverlay(metadataJSON []byte) map[string][]byte {
	if len(metadataJSON) == 0 {
		return nil
	}
	return map[string][]byte{"metadata.json": metadataJSON}
}

func buildArchive(dir, cookbookName string, opts ArchiveOptions) (Archive, error) {
	ignore := &cinc.Chefignore{}
	if !opts.SkipChefignore {
		var err error
		ignore, err = cinc.LoadChefignore(dir)
		if err != nil {
			return Archive{}, fmt.Errorf("read chefignore: %w", err)
		}
	}
	overlays := metadataOverlay(opts.MetadataJSON)
	entries, err := archiveEntries(dir, ignore)
	if err != nil {
		return Archive{}, err
	}
	entries = overlayEntries(entries, overlays)
	if len(entries) == 0 {
		return Archive{}, fmt.Errorf("cookbook %s has no files", dir)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.Name = cookbookName + ".tgz"
	gz.ModTime = time.Unix(0, 0)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		hdr := &tar.Header{
			Name:    cookbookName + "/" + entry.path,
			Mode:    0o644,
			Size:    entry.size,
			ModTime: time.Unix(0, 0),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return Archive{}, fmt.Errorf("write tar header %s: %w", entry.path, err)
		}
		if data, ok := overlays[entry.path]; ok {
			if _, err := tw.Write(data); err != nil {
				return Archive{}, fmt.Errorf("write tar entry %s: %w", entry.path, err)
			}
			continue
		}
		if err := writeFileToTar(tw, dir, entry.path); err != nil {
			return Archive{}, err
		}
	}
	if err := tw.Close(); err != nil {
		return Archive{}, fmt.Errorf("close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return Archive{}, fmt.Errorf("close gzip: %w", err)
	}
	var files []string
	if opts.IncludeFiles {
		files = make([]string, len(entries))
		for i, entry := range entries {
			files[i] = cookbookName + "/" + entry.path
		}
	}
	return Archive{Name: cookbookName + ".tgz", Bytes: buf.Bytes(), Files: files}, nil
}

func writeFileToTar(tw *tar.Writer, dir, entryPath string) error {
	f, err := os.Open(filepath.Join(dir, filepath.FromSlash(entryPath)))
	if err != nil {
		return fmt.Errorf("open %s: %w", entryPath, err)
	}
	defer func() { _ = f.Close() }() // read handle
	if _, err := io.Copy(tw, f); err != nil {
		return fmt.Errorf("write tar entry %s: %w", entryPath, err)
	}
	return nil
}

// UploadableFromDir builds the cinc-api LocalCookbook value used by the API's
// server upload implementation.
func UploadableFromDir(dir, version string) (*cinc.LocalCookbook, error) {
	return cinc.LocalCookbookFromDir(dir, version)
}

func candidateBases(cookbookPath string) []string {
	if cookbookPath == "" {
		return []string{"."}
	}
	parts := filepath.SplitList(cookbookPath)
	if len(parts) == 0 {
		return []string{cookbookPath}
	}
	return parts
}

func cookbookDir(base, name string) (string, bool) {
	info, err := os.Stat(base)
	if err == nil && info.IsDir() && hasMetadata(base) && baseIsCookbook(base, name) {
		return base, true
	}
	dir := filepath.Join(base, name)
	info, err = os.Stat(dir)
	if err == nil && info.IsDir() && hasMetadata(dir) {
		return dir, true
	}
	return "", false
}

// baseIsCookbook reports whether base, already known to hold metadata, is the
// cookbook the caller asked for by name. It matches on either the directory's
// (absolute) basename — so "." resolves to the real folder name — or the
// cookbook's declared metadata name, which lets `share mondoo` succeed from a
// directory literally named chef-mondoo.
func baseIsCookbook(base, name string) bool {
	if filepath.Base(absDir(base)) == name {
		return true
	}
	if cbName, ok := cookbookName(base); ok && cbName == name {
		return true
	}
	return false
}

// cookbookName returns the cookbook's declared name from metadata.json, or from
// metadata.rb when only that exists. It reuses the metadata.rb DSL parser and
// is deliberately lightweight — it reports false rather than erroring when a
// name can't be determined, so callers fall back to directory-name matching.
func cookbookName(dir string) (string, bool) {
	if data, err := os.ReadFile(filepath.Join(dir, "metadata.json")); err == nil {
		var md Metadata
		if json.Unmarshal(data, &md) == nil && md.Name != "" {
			return md.Name, true
		}
		return "", false
	}
	data, err := os.ReadFile(filepath.Join(dir, "metadata.rb"))
	if err != nil {
		return "", false
	}
	doc, err := parseMetadataRB(data, filepath.Base(absDir(dir)))
	if err != nil || doc.Name == "" {
		return "", false
	}
	return doc.Name, true
}

// absDir resolves dir to an absolute path so a base of "." reflects the real
// working-directory name; it falls back to dir unchanged when resolution fails.
func absDir(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

func hasMetadata(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "metadata.json")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, "metadata.rb")); err == nil {
		return true
	}
	return false
}

type archiveEntry struct {
	path string
	size int64
}

func overlayEntries(entries []archiveEntry, overlays map[string][]byte) []archiveEntry {
	if len(overlays) == 0 {
		return entries
	}
	seen := make(map[string]bool, len(entries)+len(overlays))
	for i, entry := range entries {
		if data, ok := overlays[entry.path]; ok {
			entries[i].size = int64(len(data))
		}
		seen[entry.path] = true
	}
	for path, data := range overlays {
		if !seen[path] {
			entries = append(entries, archiveEntry{path: path, size: int64(len(data))})
		}
	}
	sortArchiveEntries(entries)
	return entries
}

func archiveEntries(dir string, ignore *cinc.Chefignore) ([]archiveEntry, error) {
	var entries []archiveEntry
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			if rel != "." && ignore.Ignores(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if strings.HasPrefix(rel, ".git/") {
			return nil
		}
		if ignore.Ignores(rel) {
			return nil
		}
		entries = append(entries, archiveEntry{path: rel, size: info.Size()})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk cookbook: %w", err)
	}
	sortArchiveEntries(entries)
	return entries, nil
}

func sortArchiveEntries(entries []archiveEntry) {
	slices.SortFunc(entries, func(a, b archiveEntry) int {
		return strings.Compare(a.path, b.path)
	})
}

// ExtractArchiveFiles returns archive entry names. It is used by tests and
// dry-run output validation without shelling out to tar.
func ExtractArchiveFiles(data []byte) ([]string, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }() // read handle
	tr := tar.NewReader(gz)
	var files []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		files = append(files, hdr.Name)
	}
	return files, nil
}
