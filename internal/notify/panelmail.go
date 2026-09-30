package notify

import (
	"context"
	"log/slog"
	"strings"

	"skifity/internal/settings"
)

// SendPanelEmail sends one message to one address through the panel's own
// SMTP server, the one under Settings → Email. It is for the panel's own mail
// — a password reset — rather than a team's channels, which have their own
// recipients and may name servers of their own.
func SendPanelEmail(ctx context.Context, db SettingsReader, keyring Keyring, log *slog.Logger, to, subject, body string) error {
	config := map[string]string{"to": to}
	ctx = PrepareEmail(ctx, db, keyring, log, config)
	return sendEmail(ctx, config, Message{Title: subject, Body: body})
}

// PanelEmailConfigured says whether the panel has an SMTP server to send its
// own mail through.
func PanelEmailConfigured(ctx context.Context, db SettingsReader) bool {
	host, _, err := db.GetSetting(ctx, settings.KeySMTPHost)
	return err == nil && strings.TrimSpace(host) != ""
}
