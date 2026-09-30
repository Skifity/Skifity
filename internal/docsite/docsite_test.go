package docsite

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"skifity/docs"
)

func serve(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	New(docs.FS(), "/docs/").Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	return recorder
}

// Every page listed in the reading order has to exist, or the index links to
// nothing.
func TestEveryListedPageRenders(t *testing.T) {
	for _, slug := range order {
		t.Run(slug, func(t *testing.T) {
			response := serve(t, "/docs/"+slug)
			if response.Code != http.StatusOK {
				t.Fatalf("got %d, want 200", response.Code)
			}
			body := response.Body.String()
			if !strings.Contains(body, "<h1") {
				t.Error("the page has no heading")
			}
			// A link written for the repository is meaningless once served.
			if strings.Contains(body, `href="concepts.md`) ||
				strings.Contains(body, `src="images/`) {
				t.Error("a relative link survived into the served page")
			}
		})
	}
}

// The index has to name every page it ships, with something to read.
func TestIndexListsEveryPage(t *testing.T) {
	response := serve(t, "/docs/")
	if response.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", response.Code)
	}
	body := response.Body.String()
	for _, slug := range order {
		if !strings.Contains(body, `href="/docs/`+slug+`"`) {
			t.Errorf("the index does not link to %s", slug)
		}
	}
}

// This is the test that earns its keep: every documentation link in the error
// catalogue must resolve to a page that exists, at an anchor that exists.
// A failure message that ends in a dead link is worse than one with no link.
func TestErrorCatalogueLinksResolve(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("locate the repository root: %v", err)
	}

	referenced := map[string][]string{}
	pattern := regexp.MustCompile(`WithDocs\("([^"]+)"\)`)
	err = filepath.Walk(filepath.Join(root, "internal"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range pattern.FindAllStringSubmatch(string(source), -1) {
			rel, _ := filepath.Rel(root, path)
			referenced[match[1]] = append(referenced[match[1]], rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan for documentation links: %v", err)
	}
	if len(referenced) == 0 {
		t.Fatal("no documentation links were found, which means this test is not testing anything")
	}

	for link, sources := range referenced {
		t.Run(link, func(t *testing.T) {
			target, anchor, _ := strings.Cut(link, "#")
			response := serve(t, target)
			if response.Code != http.StatusOK {
				t.Fatalf("%s is linked from %s but answers %d",
					link, strings.Join(sources, ", "), response.Code)
			}
			if anchor == "" {
				return
			}
			// Goldmark generates the ids, so the anchor has to match one it
			// actually produced rather than one somebody expected.
			if !strings.Contains(response.Body.String(), `id="`+anchor+`"`) {
				t.Errorf("%s has no heading with id %q; the page exists but the link lands at the top",
					target, anchor)
			}
		})
	}
}

// A page that does not exist must say so rather than rendering an empty shell.
func TestMissingPageIsNotFound(t *testing.T) {
	response := serve(t, "/docs/there-is-no-such-page")
	if response.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", response.Code)
	}
	if !strings.Contains(response.Body.String(), "there-is-no-such-page") {
		t.Error("the message should name the page that was asked for")
	}
}

// Raw HTML in a document must not reach the browser: the documentation is
// served from the panel's own origin, so a script in a page would run with it.
func TestRawHTMLIsNotRendered(t *testing.T) {
	response := serve(t, "/docs/quick-start")
	if strings.Contains(response.Body.String(), "<script") {
		t.Error("a script tag reached the rendered page")
	}
}

// A link from one shipped page to another has to resolve on the server too,
// not only on GitHub. The documentation is read most by someone whose panel is
// broken, and a dead link is exactly the wrong thing to hand them.
func TestLinksBetweenPagesResolve(t *testing.T) {
	pattern := regexp.MustCompile(`\]\(([a-z0-9-]+)\.md(#[a-z0-9-]*)?\)`)

	entries, err := os.ReadDir(filepath.Join("..", "..", "docs"))
	if err != nil {
		t.Fatalf("read docs/: %v", err)
	}

	checked := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		slug := strings.TrimSuffix(entry.Name(), ".md")
		// Only the shipped pages matter: the research notes and the decision
		// record may link wherever they like.
		if serve(t, "/docs/"+slug).Code != http.StatusOK {
			continue
		}
		source, err := os.ReadFile(filepath.Join("..", "..", "docs", entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		for _, match := range pattern.FindAllStringSubmatch(string(source), -1) {
			checked++
			target := "/docs/" + match[1]
			response := serve(t, target)
			if response.Code != http.StatusOK {
				t.Errorf("%s links to %s.md, which the panel does not serve",
					entry.Name(), match[1])
				continue
			}
			if anchor := strings.TrimPrefix(match[2], "#"); anchor != "" {
				if !strings.Contains(response.Body.String(), `id="`+anchor+`"`) {
					t.Errorf("%s links to %s.md#%s, and that heading does not exist",
						entry.Name(), match[1], anchor)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no links between pages were found, which means this test is not testing anything")
	}
}
