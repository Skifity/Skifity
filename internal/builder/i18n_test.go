package builder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every note detection writes has words in all five languages.
//
// The notes were English sentences the panel showed as they came, so the new-
// app form said "Next.js listens on port 3000 by default" to somebody reading
// the rest of it in Russian. Each is now a code the panel translates, with the
// English kept for the CLI. The codes are read out of the source, so a note
// cannot be added without its words.
func TestEveryDetectionNoteHasItsWords(t *testing.T) {
	codes := map[string][]string{}
	call := regexp.MustCompile(`d\.note\("([a-z_]+)",[\s\S]*?(?:\n\s*\}|\)\n)`)
	param := regexp.MustCompile(`, "([a-z]+)", `)
	for _, file := range []string{"detect.go", "heroku.go"} {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range call.FindAllStringSubmatch(string(source), -1) {
			var params []string
			for _, p := range param.FindAllStringSubmatch(match[0], -1) {
				params = append(params, p[1])
			}
			codes[match[1]] = params
		}
	}
	if len(codes) < 25 {
		t.Fatalf("only %d note codes were found, so this test has stopped reading the source", len(codes))
	}

	// And nothing writes a note that has no code.
	for _, file := range []string{"detect.go", "heroku.go"} {
		source, _ := os.ReadFile(file)
		if n := strings.Count(string(source), "d.Notes = append"); n != 0 && !(file == "detect.go" && n == 1) {
			t.Errorf("%s appends %d notes without a code", file, n)
		}
	}

	locales, err := filepath.Glob(filepath.Join("..", "..", "web", "src", "locales", "*.json"))
	if err != nil || len(locales) < 5 {
		t.Fatalf("find the locales: %v", err)
	}
	names := make([]string, 0, len(codes))
	for code := range codes {
		names = append(names, code)
	}
	sort.Strings(names)
	for _, path := range locales {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var locale struct {
			Detect struct {
				Note map[string]string `json:"note"`
			} `json:"detect"`
		}
		if err := json.Unmarshal(body, &locale); err != nil {
			t.Fatal(err)
		}
		language := strings.TrimSuffix(filepath.Base(path), ".json")
		for _, code := range names {
			words, ok := locale.Detect.Note[code]
			if !ok {
				t.Errorf("%s: the note %q has no words: add detect.note.%s", language, code, code)
				continue
			}
			for _, p := range codes[code] {
				if !strings.Contains(words, "{{"+p+"}}") {
					t.Errorf("%s: detect.note.%s does not say {{%s}}", language, code, p)
				}
			}
		}
	}
}

func TestANoteIsWrittenInBothForms(t *testing.T) {
	d := Detect(Tree{Files: []string{"go.mod"}})
	if len(d.Notes) == 0 || len(d.Notes) != len(d.NoteCodes) {
		t.Fatalf("%d notes and %d codes", len(d.Notes), len(d.NoteCodes))
	}
}
