// Package remote reads a team's own template catalogue: a file somebody
// publishes at an HTTPS address, in the same schema as the catalogue that ships
// in the binary.
//
// The built-in catalogue is fixed when the binary is built, and a company's own
// software — the internal wiki, the licence server, the pinned fork of
// something public — has no place in it. Kubero lets an administrator add
// catalogue URLs and Dokploy lets the template source be changed; both then
// trust whatever comes back. This does not. A private catalogue is read with
// the same parser and held to the same checks as the built-in one
// (templates.Parse and templates.Validate), each template on its own: one
// that fails is listed with why and cannot be installed, and the rest of the
// catalogue still loads.
//
// Two shapes are read. An index is one YAML or JSON document holding a list
// of templates, under `templates:` or as the document itself. An archive is a
// .tar.gz or a .zip of files exactly like internal/templates/catalogue: one
// template per `*.yaml`, in a `catalogue/` directory or at the top, with
// logos beside them in `icons/<id>.svg`, `.png` or `.webp`. A repository's
// own archive — GitHub's, Gitea's — wraps everything in one directory named
// after the commit, and that directory is looked through.
//
// Nothing here stores anything or decides who may see what; that is
// internal/api, which keeps the last good copy of each catalogue and resolves
// a template by the catalogue it came from and the team that owns it.
package remote

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"

	"skifity/internal/templates"
)

// The limits on what a catalogue may be.
//
// A catalogue is a list of small text files, and the built-in one — 374
// templates and their logos — is under two megabytes as an archive. These are
// generous against that and small against the memory of the process that holds
// the master key.
const (
	// MaxDownloadBytes is the most a catalogue's address may answer with.
	MaxDownloadBytes = 5 << 20
	// MaxUnpackedBytes is the most an archive may unpack to: a gzip bomb is
	// a few kilobytes that inflate to gigabytes.
	MaxUnpackedBytes = 32 << 20
	// MaxArchiveEntries is the most files an archive may hold, of any kind.
	MaxArchiveEntries = 10000
	// MaxTemplateFileBytes is the most one template's file may hold. The
	// largest built-in template, files and all, is a few kilobytes.
	MaxTemplateFileBytes = 1 << 20
	// MaxTemplates is the most templates one catalogue may offer.
	MaxTemplates = 1000
	// MaxIconBytes is the most one logo may be.
	MaxIconBytes = 256 << 10
)

// ErrUnreadable is a download that is not a catalogue at all: an HTML error
// page answered with 200, an archive that does not open, a document that is
// not a list of templates. The last good copy is kept.
var ErrUnreadable = errors.New("this is not a template catalogue")

// Problem is a template in a catalogue that cannot be installed, and why.
type Problem struct {
	// File is where in the catalogue it is: the file in an archive, or its
	// place in an index's list.
	File string `json:"file"`
	// ID and Name are the template's, when it got far enough to have them.
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	// Errors are the sentences templates.Validate answered, or why the
	// template could not be read at all.
	Errors []string `json:"errors"`
}

// Icon is a logo, as it will be served.
type Icon struct {
	ContentType string
	Body        []byte
}

// Catalogue is a catalogue's file, read and checked.
type Catalogue struct {
	// Format is "index", "tar.gz" or "zip".
	Format string
	// Templates are the ones that can be installed, sorted by name. Each has
	// no Icon: the loader sets it from what it found for the template.
	Templates []templates.Template
	// Problems are the ones that cannot, with why.
	Problems []Problem
	// Icons are the logos an archive carried, by template id.
	Icons map[string]Icon
	// IconRefs are the addresses the templates' own `icon` fields name, by
	// template id, for the loader to fetch — or not; see ResolveIconURL.
	IconRefs map[string]string
}

// Read reads a catalogue from what its address answered.
func Read(body []byte) (Catalogue, error) {
	switch {
	case bytes.HasPrefix(body, []byte{0x1f, 0x8b}):
		files, err := untar(body)
		if err != nil {
			return Catalogue{}, err
		}
		return fromFiles("tar.gz", files)
	case bytes.HasPrefix(body, []byte("PK\x03\x04")) || bytes.HasPrefix(body, []byte("PK\x05\x06")):
		files, err := unzip(body)
		if err != nil {
			return Catalogue{}, err
		}
		return fromFiles("zip", files)
	default:
		return fromIndex(body)
	}
}

// entry is one template's source, before it is read.
type entry struct {
	where string
	body  []byte
	// tooLarge is a file in an archive that was over the limit, and was
	// not read.
	tooLarge bool
}

// fromIndex reads a YAML or JSON document holding a list of templates.
func fromIndex(body []byte) (Catalogue, error) {
	// A page that answered HTML with a 200 is the ordinary way a wrong
	// address fails, and YAML would read one as a string.
	trimmed := bytes.TrimSpace(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf")))
	if len(trimmed) == 0 {
		return Catalogue{}, fmt.Errorf("%w: the file is empty", ErrUnreadable)
	}
	if trimmed[0] == '<' {
		return Catalogue{}, fmt.Errorf("%w: the address answered a web page, not a YAML or JSON file, a .tar.gz or a .zip", ErrUnreadable)
	}
	document, err := yaml.YAMLToJSON(trimmed)
	if err != nil {
		return Catalogue{}, fmt.Errorf("%w: it is not YAML or JSON: %w", ErrUnreadable, err)
	}

	var list []json.RawMessage
	switch {
	case bytes.HasPrefix(bytes.TrimSpace(document), []byte("[")):
		if err := json.Unmarshal(document, &list); err != nil {
			return Catalogue{}, fmt.Errorf("%w: %w", ErrUnreadable, err)
		}
	case bytes.HasPrefix(bytes.TrimSpace(document), []byte("{")):
		var index map[string]json.RawMessage
		if err := json.Unmarshal(document, &index); err != nil {
			return Catalogue{}, fmt.Errorf("%w: %w", ErrUnreadable, err)
		}
		raw, ok := index["templates"]
		if !ok {
			return Catalogue{}, fmt.Errorf("%w: an index is a list of templates under `templates:`, and this has %s",
				ErrUnreadable, keysOf(index))
		}
		for key := range index {
			if key != "templates" {
				return Catalogue{}, fmt.Errorf("%w: an index holds `templates:` and nothing else, and this also has %q",
					ErrUnreadable, key)
			}
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			return Catalogue{}, fmt.Errorf("%w: `templates:` is not a list: %w", ErrUnreadable, err)
		}
	default:
		return Catalogue{}, fmt.Errorf("%w: an index is a list of templates, and this is a single value", ErrUnreadable)
	}

	entries := make([]entry, 0, len(list))
	for i, raw := range list {
		entries = append(entries, entry{where: fmt.Sprintf("templates[%d]", i), body: raw})
	}
	return check("index", entries, nil)
}

// keysOf lists an object's keys for a sentence.
func keysOf(object map[string]json.RawMessage) string {
	if len(object) == 0 {
		return "nothing in it"
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, fmt.Sprintf("%q", key))
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// check reads each template on its own and holds it to the same rules as the
// built-in catalogue. One that fails is a problem, and the rest still load.
func check(format string, entries []entry, icons map[string]Icon) (Catalogue, error) {
	if len(entries) == 0 {
		return Catalogue{}, fmt.Errorf("%w: there are no templates in it", ErrUnreadable)
	}
	if len(entries) > MaxTemplates {
		return Catalogue{}, fmt.Errorf("%w: it has %d templates, and a catalogue holds at most %d",
			ErrUnreadable, len(entries), MaxTemplates)
	}

	out := Catalogue{Format: format, Templates: []templates.Template{}, Problems: []Problem{},
		Icons: map[string]Icon{}, IconRefs: map[string]string{}}
	taken := map[string]string{}
	for _, e := range entries {
		if e.tooLarge || len(e.body) > MaxTemplateFileBytes {
			out.Problems = append(out.Problems, Problem{File: e.where, Errors: []string{
				fmt.Sprintf("%s is larger than %s, and one template may be %s at most",
					e.where, sizeOf(MaxTemplateFileBytes), sizeOf(MaxTemplateFileBytes))}})
			continue
		}
		template, err := templates.Parse(e.body)
		if err != nil {
			id, name := identify(e.body)
			out.Problems = append(out.Problems, Problem{File: e.where, ID: id, Name: name,
				Errors: []string{fmt.Sprintf("%s could not be read: %v", e.where, err)}})
			continue
		}
		// The `icon` field of a private template is an address to fetch
		// the logo from, never a file name the panel serves; the loader
		// decides what Icon ends up as.
		ref := strings.TrimSpace(template.Icon)
		template.Icon = ""

		problems := templates.Validate(template)
		if first, clash := taken[template.ID]; clash && template.ID != "" {
			problems = append(problems, fmt.Sprintf(
				"%s has the id %q, which %s already has; ids are unique within a catalogue", e.where, template.ID, first))
		}
		if len(problems) > 0 {
			out.Problems = append(out.Problems, Problem{File: e.where, ID: template.ID, Name: template.Name, Errors: problems})
			continue
		}
		taken[template.ID] = e.where
		out.Templates = append(out.Templates, template)
		if icon, ok := icons[template.ID]; ok {
			out.Icons[template.ID] = icon
		} else if ref != "" {
			out.IconRefs[template.ID] = ref
		}
	}
	sort.SliceStable(out.Templates, func(i, j int) bool { return out.Templates[i].Name < out.Templates[j].Name })
	return out, nil
}

// identify pulls an id and a name out of a template that could not be read,
// so the problem can say which one it was.
func identify(body []byte) (string, string) {
	var label struct {
		ID   any `json:"id"`
		Name any `json:"name"`
	}
	if err := yaml.Unmarshal(body, &label); err != nil {
		return "", ""
	}
	text := func(v any) string {
		if s, ok := v.(string); ok && len(s) <= 100 {
			return s
		}
		return ""
	}
	return text(label.ID), text(label.Name)
}

// file is one file an archive holds, by its path inside it.
type file struct {
	name     string
	body     []byte
	tooLarge bool
}

// wanted says whether an archive's file could be part of a catalogue, before
// its layout is known: a template or a logo, no deeper than
// root/catalogue/x.yaml.
func wanted(name string) bool {
	if strings.Count(name, "/") > 2 {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") || part == "__MACOSX" {
			return false
		}
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".yaml", ".yml", ".svg", ".png", ".webp":
		return true
	}
	return false
}

// cleanName is a path inside an archive, made relative and refused when it
// climbs out.
func cleanName(name string) (string, bool) {
	name = strings.ReplaceAll(name, "\\", "/")
	cleaned := path.Clean(strings.TrimPrefix(name, "./"))
	if cleaned == "." || strings.HasPrefix(cleaned, "/") || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	return cleaned, true
}

// budget counts what an archive unpacks to, across every file in it.
type budget struct {
	entries int
	bytes   int64
}

func (b *budget) take(entries int) error {
	b.entries += entries
	if b.entries > MaxArchiveEntries {
		return fmt.Errorf("%w: the archive holds more than %d files", ErrUnreadable, MaxArchiveEntries)
	}
	return nil
}

// spend counts bytes that will be inflated, read or not: a tar reader skips
// a file it is not asked for by decompressing it, so a file of zeros nobody
// wanted still costs what it declares.
func (b *budget) spend(n int64) error {
	b.bytes += n
	if n < 0 || b.bytes > MaxUnpackedBytes {
		return fmt.Errorf("%w: the archive unpacks to more than %s", ErrUnreadable, sizeOf(MaxUnpackedBytes))
	}
	return nil
}

// read reads one file, no more than it may be. It reports whether the whole
// file fitted.
func read(r io.Reader, limit int) ([]byte, bool, error) {
	body, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return nil, false, fmt.Errorf("%w: the archive could not be read: %w", ErrUnreadable, err)
	}
	if len(body) > limit {
		return nil, false, nil
	}
	return body, true, nil
}

// limitFor is how large a file of this kind may be.
func limitFor(name string) int {
	switch strings.ToLower(path.Ext(name)) {
	case ".yaml", ".yml":
		return MaxTemplateFileBytes
	}
	return MaxIconBytes
}

// untar reads the files of a .tar.gz a catalogue could be made of.
func untar(body []byte) ([]file, error) {
	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: it starts like a .tar.gz and does not open as one: %w", ErrUnreadable, err)
	}
	defer func() { _ = gz.Close() }()
	archive := tar.NewReader(gz)
	var out []file
	var spent budget
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: the archive could not be read: %w", ErrUnreadable, err)
		}
		if err := spent.take(1); err != nil {
			return nil, err
		}
		if err := spent.spend(header.Size); err != nil {
			return nil, err
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		name, ok := cleanName(header.Name)
		if !ok || !wanted(name) {
			continue
		}
		body, whole, err := read(archive, limitFor(name))
		if err != nil {
			return nil, err
		}
		// One too large is kept, without its bytes, so a template that is
		// says so rather than vanishing.
		out = append(out, file{name: name, body: body, tooLarge: !whole})
	}
	return out, nil
}

// unzip reads the files of a .zip a catalogue could be made of.
func unzip(body []byte) ([]file, error) {
	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, fmt.Errorf("%w: it starts like a .zip and does not open as one: %w", ErrUnreadable, err)
	}
	var spent budget
	if err := spent.take(len(archive.File)); err != nil {
		return nil, err
	}
	var out []file
	for _, entry := range archive.File {
		if !entry.Mode().IsRegular() {
			continue
		}
		name, ok := cleanName(entry.Name)
		if !ok || !wanted(name) {
			continue
		}
		// The sizes in a zip's directory are what it says about itself;
		// what is read is limited whatever they say.
		reader, err := entry.Open()
		if err != nil {
			return nil, fmt.Errorf("%w: %s could not be opened: %w", ErrUnreadable, name, err)
		}
		content, whole, err := read(reader, limitFor(name))
		reader.Close()
		if err != nil {
			return nil, err
		}
		if err := spent.spend(int64(len(content))); err != nil {
			return nil, err
		}
		out = append(out, file{name: name, body: content, tooLarge: !whole})
	}
	return out, nil
}

// fromFiles finds a catalogue among an archive's files.
func fromFiles(format string, files []file) (Catalogue, error) {
	files = stripCommonRoot(files)

	dir := ""
	for _, f := range files {
		if strings.HasPrefix(f.name, "catalogue/") {
			dir = "catalogue/"
			break
		}
	}
	var entries []entry
	icons := map[string]Icon{}
	for _, f := range files {
		ext := strings.ToLower(path.Ext(f.name))
		switch {
		case (ext == ".yaml" || ext == ".yml") && path.Dir(f.name)+"/" == dirOrRoot(dir):
			entries = append(entries, entry{where: f.name, body: f.body, tooLarge: f.tooLarge})
		case path.Dir(f.name) == "icons" && !f.tooLarge:
			id := strings.TrimSuffix(path.Base(f.name), path.Ext(f.name))
			if contentType, ok := Sniff(f.body); ok && contentType == iconTypes[ext] {
				icons[id] = Icon{ContentType: contentType, Body: f.body}
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].where < entries[j].where })
	if len(entries) == 0 {
		return Catalogue{}, fmt.Errorf("%w: the archive has no *.yaml files in catalogue/ or at its top", ErrUnreadable)
	}
	return check(format, entries, icons)
}

// dirOrRoot is the directory templates are read from, as path.Dir writes it.
func dirOrRoot(dir string) string {
	if dir == "" {
		return "./"
	}
	return dir
}

// stripCommonRoot looks through the one directory a repository's archive
// wraps everything in.
func stripCommonRoot(files []file) []file {
	if len(files) == 0 {
		return files
	}
	root, _, found := strings.Cut(files[0].name, "/")
	if !found {
		return files
	}
	for _, f := range files {
		first, _, found := strings.Cut(f.name, "/")
		if !found || first != root {
			return files
		}
	}
	out := make([]file, 0, len(files))
	for _, f := range files {
		out = append(out, file{name: strings.TrimPrefix(f.name, root+"/"), body: f.body, tooLarge: f.tooLarge})
	}
	return out
}

// iconTypes is what each logo's extension has to turn out to be.
var iconTypes = map[string]string{
	".svg":  "image/svg+xml",
	".png":  "image/png",
	".webp": "image/webp",
}
