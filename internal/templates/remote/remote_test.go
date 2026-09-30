package remote

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// A catalogue is somebody else's file, read by the panel every day without
// anybody watching. What is checked here: both shapes it can come in, that
// each template is held to the built-in catalogue's rules on its own, and that
// something which is not a catalogue — or is one built to hurt — is refused
// as a whole and does not replace the copy the team already had.

// templateYAML is a template the built-in catalogue's checks accept.
func templateYAML(id, name, image string) string {
	return fmt.Sprintf(`id: %s
name: %s
description: An internal tool.
category: developer
website: https://tools.example.org/%s
services:
- name: %s
  image: %s
  port: 8080
  public: true
  mem_request_mb: 64
  mem_limit_mb: 256
`, id, name, id, id, image)
}

const svgLogo = `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><rect width="1" height="1"/></svg>`

func TestAnIndexIsReadAsYAMLOrAsJSON(t *testing.T) {
	yamlIndex := "templates:\n" +
		indent(templateYAML("wiki", "Wiki", "registry.example.org/wiki:3.2.1")) +
		indent(templateYAML("licences", "Licence server", "registry.example.org/licences:1.0.4"))
	jsonIndex := `[{"id":"wiki","name":"Wiki","description":"An internal tool.","category":"developer",
		"website":"https://tools.example.org/wiki","icon":"icons/wiki.svg",
		"services":[{"name":"wiki","image":"registry.example.org/wiki:3.2.1","port":8080,"public":true}]}]`

	for name, body := range map[string]string{"yaml": yamlIndex, "json": jsonIndex} {
		catalogue, err := Read([]byte(body))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if catalogue.Format != "index" || len(catalogue.Problems) != 0 || len(catalogue.Templates) == 0 {
			t.Fatalf("%s: read as %+v", name, catalogue)
		}
		for _, template := range catalogue.Templates {
			if template.Databases == nil || template.Inputs == nil {
				t.Errorf("%s: %s has nil lists, which the API answers as null", name, template.ID)
			}
			if template.Icon != "" {
				t.Errorf("%s: %s kept its icon field %q as the name of a file the panel serves", name, template.ID, template.Icon)
			}
		}
	}
	catalogue, _ := Read([]byte(jsonIndex))
	if catalogue.IconRefs["wiki"] != "icons/wiki.svg" {
		t.Errorf("the icon the template names was not kept for the loader: %+v", catalogue.IconRefs)
	}
}

// indent puts a template under `templates:` as one list item.
func indent(template string) string {
	var b strings.Builder
	for i, line := range strings.Split(strings.TrimRight(template, "\n"), "\n") {
		if i == 0 {
			b.WriteString("- " + line + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}
	return b.String()
}

func TestAnArchiveIsReadLikeTheBuiltInCatalogue(t *testing.T) {
	// GitHub's own archive of a repository: everything inside one directory
	// named after the commit, templates in catalogue/, logos in icons/, and
	// YAML elsewhere that is not a template at all.
	files := map[string]string{
		"acme-catalogue-3f2a9c1/catalogue/wiki.yaml":         templateYAML("wiki", "Wiki", "registry.example.org/wiki:3.2.1"),
		"acme-catalogue-3f2a9c1/catalogue/licences.yml":      templateYAML("licences", "Licence server", "registry.example.org/licences:1.0.4"),
		"acme-catalogue-3f2a9c1/icons/wiki.svg":              svgLogo,
		"acme-catalogue-3f2a9c1/icons/licences.svg":          "<html><body>not a picture</body></html>",
		"acme-catalogue-3f2a9c1/icons/gone.svg":              svgLogo,
		"acme-catalogue-3f2a9c1/.github/workflows/check.yml": "on: push\n",
		"acme-catalogue-3f2a9c1/docker-compose.yml":          "services: {}\n",
		"acme-catalogue-3f2a9c1/README.md":                   "# Our templates\n",
	}
	for format, body := range map[string][]byte{"tar.gz": tarGz(t, files), "zip": zipOf(t, files)} {
		catalogue, err := Read(body)
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		if catalogue.Format != format {
			t.Errorf("read as %q, want %q", catalogue.Format, format)
		}
		if len(catalogue.Templates) != 2 || len(catalogue.Problems) != 0 {
			t.Fatalf("%s: %d templates and problems %+v; the compose file and the workflow are not templates",
				format, len(catalogue.Templates), catalogue.Problems)
		}
		if catalogue.Templates[0].Name != "Licence server" {
			t.Errorf("%s: the templates are not sorted by name: %s first", format, catalogue.Templates[0].Name)
		}
		if icon, ok := catalogue.Icons["wiki"]; !ok || icon.ContentType != "image/svg+xml" {
			t.Errorf("%s: the wiki's logo was not kept: %+v", format, catalogue.Icons)
		}
		if _, ok := catalogue.Icons["licences"]; ok {
			t.Errorf("%s: an HTML page called licences.svg was kept as a logo", format)
		}
		if _, ok := catalogue.Icons["gone"]; ok {
			t.Errorf("%s: a logo for a template that is not there was kept", format)
		}
	}

	// And flat, with the templates at the top: an archive of the directory
	// itself rather than of a repository.
	catalogue, err := Read(tarGz(t, map[string]string{"wiki.yaml": templateYAML("wiki", "Wiki", "registry.example.org/wiki:3.2.1")}))
	if err != nil || len(catalogue.Templates) != 1 {
		t.Fatalf("a flat archive read as %+v, %v", catalogue, err)
	}
}

// One template that fails is listed with why, and the rest still load.
func TestATemplateThatFailsIsListedWithWhyAndTheRestStillLoad(t *testing.T) {
	index := "templates:\n" +
		indent(templateYAML("wiki", "Wiki", "registry.example.org/wiki:3.2.1")) +
		indent(templateYAML("latest", "Floating", "registry.example.org/floating:latest")) +
		indent(templateYAML("wiki", "Second wiki", "registry.example.org/wiki:3.2.2")) +
		indent(templateYAML("typo", "Typo", "registry.example.org/typo:1.2.3")+"volumes_typo: []\n") +
		indent(strings.Replace(templateYAML("orphan", "Orphan", "registry.example.org/orphan:2.0.1"),
			"services:", "databases:\n- name: orphan-db\n  engine: postgres\n  storage_gb: 5\n  link_to: [web]\n  var_name: DATABASE_URL\nservices:", 1))

	catalogue, err := Read([]byte(index))
	if err != nil {
		t.Fatal(err)
	}
	if len(catalogue.Templates) != 1 || catalogue.Templates[0].ID != "wiki" || catalogue.Templates[0].Name != "Wiki" {
		t.Fatalf("installable: %+v", catalogue.Templates)
	}
	want := map[string]string{
		"templates[1]": "whatever is newest",
		"templates[2]": `which templates[0] already has`,
		"templates[3]": "volumes_typo",
		"templates[4]": `links to "web", which is not a service`,
	}
	if len(catalogue.Problems) != len(want) {
		t.Fatalf("problems: %+v", catalogue.Problems)
	}
	for _, problem := range catalogue.Problems {
		says, ok := want[problem.File]
		if !ok {
			t.Errorf("an unexpected problem: %+v", problem)
			continue
		}
		if !strings.Contains(strings.Join(problem.Errors, "\n"), says) {
			t.Errorf("%s says %q, which does not explain %q", problem.File, problem.Errors, says)
		}
		if problem.ID == "" {
			t.Errorf("%s does not say which template it is: %+v", problem.File, problem)
		}
	}
}

func TestWhatIsNotACatalogueIsRefusedAsAWhole(t *testing.T) {
	cases := map[string][]byte{
		"an empty file":             nil,
		"a web page":                []byte("<!doctype html><html><body>Sign in</body></html>"),
		"not YAML":                  []byte("templates: [\n  - id: x\n  bad"),
		"a scalar":                  []byte("just a sentence"),
		"an object with no list":    []byte("apps:\n- id: wiki\n"),
		"templates and more":        []byte("templates: []\nversion: 2\n"),
		"no templates in it":        []byte("templates: []\n"),
		"an archive of other files": tarGz(t, map[string]string{"README.md": "# nothing\n"}),
		"a broken archive":          append([]byte{0x1f, 0x8b}, []byte("not gzip at all")...),
		"a broken zip":              []byte("PK\x03\x04 not really"),
	}
	for name, body := range cases {
		if _, err := Read(body); !errors.Is(err, ErrUnreadable) {
			t.Errorf("%s: answered %v, not that it is unreadable", name, err)
		}
	}

	var many strings.Builder
	many.WriteString("templates:\n")
	for i := 0; i <= MaxTemplates; i++ {
		many.WriteString(indent(templateYAML(fmt.Sprintf("t%d", i), fmt.Sprintf("T%d", i), "example/t:1.2.3")))
	}
	if _, err := Read([]byte(many.String())); !errors.Is(err, ErrUnreadable) || !strings.Contains(err.Error(), "at most") {
		t.Errorf("a catalogue of %d templates answered %v", MaxTemplates+1, err)
	}
}

// A gzip bomb is a few kilobytes that inflate to gigabytes. The sizes a tar
// declares are what it spends reading, so they are what is counted, and one
// template file too large to be a template is a problem of its own.
func TestAnArchiveCannotUnpackForever(t *testing.T) {
	huge := strings.Repeat("0", MaxUnpackedBytes+1)
	body := tarGz(t, map[string]string{
		"catalogue/wiki.yaml": templateYAML("wiki", "Wiki", "registry.example.org/wiki:3.2.1"),
		"filler.bin":          huge,
	})
	if len(body) > MaxDownloadBytes {
		t.Fatalf("the bomb is %d bytes, which the download limit would stop first", len(body))
	}
	if _, err := Read(body); !errors.Is(err, ErrUnreadable) || !strings.Contains(err.Error(), "unpacks to more than") {
		t.Errorf("an archive unpacking to %d bytes answered %v", len(huge), err)
	}

	large := tarGz(t, map[string]string{
		"catalogue/wiki.yaml":  templateYAML("wiki", "Wiki", "registry.example.org/wiki:3.2.1"),
		"catalogue/large.yaml": templateYAML("large", "Large", "example/large:1.2.3") + "notes: " + strings.Repeat("x", MaxTemplateFileBytes) + "\n",
	})
	catalogue, err := Read(large)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalogue.Templates) != 1 || len(catalogue.Problems) != 1 ||
		!strings.Contains(catalogue.Problems[0].Errors[0], "may be") {
		t.Errorf("a template file over the limit read as %+v / %+v", catalogue.Templates, catalogue.Problems)
	}
}

// A path that climbs out of the archive is not read, whatever it is called.
func TestAnArchivePathThatClimbsOutIsIgnored(t *testing.T) {
	for _, name := range []string{"../wiki.yaml", "/etc/wiki.yaml", "a/../../wiki.yaml"} {
		if _, ok := cleanName(name); ok {
			t.Errorf("%s was accepted as a path inside the archive", name)
		}
	}
	if name, ok := cleanName("./catalogue/wiki.yaml"); !ok || name != "catalogue/wiki.yaml" {
		t.Errorf("an ordinary path became %q, %v", name, ok)
	}
}

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	archive := tar.NewWriter(gz)
	for _, name := range sortedNames(files) {
		body := files[name]
		if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)
	for _, name := range sortedNames(files) {
		w, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sortedNames(files map[string]string) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return names
}
