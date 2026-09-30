// Package docsite serves the documentation embedded in the binary.
//
// It renders the Markdown in docs/ to HTML at request time. Rendering at
// request time rather than at build time keeps the binary smaller and means a
// page is never out of step with its source; the pages are small enough that
// the cost does not show up.
package docsite

import (
	"bytes"
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"

	"skifity/internal/version"
)

// Page is one document, in the order the index lists them.
type Page struct {
	// Slug is the URL segment, which is the filename without .md.
	Slug string
	// Title is the first heading in the file.
	Title string
	// Summary is the first paragraph, used on the index.
	Summary string
}

// Site serves the documentation under a prefix.
type Site struct {
	files  fs.FS
	prefix string
	md     goldmark.Markdown

	once  sync.Once
	pages []Page
	index map[string]Page
}

// order is the reading order of the index. A document not listed here still
// works at its own URL; it simply does not appear on the front page, which is
// how a new page can be added without deciding where it belongs first.
var order = []string{
	"quick-start",
	"concepts",
	"templates",
	"adding-servers",
	"firewall",
	"plugins",
	"backups",
	"configuration",
	"cli",
	"troubleshooting",
	"faq",
	"performance",
}

// New returns a Site serving files under prefix, which must start and end with
// a slash, such as "/docs/".
func New(files fs.FS, prefix string) *Site {
	return &Site{
		files:  files,
		prefix: prefix,
		md: goldmark.New(
			// Tables and strikethrough, because the documentation uses them;
			// no raw HTML, so a page cannot inject a script into the panel's
			// own origin even if a future page is written carelessly.
			goldmark.WithExtensions(extension.GFM),
			goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		),
	}
}

func (s *Site) load() {
	s.once.Do(func() {
		s.index = map[string]Page{}
		entries, err := fs.ReadDir(s.files, ".")
		if err != nil {
			return
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			slug := strings.TrimSuffix(entry.Name(), ".md")
			source, err := fs.ReadFile(s.files, entry.Name())
			if err != nil {
				continue
			}
			page := Page{Slug: slug, Title: firstHeading(source), Summary: firstParagraph(source)}
			if page.Title == "" {
				page.Title = slug
			}
			s.index[slug] = page
		}
		for _, slug := range order {
			if page, ok := s.index[slug]; ok {
				s.pages = append(s.pages, page)
			}
		}
		// Anything not in the reading order goes after it, alphabetically, so
		// a page is never invisible just because nobody updated a list.
		var rest []Page
		for slug, page := range s.index {
			if !contains(order, slug) {
				rest = append(rest, page)
			}
		}
		sort.Slice(rest, func(i, j int) bool { return rest[i].Slug < rest[j].Slug })
		s.pages = append(s.pages, rest...)
	})
}

// Handler serves the documentation. Mount it at the same prefix it was made
// with.
func (s *Site) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.load()

		rest := strings.TrimPrefix(path.Clean(r.URL.Path), strings.TrimSuffix(s.prefix, "/"))
		rest = strings.Trim(rest, "/")

		switch {
		case rest == "":
			s.writeIndex(w)
		case strings.HasPrefix(rest, "images/"):
			s.serveFile(w, r, rest)
		default:
			// A trailing .md is what someone pasting a link from the
			// repository will have; both spellings work.
			s.writePage(w, strings.TrimSuffix(rest, ".md"))
		}
	})
}

func (s *Site) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	data, err := fs.ReadFile(s.files, name)
	if err != nil {
		s.notFound(w, name)
		return
	}
	switch path.Ext(name) {
	case ".svg":
		w.Header().Set("Content-Type", "image/svg+xml")
	case ".png":
		w.Header().Set("Content-Type", "image/png")
	}
	// Documentation images change only when the binary does.
	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeContent(w, r, name, versionTime, bytes.NewReader(data))
}

func (s *Site) writePage(w http.ResponseWriter, slug string) {
	source, err := fs.ReadFile(s.files, slug+".md")
	if err != nil {
		s.notFound(w, slug)
		return
	}

	var body bytes.Buffer
	if err := s.md.Convert(rewriteLinks(source, s.prefix), &body); err != nil {
		http.Error(w, "that page could not be rendered", http.StatusInternalServerError)
		return
	}

	page := s.index[slug]
	if page.Title == "" {
		page.Title = slug
	}
	s.writeHTML(w, page.Title, body.String())
}

func (s *Site) writeIndex(w http.ResponseWriter) {
	var body strings.Builder
	fmt.Fprintf(&body, "<h1>%s documentation</h1>\n", html.EscapeString(version.Name))
	fmt.Fprintf(&body, "<p class=\"lead\">%s</p>\n<ul class=\"index\">\n",
		html.EscapeString(version.Tagline))
	for _, page := range s.pages {
		fmt.Fprintf(&body, "<li><a href=\"%s%s\">%s</a><span>%s</span></li>\n",
			s.prefix, page.Slug, html.EscapeString(page.Title), html.EscapeString(page.Summary))
	}
	body.WriteString("</ul>\n")
	s.writeHTML(w, version.Name+" documentation", body.String())
}

func (s *Site) notFound(w http.ResponseWriter, what string) {
	w.WriteHeader(http.StatusNotFound)
	s.writeHTML(w, "Not found", fmt.Sprintf(
		`<h1>There is no page called %q</h1>
<p>It may have been renamed. <a href="%s">Everything there is</a> is one page away.</p>`,
		html.EscapeString(what), s.prefix))
}

func (s *Site) writeHTML(w http.ResponseWriter, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprintf(w, pageTemplate, html.EscapeString(title), version.Name, s.prefix, body)
}

// rewriteLinks turns the repository's relative links into ones that work when
// the page is served. "concepts.md" and "images/setup.png" are correct on
// GitHub and meaningless here without this.
func rewriteLinks(source []byte, prefix string) []byte {
	out := relativeLink.ReplaceAll(source, []byte("]("+prefix+"$1$2)"))
	return relativeImage.ReplaceAll(out, []byte("]("+prefix+"images/$1)"))
}

var (
	// [text](concepts.md#anchor) but not [text](https://...) or [text](/docs/x)
	relativeLink = regexp.MustCompile(`\]\(([a-z0-9-]+)\.md(#[a-z0-9-]*)?\)`)
	// ![alt](images/setup.png). Raw <img> tags are not handled because raw
	// HTML is not rendered at all: see New.
	relativeImage = regexp.MustCompile(`\]\(images/([^)]+)\)`)
)

func firstHeading(source []byte) string {
	for _, line := range strings.Split(string(source), "\n") {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func firstParagraph(source []byte) string {
	lines := strings.Split(string(source), "\n")
	seenHeading := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			seenHeading = true
			continue
		}
		if !seenHeading || trimmed == "" || strings.HasPrefix(trimmed, "<") ||
			strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "```") {
			continue
		}
		return trimmed
	}
	return ""
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
