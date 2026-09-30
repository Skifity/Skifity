// Package notify delivers events to email, Telegram, Discord and webhooks.
package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"skifity/internal/netguard"
	"skifity/internal/version"
)

// Message is one notification.
type Message struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	// Level is info, success, warning or error, and drives the colour.
	Level string `json:"level"`
	// URL links back into the panel.
	URL string `json:"url,omitempty"`
	// Path is where in the panel this happened, such as "/apps/app_123". The
	// dispatcher turns it into a URL using the configured panel address, so a
	// producer does not need to know what that is.
	Path string `json:"-"`
	// Fields carry structured detail such as the app and the commit.
	Fields map[string]string `json:"fields,omitempty"`
}

// Event names channels can subscribe to.
const (
	EventDeploySucceeded = "deploy.succeeded"
	EventDeployFailed    = "deploy.failed"
	EventAppUnhealthy    = "app.unhealthy"
	EventServerAdded     = "server.added"
	EventServerLost      = "server.lost"
	EventBackupFailed    = "backup.failed"
	EventCertificate     = "certificate.failed"
)

// AllEvents is what the UI offers when configuring a channel.
var AllEvents = []string{
	EventDeploySucceeded, EventDeployFailed, EventAppUnhealthy,
	EventServerAdded, EventServerLost, EventBackupFailed, EventCertificate,
}

// client is shared so notifications reuse connections and always time out.
//
// Guarded, because a webhook address is a setting: a team administrator could
// otherwise point it at the cloud metadata service and read the answer back out
// of the error this returns. See internal/netguard.
var client = netguard.Client(15 * time.Second)

// ValidateConfig checks a channel's configuration before it is stored, so a typo
// is caught while the person is still looking at the form.
//
// provider may be nil, which is a panel with no plugins: a kind this package
// does not know is then simply not a kind.
func ValidateConfig(ctx context.Context, kind string, config map[string]string, provider Provider) error {
	if IsProvided(kind) {
		if provider == nil {
			return unknownKind(kind)
		}
		return provider.Validate(ctx, kind, config)
	}
	switch kind {
	case "telegram":
		if config["bot_token"] == "" {
			return errors.New("a Telegram bot token is needed; create a bot with @BotFather")
		}
		if config["chat_id"] == "" {
			return errors.New("a chat id is needed; message your bot, then read the chat id from getUpdates")
		}
	case "discord":
		if !strings.HasPrefix(config["webhook_url"], "https://discord.com/api/webhooks/") &&
			!strings.HasPrefix(config["webhook_url"], "https://discordapp.com/api/webhooks/") {
			return errors.New("that does not look like a Discord webhook URL")
		}
	case "webhook":
		url := config["url"]
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			return errors.New("enter a full URL, starting with https://")
		}
	case "email":
		if config["to"] == "" {
			return errors.New("enter at least one address to send to")
		}
	default:
		return unknownKind(kind)
	}
	return nil
}

// Send delivers a message through one channel.
//
// provider may be nil, as in ValidateConfig.
func Send(ctx context.Context, kind string, config map[string]string, msg Message, provider Provider) error {
	if IsProvided(kind) {
		if provider == nil {
			return unknownKind(kind)
		}
		return provider.Send(ctx, kind, config, msg)
	}
	switch kind {
	case "telegram":
		return sendTelegram(ctx, config, msg)
	case "discord":
		return sendDiscord(ctx, config, msg)
	case "webhook":
		return sendWebhook(ctx, config, msg)
	case "email":
		return sendEmail(ctx, config, msg)
	default:
		return unknownKind(kind)
	}
}

func sendTelegram(ctx context.Context, config map[string]string, msg Message) error {
	payload := map[string]any{
		"chat_id":    config["chat_id"],
		"text":       renderMarkdown(msg),
		"parse_mode": "Markdown",
		// Deploy notifications carry a URL; a preview card for it is noise.
		"disable_web_page_preview": true,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode telegram message: %w", err)
	}
	url := "https://api.telegram.org/bot" + config["bot_token"] + "/sendMessage"
	return postJSON(ctx, url, body, nil)
}

func sendDiscord(ctx context.Context, config map[string]string, msg Message) error {
	embed := map[string]any{
		"title":       msg.Title,
		"description": msg.Body,
		"color":       discordColour(msg.Level),
		"footer":      map[string]string{"text": version.Name},
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
	}
	if msg.URL != "" {
		embed["url"] = msg.URL
	}
	if len(msg.Fields) > 0 {
		fields := make([]map[string]any, 0, len(msg.Fields))
		for k, v := range msg.Fields {
			fields = append(fields, map[string]any{"name": k, "value": v, "inline": true})
		}
		embed["fields"] = fields
	}
	body, err := json.Marshal(map[string]any{"embeds": []any{embed}})
	if err != nil {
		return fmt.Errorf("encode discord message: %w", err)
	}
	return postJSON(ctx, config["webhook_url"], body, nil)
}

func sendWebhook(ctx context.Context, config map[string]string, msg Message) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("encode webhook message: %w", err)
	}
	headers := map[string]string{}
	if secret := config["secret"]; secret != "" {
		// A shared secret in a header is enough for a webhook the operator
		// configured themselves; the receiver compares it.
		headers["X-Skifity-Secret"] = secret
	}
	return postJSON(ctx, config["url"], body, headers)
}

func sendEmail(ctx context.Context, config map[string]string, msg Message) error {
	host := config["smtp_host"]
	if host == "" {
		return errors.New("email notifications need SMTP settings; fill them in under Settings, then Email")
	}
	port, err := strconv.Atoi(defaultStr(config["smtp_port"], "587"))
	if err != nil {
		return fmt.Errorf("SMTP port %q is not a number", config["smtp_port"])
	}
	from := defaultStr(config["from"], "skifity@"+host)
	recipients := splitAddresses(config["to"])
	if len(recipients) == 0 {
		return errors.New("no recipient addresses are configured")
	}

	var body bytes.Buffer
	fmt.Fprintf(&body, "From: %s\r\n", from)
	fmt.Fprintf(&body, "To: %s\r\n", strings.Join(recipients, ", "))
	fmt.Fprintf(&body, "Subject: [%s] %s\r\n", version.Name, msg.Title)
	fmt.Fprintf(&body, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	body.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n")
	body.WriteString(msg.Body)
	if msg.URL != "" {
		fmt.Fprintf(&body, "\r\n\r\n%s\r\n", msg.URL)
	}

	address := net.JoinHostPort(host, strconv.Itoa(port))
	var auth smtp.Auth
	if user := config["smtp_user"]; user != "" {
		auth = smtp.PlainAuth("", user, config["smtp_password"], host)
	}

	// Port 465 is implicit TLS; everything else starts plain and upgrades.
	if port == 465 {
		dialer := &tls.Dialer{Config: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}}
		conn, err := dialer.DialContext(ctx, "tcp", address)
		if err != nil {
			return fmt.Errorf("connect to %s over TLS: %w", address, err)
		}
		defer conn.Close()
		c, err := smtp.NewClient(conn, host)
		if err != nil {
			return fmt.Errorf("start SMTP session: %w", err)
		}
		defer c.Quit()
		return deliver(c, auth, from, recipients, body.Bytes())
	}

	c, err := smtp.Dial(address)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", address, err)
	}
	defer c.Quit()
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("start TLS with %s: %w", host, err)
		}
	}
	return deliver(c, auth, from, recipients, body.Bytes())
}

func deliver(c *smtp.Client, auth smtp.Auth, from string, to []string, body []byte) error {
	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("SMTP authentication failed: %w", err)
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("SMTP rejected the sender address: %w", err)
	}
	for _, addr := range to {
		if err := c.Rcpt(addr); err != nil {
			return fmt.Errorf("SMTP rejected the recipient %s: %w", addr, err)
		}
	}
	wc, err := c.Data()
	if err != nil {
		return fmt.Errorf("SMTP refused the message body: %w", err)
	}
	if _, err := wc.Write(body); err != nil {
		return fmt.Errorf("write message body: %w", err)
	}
	return wc.Close()
}

func postJSON(ctx context.Context, url string, body []byte, headers map[string]string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send notification: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		// The body usually says exactly what is wrong; include a little of it.
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("the service answered %s: %s", resp.Status, strings.TrimSpace(string(snippet)))
	}
	return nil
}

func renderMarkdown(msg Message) string {
	var b strings.Builder
	fmt.Fprintf(&b, "*%s*\n%s", msg.Title, msg.Body)
	for k, v := range msg.Fields {
		fmt.Fprintf(&b, "\n%s: `%s`", k, v)
	}
	if msg.URL != "" {
		fmt.Fprintf(&b, "\n%s", msg.URL)
	}
	return b.String()
}

func discordColour(level string) int {
	switch level {
	case "success":
		return 0x2ecc71
	case "warning":
		return 0xf1c40f
	case "error":
		return 0xe74c3c
	default:
		return 0x3498db
	}
}

func splitAddresses(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == ' ' })
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func defaultStr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
