package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestEverySettingHasItsWordsInTheInterface
//
// The server's label and help are English: the server has one language, and the
// API, the CLI and an assistant all read them. The panel has five, and looks
// them up by the setting's own key — so a setting added here without a
// translation is a paragraph of English in the middle of an Indonesian page,
// which is exactly what this page used to be from top to bottom.
//
// The English in the locale has to be the English here, character for
// character: two copies of the same sentence drift, and the one that drifts is
// the one nobody reads.
func TestEverySettingHasItsWordsInTheInterface(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "locales", "en.json"))
	if err != nil {
		t.Fatalf("read the English locale: %v", err)
	}
	var locale struct {
		Settings struct {
			Field map[string]struct {
				Label string `json:"label"`
				Help  string `json:"help"`
			} `json:"field"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(body, &locale); err != nil {
		t.Fatalf("read the English locale: %v", err)
	}

	for _, def := range Definitions {
		key := ""
		for _, ch := range def.Key {
			if ch == '.' {
				key += "_"
				continue
			}
			key += string(ch)
		}
		field, ok := locale.Settings.Field[key]
		if !ok {
			t.Errorf("the setting %q has no words in the interface: "+
				"add settings.field.%s to web/src/locales/*.json", def.Key, key)
			continue
		}
		if field.Label != def.Label {
			t.Errorf("the setting %q is labelled %q here and %q in the interface",
				def.Key, def.Label, field.Label)
		}
		if field.Help != def.Help {
			t.Errorf("the help for %q differs between this file and the interface:\n  here: %s\n   ui: %s",
				def.Key, def.Help, field.Help)
		}
	}
}
