package notify

import (
	"maps"
	"slices"
	"testing"
)

// Editing a channel keeps its secrets unless somebody types a new one, and
// never sends one back to the form.

func TestAnEditKeepsASecretLeftEmpty(t *testing.T) {
	fields, known := FormOf("telegram", nil)
	stored := map[string]string{"bot_token": "123:fake-token", "chat_id": "-100"}

	// The form sends every field; the password box nobody touched is empty.
	merged := MergeConfig(fields, known, stored, map[string]string{"bot_token": "", "chat_id": "-200"})
	if merged["bot_token"] != "123:fake-token" {
		t.Errorf("a secret left empty was lost: %v", merged)
	}
	if merged["chat_id"] != "-200" {
		t.Errorf("a changed setting was not changed: %v", merged)
	}

	// A new value replaces the secret.
	merged = MergeConfig(fields, known, stored, map[string]string{"bot_token": "456:other-fake"})
	if merged["bot_token"] != "456:other-fake" || merged["chat_id"] != "-100" {
		t.Errorf("replacing the token: %v", merged)
	}

	if stored["bot_token"] != "123:fake-token" || stored["chat_id"] != "-100" {
		t.Errorf("the stored settings were changed in place: %v", stored)
	}
}

func TestAnEditClearsASettingThatIsNotSecret(t *testing.T) {
	fields, known := FormOf("ntfy", nil)
	stored := map[string]string{"topic": "deploys", "server": "https://ntfy.example.test", "token": "tk_fake"}
	merged := MergeConfig(fields, known, stored, map[string]string{"topic": "deploys", "server": "", "token": ""})
	if _, ok := merged["server"]; ok {
		t.Errorf("an emptied server was kept, so ntfy.sh can never be gone back to: %v", merged)
	}
	if merged["token"] != "tk_fake" {
		t.Errorf("the token left empty was lost: %v", merged)
	}
}

// What the form does not know about is kept, and counted as a secret: an
// email channel given its own server through the API has settings the form
// never shows, and a plugin that cannot be asked has a form nobody can read.
func TestWhatTheFormDoesNotKnowIsKeptAndHidden(t *testing.T) {
	fields, known := FormOf("email", nil)
	stored := map[string]string{"to": "ops@example.test", "smtp_host": "smtp.example.test", "smtp_password": "fake-password"}
	merged := MergeConfig(fields, known, stored, map[string]string{"to": "dev@example.test", "smtp_password": ""})
	if merged["smtp_host"] != "smtp.example.test" || merged["smtp_password"] != "fake-password" ||
		merged["to"] != "dev@example.test" {
		t.Errorf("settings the form does not show were lost: %v", merged)
	}
	values, secrets := Revealable(fields, known, stored)
	if !maps.Equal(values, map[string]string{"to": "ops@example.test"}) || len(secrets) != 0 {
		t.Errorf("the form is shown %v and told of %v", values, secrets)
	}

	gone := "plugin:com.example.chat/rooms"
	fields, known = FormOf(gone, nil)
	if known {
		t.Fatal("a kind no plugin provides has a form")
	}
	stored = map[string]string{"room": "ops", "api_key": "fake-key"}
	merged = MergeConfig(fields, known, stored, map[string]string{"room": "", "api_key": ""})
	if !maps.Equal(merged, stored) {
		t.Errorf("a form nobody can read emptied settings: %v", merged)
	}
	if values, secrets := Revealable(fields, known, stored); len(values) != 0 || len(secrets) != 0 {
		t.Errorf("a form nobody can read was shown %v and %v", values, secrets)
	}
}

// The form is told a secret is stored and never what it is — for every
// built-in kind, and for a plugin's field marked secret or drawn as a
// password.
func TestASecretIsNeverRevealed(t *testing.T) {
	for kind, fields := range builtInForms {
		stored := map[string]string{}
		for _, field := range fields {
			stored[field.Key] = "value-of-" + field.Key
		}
		values, secrets := Revealable(fields, true, stored)
		for _, field := range fields {
			shown, revealed := values[field.Key]
			switch {
			case field.Secret && revealed:
				t.Errorf("%s: the secret %s was sent to the form as %q", kind, field.Key, shown)
			case field.Secret && !slices.Contains(secrets, field.Key):
				t.Errorf("%s: the form is not told %s is stored", kind, field.Key)
			case !field.Secret && shown != stored[field.Key]:
				t.Errorf("%s: %s is not shown to the form that changes it", kind, field.Key)
			}
		}
	}

	provided := []ChannelKind{{Kind: "plugin:com.example.chat/rooms", Fields: []Field{
		{Key: "room"}, {Key: "api_key", Secret: true}, {Key: "password", Kind: "password"},
	}}}
	fields, known := FormOf("plugin:com.example.chat/rooms", provided)
	values, secrets := Revealable(fields, known, map[string]string{"room": "ops", "api_key": "k", "password": "p"})
	if _, revealed := values["api_key"]; revealed {
		t.Errorf("a plugin's secret field was revealed: %v", values)
	}
	if _, revealed := values["password"]; revealed {
		t.Errorf("a plugin's password field was revealed: %v", values)
	}
	if values["room"] != "ops" || !slices.Equal(secrets, []string{"api_key", "password"}) {
		t.Errorf("a plugin's form is shown %v and told of %v", values, secrets)
	}
}
