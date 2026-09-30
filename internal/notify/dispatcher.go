package notify

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"skifity/internal/runsafe"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// Notifier is the part of a Dispatcher a producer needs.
//
// Producers take this interface rather than *Dispatcher so a test can watch
// what would have been sent without standing up a keyring and a database.
type Notifier interface {
	Notify(ctx context.Context, teamID, event string, msg Message)
}

// Keyring is the part of the crypto keyring a dispatcher needs.
type Keyring interface {
	Open(stored, context string) ([]byte, error)
}

// Store is the part of the database a dispatcher needs.
type Store interface {
	ListNotificationChannels(ctx context.Context, teamID string) ([]store.NotificationChannel, error)
	// GetSetting reads a panel-wide setting, and says whether it was sealed.
	// Used for the SMTP server, which belongs to the panel rather than to each
	// email channel.
	GetSetting(ctx context.Context, key string) (string, bool, error)
}

// Dispatcher sends an event to every channel in a team that asked for it.
//
// It exists because the panel let people subscribe to "a deployment failed" and
// then never told them: every producer knew the event had happened and nothing
// carried it anywhere. A channel that is configured and silent is worse than no
// channel, because it is trusted.
type Dispatcher struct {
	db      Store
	keyring Keyring
	log     *slog.Logger

	// panelURL is used to build links back into the panel. It is read at send
	// time rather than at construction, because an operator can set it after
	// the panel is already running.
	panelURL func(context.Context) string

	// provider is the installed plugins, for a channel kind this package does
	// not send itself. Nil on a panel with no plugins, which is the ordinary
	// case and not a failure.
	//
	// Set after construction rather than passed in, because the plugin
	// dispatcher is built from the cluster adapter and the cluster adapter is
	// built after this. A dispatcher with no provider still sends every
	// built-in kind.
	provider Provider

	// wg lets the panel wait for in-flight notifications during shutdown, so a
	// deployment failure reported at the moment of a restart still goes out.
	wg sync.WaitGroup
}

// SetProvider gives the dispatcher the plugins that provide channel kinds.
func (d *Dispatcher) SetProvider(provider Provider) {
	if d != nil {
		d.provider = provider
	}
}

// NewDispatcher returns a dispatcher. panelURL may be nil.
func NewDispatcher(db Store, keyring Keyring, log *slog.Logger, panelURL func(context.Context) string) *Dispatcher {
	if panelURL == nil {
		panelURL = func(context.Context) string { return "" }
	}
	return &Dispatcher{db: db, keyring: keyring, log: log, panelURL: panelURL}
}

// Notify delivers a message to the team's channels that subscribe to the event.
//
// It never blocks the caller and never returns an error: a deployment must not
// fail because Discord is down, and a caller that has just finished rolling out
// an app has nothing useful to do with a delivery error anyway. Failures are
// logged.
func (d *Dispatcher) Notify(ctx context.Context, teamID, event string, msg Message) {
	if d == nil || d.db == nil || teamID == "" {
		return
	}

	channels, err := d.db.ListNotificationChannels(ctx, teamID)
	if err != nil {
		d.log.Warn("could not read the notification channels", "team", teamID, "error", err)
		return
	}

	// Resolve the link before the request context is gone.
	if msg.URL == "" {
		if base := strings.TrimSuffix(d.panelURL(ctx), "/"); base != "" && msg.Path != "" {
			msg.URL = base + msg.Path
		}
	}

	for _, channel := range channels {
		if !channel.Enabled || !subscribes(channel.Events, event) {
			continue
		}
		config, err := d.configFor(teamID, channel)
		if err != nil {
			d.log.Error("could not read a notification channel's configuration",
				"channel", channel.ID, "kind", channel.Kind, "error", err)
			continue
		}
		if channel.Kind == "email" {
			d.fillSMTPFromSettings(ctx, config)
		}

		d.wg.Add(1)
		go func(kind, id string, config map[string]string) {
			defer d.wg.Done()
			// A notification is the least important thing the panel does and
			// used to be able to end it: a malformed channel configuration
			// reaching a template or a URL parser is a panic in a goroutine
			// nobody is waiting on.
			defer runsafe.Recover(d.log, "the "+kind+" notification", nil)
			// Detached from the caller's context on purpose: the request that
			// produced the event is usually over by now, and a notification
			// cancelled because a browser navigated away is a notification
			// nobody gets.
			sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			if err := Send(sendCtx, kind, config, msg, d.provider); err != nil {
				d.log.Warn("a notification could not be delivered",
					"channel", id, "kind", kind, "event", event, "error", err)
				return
			}
			d.log.Debug("notification delivered", "channel", id, "kind", kind, "event", event)
		}(channel.Kind, channel.ID, config)
	}
}

// Wait blocks until every notification already started has finished or timed
// out. The panel calls it during shutdown.
func (d *Dispatcher) Wait() {
	if d == nil {
		return
	}
	d.wg.Wait()
}

func (d *Dispatcher) configFor(teamID string, channel store.NotificationChannel) (map[string]string, error) {
	raw, err := d.keyring.Open(channel.ConfigEnc, "notification_channel:"+teamID+":"+channel.Name)
	if err != nil {
		return nil, err
	}
	var config map[string]string
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	return config, nil
}

// smtpSettings maps a panel-wide setting to the key an email channel reads.
//
// The settings page has had an Email section since the beginning — server,
// port, user, password, from, TLS — and nothing read any of it. Every email
// channel carried its own copy, so adding three recipients meant typing the
// same SMTP password three times, and the page that looked like it configured
// email configured nothing at all.
var smtpSettings = map[string]string{
	settings.KeySMTPHost:     "smtp_host",
	settings.KeySMTPPort:     "smtp_port",
	settings.KeySMTPUser:     "smtp_user",
	settings.KeySMTPPassword: "smtp_password",
	settings.KeySMTPFrom:     "from",
	settings.KeySMTPTLS:      "smtp_tls",
}

// fillSMTPFromSettings fills in what the channel did not say for itself.
//
// The channel wins where it has an answer: somebody who put a different server
// on one channel meant it. Everything else falls back to the panel's own
// settings, so an email channel is a list of recipients and nothing more.
func (d *Dispatcher) fillSMTPFromSettings(ctx context.Context, config map[string]string) {
	for key, field := range smtpSettings {
		if strings.TrimSpace(config[field]) != "" {
			continue
		}
		value, encrypted, err := d.db.GetSetting(ctx, key)
		if err != nil {
			d.log.Warn("could not read an SMTP setting", "setting", key, "error", err)
			continue
		}
		if value == "" {
			continue
		}
		if encrypted {
			plaintext, err := d.keyring.Open(value, settings.Context(key))
			if err != nil {
				d.log.Warn("could not read the SMTP password", "error", err)
				continue
			}
			value = string(plaintext)
		}
		config[field] = value
	}
}

// subscribes reports whether a channel's stored event list covers an event.
//
// An empty list means every event: a channel created without choosing anything
// is one somebody wanted to hear from, and silence would be the wrong reading.
func subscribes(events, event string) bool {
	events = strings.TrimSpace(events)
	if events == "" {
		return true
	}
	for _, candidate := range strings.Split(events, ",") {
		if strings.TrimSpace(candidate) == event {
			return true
		}
	}
	return false
}
