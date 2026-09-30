package notify

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"testing"
	"time"

	"skifity/internal/netguard"
	"skifity/internal/runsafe"
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
	PrepareEmail(t.Context(), db, fakeKeyring{}, slog.New(slog.DiscardHandler), config)

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

// A channel that names its own server meant it — and gets nothing of the
// panel's: its user and password belong to the panel's server. Filling them
// in handed the panel's SMTP password to whatever server a team admin named.
func TestAChannelsOwnServerIsNotGivenThePanelsCredentials(t *testing.T) {
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
	ctx := PrepareEmail(t.Context(), db, fakeKeyring{}, slog.New(slog.DiscardHandler), config)

	if config["smtp_host"] != "other.example.test" || config["smtp_user"] != "alerts" {
		t.Errorf("the channel's own settings were overwritten: %v", config)
	}
	if config["smtp_password"] != "" {
		t.Fatalf("the panel's SMTP password was handed to a server a channel named: %q", config["smtp_password"])
	}
	if ctx.Value(panelServer{}) != nil {
		t.Fatal("a channel's own server is dialled as if the administrator had chosen it")
	}

	// The panel's own server named again, in any case, is the panel's.
	config = map[string]string{"to": "ops@example.test", "smtp_host": "SMTP.example.test"}
	PrepareEmail(t.Context(), db, fakeKeyring{}, slog.New(slog.DiscardHandler), config)
	if config["smtp_password"] != "a-password" {
		t.Fatalf("the panel's own server was not given its password: %v", config)
	}
}

func TestAServerAChannelNamesIsDialledCarefully(t *testing.T) {
	// The panel's own machine is not somewhere a team's email goes.
	err := sendEmail(t.Context(), map[string]string{"to": "a@example.test", "smtp_host": "127.0.0.1", "smtp_port": "2525"}, Message{Title: "t"})
	var blocked *netguard.Blocked
	if !errors.As(err, &blocked) {
		t.Fatalf("a channel's server on loopback: %v", err)
	}

	// A server that accepts and never speaks does not hold the sender past
	// its deadline.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		defer runsafe.Recover(nil, "the silent SMTP server", nil)
		conn, err := listener.Accept()
		if err == nil {
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	ctx, cancel := context.WithTimeout(context.WithValue(t.Context(), panelServer{}, true), 300*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = sendEmail(ctx, map[string]string{"to": "a@example.test", "smtp_host": host, "smtp_port": port}, Message{Title: "t"})
	if err == nil || time.Since(started) > 5*time.Second {
		t.Fatalf("a silent server: %v after %s", err, time.Since(started))
	}
}
