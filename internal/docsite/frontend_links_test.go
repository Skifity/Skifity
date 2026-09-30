package docsite

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every /docs link the interface offers has to land somewhere.
//
// The catalogue's WithDocs links are already checked, page and anchor, because
// an error that sends somebody to a missing page is worse than one that sends
// them nowhere. A link written straight into a .tsx file was not covered by
// anything: the Domains tab, the sidebar and the dashboard each carry one, and
// a page renamed in docs/ would have broken all three silently.
func TestEveryDocsLinkInTheInterfaceResolves(t *testing.T) {
	links := regexp.MustCompile(`href="(/docs/[^"]*)"`)

	found := 0
	root := filepath.Join("..", "..", "web", "src")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".tsx") {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range links.FindAllStringSubmatch(string(body), -1) {
			found++
			target, anchor, _ := strings.Cut(match[1], "#")
			response := serve(t, target)
			if response.Code != http.StatusOK {
				t.Errorf("%s links to %s, which answers %d", filepath.Base(path), target, response.Code)
				continue
			}
			if anchor != "" && !strings.Contains(response.Body.String(), `id="`+anchor+`"`) {
				t.Errorf("%s links to %s, and that page has no heading with the id %q",
					filepath.Base(path), match[1], anchor)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read the frontend: %v", err)
	}
	if found == 0 {
		t.Fatal("no /docs links were found in the interface, so this test has stopped reading it")
	}
	t.Logf("checked %d documentation links", found)
}
