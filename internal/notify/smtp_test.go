package notify

import (
	"context"
	"log/slog"
	"testing"

	"skifity/internal/settings"
	"skifity/internal/store"
)

// The panel's Email settings had never been read by anything.
//
// Server, port, user, password, from and TLS have been on the settings page
// since the beginning, sealed and stored, and every email channel carried its
// own copy instead. So the page that looked like it configured email
// configured nothing, and adding three recipients meant typing the same SMTP
// password three times.

func TestAnEmailChannelFallsBackToThePanelsSMTPSettings(t *testing.T) {
	db := fakeStore{
		channels: []store.NotificationChannel{{
			ID: "ch_1", Name: "ops", Kind: "email", Enabled: true,
			// Only the recipients, which is all a channel should have to say.
			ConfigEnc: `{"to":"ops@example.test"}`,
		}},
		settings: map[string]string{
			settings.KeySMTPHost:     "smtp.example.test",
			settings.KeySMTPPort:     "2525",
			settings.KeySMTPUser:     "panel",
			settings.KeySMTPPassword: "a-password",
			settings.KeySMTPFrom:     "skifity@example.test",
		},
	}
	dispatcher := NewDispatcher(db, fakeKeyring{}, slog.New(slog.DiscardHandler), func(context.Context) string { return "" })

	config, err := dispatcher.configFor("team_1", db.channels[0])
	if err != nil {
		t.Fatalf("read the channel: %v", err)
	}
	dispatcher.fillSMTPFromSettings(t.Context(), config)

	for field, want := range map[string]string{
		"smtp_host":     "smtp.example.test",
		"smtp_port":     "2525",
		"smtp_user":     "panel",
		"smtp_password": "a-password",
		"from":          "skifity@example.test",
	} {
		if config[field] != want {
			t.Errorf("%s is %q, want %q: the panel's Email settings are not being read",
				field, config[field], want)
		}
	}
	if config["to"] != "ops@example.test" {
		t.Errorf("the channel's own recipients were lost: %q", config["to"])
	}
}

// A channel that names its own server meant it.
func TestAChannelsOwnSMTPSettingsWin(t *testing.T) {
	db := fakeStore{
		channels: []store.NotificationChannel{{
			ID: "ch_1", Name: "alerts", Kind: "email", Enabled: true,
			ConfigEnc: `{"to":"alerts@example.test","smtp_host":"other.example.test","smtp_user":"alerts"}`,
		}},
		settings: map[string]string{
			settings.KeySMTPHost:     "smtp.example.test",
			settings.KeySMTPUser:     "panel",
			settings.KeySMTPPassword: "a-password",
		},
	}
	dispatcher := NewDispatcher(db, fakeKeyring{}, slog.New(slog.DiscardHandler), func(context.Context) string { return "" })

	config, err := dispatcher.configFor("team_1", db.channels[0])
	if err != nil {
		t.Fatalf("read the channel: %v", err)
	}
	dispatcher.fillSMTPFromSettings(t.Context(), config)

	if config["smtp_host"] != "other.example.test" {
		t.Errorf("the channel's own server was overwritten: %q", config["smtp_host"])
	}
	if config["smtp_user"] != "alerts" {
		t.Errorf("the channel's own user was overwritten: %q", config["smtp_user"])
	}
	// And what it did not say still comes from the panel.
	if config["smtp_password"] != "a-password" {
		t.Errorf("the panel's password was not filled in: %q", config["smtp_password"])
	}
}
