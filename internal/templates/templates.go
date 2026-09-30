// Package templates is the one-click app catalogue.
//
// A template is a description of an app, the databases it needs and the
// variables that wire them together. Installing one creates ordinary Skifity
// resources, so there is nothing special about a templated app afterwards: it
// can be scaled, backed up and rolled back like any other.
package templates

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"

	"sigs.k8s.io/yaml"
)

// The catalogue is data, not code.
//
// It started as a Go literal, which is fine for eight entries and impossible
// for three hundred. Coolify's catalogue is the single most-cited reason people
// choose it, and it got there because a template is a file somebody can add
// without touching the product. So this is a directory of files, one per
// template, read at startup and checked by the tests in this package.
//
// Every image names a version. A floating tag is not a version: two deploys of
// the same app run different software, a rollback restores a tag rather than
// the thing that worked, and an upstream release arrives on a restart nobody
// asked for. Where upstream publishes a series tag that takes patches without
// breaking changes, that is what is used; where it does not, an exact version
// is, and moving it forward is a change to a file here. A test refuses anything
// that ends in `latest`.
//
//go:embed catalogue/*.yaml
var files embed.FS

// The logos, one per template, named after the template they belong to.
//
// A catalogue of 282 grey squares with a letter in them is a catalogue nobody
// wants to look through, and every product in this category shows logos. They
// are vendored rather than loaded from a CDN because the panel's own
// Content-Security-Policy says `img-src 'self'`: pointing at somebody else's
// server would mean widening it, telling that server which self-hosted apps
// each user is browsing, and leaving an offline install without pictures.
//
// hack/fetch_icons.py refreshes them. See internal/templates/icons/README.md
// for where they come from and under what licence.
//
//go:embed icons
var iconFiles embed.FS

var (
	once      sync.Once
	catalogue []Template
	loadErr   error
)

// load reads every file in the catalogue once.
//
// A file that cannot be read is a build-time mistake, not a runtime condition:
// the tests in this package read the same directory and fail on it first.
func load() {
	entries, err := fs.ReadDir(files, "catalogue")
	if err != nil {
		loadErr = fmt.Errorf("read the template catalogue: %w", err)
		return
	}
	for _, entry := range entries {
		body, err := files.ReadFile("catalogue/" + entry.Name())
		if err != nil {
			loadErr = fmt.Errorf("read %s: %w", entry.Name(), err)
			return
		}
		var template Template
		if err := yaml.Unmarshal(body, &template); err != nil {
			loadErr = fmt.Errorf("%s: %w", entry.Name(), err)
			return
		}
		// A nil slice marshals as `null`, and the API says these are arrays.
		// 157 of the templates in this directory have no database, and every
		// one of them answered `"databases": null` — which the panel iterated,
		// which threw, which meant the Templates page rendered an error
		// boundary instead of the catalogue on every install since it grew
		// past the hand-written eight. Nothing caught it: the structural tests
		// below check the Go value, not the JSON, and nothing ever opened the
		// page.
		if template.Databases == nil {
			template.Databases = []DatabaseSpec{}
		}
		if template.Inputs == nil {
			template.Inputs = []Input{}
		}
		if template.Services == nil {
			template.Services = []Service{}
		}
		template.Icon = iconFor(template.ID)
		catalogue = append(catalogue, template)
	}
	// Sorted by name, because a directory listing is not an order anybody
	// chose and the panel groups by category anyway.
	sort.Slice(catalogue, func(i, j int) bool { return catalogue[i].Name < catalogue[j].Name })
}

// iconExtensions are what the collection publishes, best first. An SVG is a few
// kilobytes and scales; WebP is the fallback for a logo that only exists as a
// bitmap.
var iconExtensions = []string{".svg", ".webp"}

// iconFor returns the file name of a template's logo, or "" when there is none.
func iconFor(id string) string {
	for _, extension := range iconExtensions {
		name := id + extension
		if _, err := fs.Stat(iconFiles, "icons/"+name); err == nil {
			return name
		}
	}
	return ""
}

// IconContentTypes maps an icon's extension to what it has to be served as.
// A browser will not render an SVG sent as text/plain, and guessing from the
// bytes is how an SVG becomes a download.
var IconContentTypes = map[string]string{
	".svg":  "image/svg+xml",
	".webp": "image/webp",
}

// ReadIcon returns a template's logo and the type to serve it as.
//
// The name comes from the catalogue rather than from a request, so there is no
// path to traverse — but it is checked anyway, because the one that is not
// checked is the one that changes later.
func ReadIcon(id string) (data []byte, contentType string, ok bool) {
	template, found := Lookup(id)
	if !found || template.Icon == "" {
		return nil, "", false
	}
	dot := strings.LastIndex(template.Icon, ".")
	if dot < 0 {
		// Unreachable while the name comes from iconFor, which builds it from
		// a known extension — and a slice on -1 is a panic in the one function
		// that is documented as checking anyway.
		return nil, "", false
	}
	contentType, known := IconContentTypes[template.Icon[dot:]]
	if !known {
		return nil, "", false
	}
	body, err := iconFiles.ReadFile("icons/" + template.Icon)
	if err != nil {
		return nil, "", false
	}
	return body, contentType, true
}

// Err reports a catalogue that could not be read. The tests in this package
// check it, so a broken file fails the build rather than the panel.
func Err() error {
	once.Do(load)
	return loadErr
}

// Template is one installable application.
type Template struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	// Website is where to learn what the software does.
	Website string `json:"website"`
	// Beta marks templates that are known to need extra care.
	Beta bool `json:"beta,omitempty"`
	// Services are the containers to run.
	Services []Service `json:"services"`
	// Databases are provisioned first, so their connection strings exist
	// before the services start.
	Databases []DatabaseSpec `json:"databases"`
	// Inputs are asked for at install time, such as an admin email.
	Inputs []Input `json:"inputs,omitempty"`
	// Notes are shown in the install dialog and again on the page the install
	// lands on, for the steps we cannot automate.
	Notes string `json:"notes,omitempty"`
	// Icon is the file name of this template's logo, or empty when the
	// collection has none for it. It is filled in by the loader from what is
	// on disk rather than written in the YAML, so adding a logo is adding a
	// file and nothing else.
	Icon string `json:"icon,omitempty"`
}

// Service is one container in a template.
type Service struct {
	Name         string            `json:"name"`
	Image        string            `json:"image"`
	Port         int               `json:"port"`
	HealthPath   string            `json:"health_path,omitempty"`
	Variables    map[string]string `json:"variables,omitempty"`
	Volumes      []VolumeSpec      `json:"volumes,omitempty"`
	MemRequestMB int               `json:"mem_request_mb,omitempty"`
	MemLimitMB   int               `json:"mem_limit_mb,omitempty"`
	CPURequestM  int               `json:"cpu_request_m,omitempty"`
	CPULimitM    int               `json:"cpu_limit_m,omitempty"`
	// Public means this service gets a domain; a worker does not.
	Public bool `json:"public"`
}

// VolumeSpec is persistent storage a template needs.
type VolumeSpec struct {
	Name      string `json:"name"`
	MountPath string `json:"mount_path"`
	SizeGB    int    `json:"size_gb"`
}

// DatabaseSpec is a managed database a template needs.
type DatabaseSpec struct {
	Name      string `json:"name"`
	Engine    string `json:"engine"`
	StorageGB int    `json:"storage_gb"`
	// LinkTo names the services that get the connection string, and VarName is
	// the variable it arrives as.
	//
	// It is a list because a stack is usually a web app and a worker sharing
	// one database, and linking only the first of them produces a worker that
	// starts without the variable it cannot run without.
	LinkTo  []string `json:"link_to"`
	VarName string   `json:"var_name"`
}

// Input is a value asked for at install time.
type Input struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Help     string `json:"help,omitempty"`
	Default  string `json:"default,omitempty"`
	Secret   bool   `json:"secret,omitempty"`
	Required bool   `json:"required,omitempty"`
	// Generate fills the value with a random secret when left empty, which is
	// what most of these actually want.
	Generate bool `json:"generate,omitempty"`
}

// All returns the catalogue.
func All() []Template {
	once.Do(load)
	return catalogue
}

// Lookup finds a template by id.
func Lookup(id string) (Template, bool) {
	for _, t := range All() {
		if t.ID == id {
			return t, true
		}
	}
	return Template{}, false
}

// Categories lists the distinct categories, for the filter in the UI.
func Categories() []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range All() {
		if !seen[t.Category] {
			seen[t.Category] = true
			out = append(out, t.Category)
		}
	}
	return out
}

// Search filters the catalogue by a free-text query.
func Search(query string) []Template {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return All()
	}
	var out []Template
	for _, t := range All() {
		haystack := strings.ToLower(t.Name + " " + t.Description + " " + t.Category)
		if strings.Contains(haystack, query) {
			out = append(out, t)
		}
	}
	return out
}
