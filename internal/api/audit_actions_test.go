package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestEveryAuditActionHasWordsForIt
//
// An audit action is a code: "app.scaling_changed". The Activity page and the
// audit tab both show what happened to a person, so every code needs a phrase
// in the interface — and a code with no phrase falls back to itself, which is
// how the page used to read in full: a column of snake_case.
//
// The list is read out of this package's own source rather than kept by hand,
// because the one that gets forgotten is the one added last.
var auditCall = regexp.MustCompile(`audit\w*\([^)]*?"([a-z_]+\.[a-z_]+)"`)

func TestEveryAuditActionHasWordsForIt(t *testing.T) {
	phrases := auditPhrases(t)
	if len(phrases) < 50 {
		t.Fatalf("only %d phrases were read from the locale; this test is not reading it", len(phrases))
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}
	found := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		for _, match := range auditCall.FindAllStringSubmatch(string(body), -1) {
			found++
			if _, ok := phrases[match[1]]; !ok {
				t.Errorf("%s records the action %q and the interface has no words for it: "+
					"add activity.action.%s to web/src/locales/*.json",
					entry.Name(), match[1], match[1])
			}
		}
	}
	if found < 50 {
		t.Fatalf("only %d audit calls were found in this package; this test is not reading it", found)
	}
}

// auditPhrases reads activity.action out of the English locale.
func auditPhrases(t *testing.T) map[string]string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "locales", "en.json"))
	if err != nil {
		t.Fatalf("read the English locale: %v", err)
	}
	var locale struct {
		Activity struct {
			Action map[string]string `json:"action"`
		} `json:"activity"`
	}
	if err := json.Unmarshal(body, &locale); err != nil {
		t.Fatalf("read the English locale: %v", err)
	}
	return locale.Activity.Action
}
