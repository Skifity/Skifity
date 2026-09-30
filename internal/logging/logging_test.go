package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func capture(t *testing.T, fn func(*slog.Logger)) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	fn(New(&buf, "debug", "json"))

	var out map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &out); err != nil {
		t.Fatalf("the log line is not valid JSON: %v\n%s", err, buf.String())
	}
	return out
}

func TestSensitiveAttributesAreRedacted(t *testing.T) {
	entry := capture(t, func(log *slog.Logger) {
		log.Info("connecting",
			"host", "203.0.113.10",
			"password", "hunter2",
			"api_token", "skf_abc123",
			"private_key", "-----BEGIN OPENSSH PRIVATE KEY-----",
			"authorization", "Bearer abc",
			"user", "root")
	})

	for _, key := range []string{"password", "api_token", "private_key", "authorization"} {
		if entry[key] != Redacted {
			t.Errorf("%s was logged as %v", key, entry[key])
		}
	}
	// Ordinary fields must survive, or the logs become useless.
	if entry["host"] != "203.0.113.10" || entry["user"] != "root" {
		t.Fatalf("a non-sensitive field was redacted: %v", entry)
	}
}

func TestPublicSurvivesRedaction(t *testing.T) {
	// The setup token has a secret-shaped name and must still be readable: a
	// redacted one leaves an operator with a panel nobody can sign in to.
	entry := capture(t, func(log *slog.Logger) {
		log.Warn("first-run setup is pending", "setup_token", Public{Value: "abc123"})
	})
	if entry["setup_token"] != "abc123" {
		t.Fatalf("an explicitly public value was redacted: %v", entry["setup_token"])
	}
}

func TestSecretShapedValuesAreScrubbedFromMessages(t *testing.T) {
	entry := capture(t, func(log *slog.Logger) {
		log.Info("command failed: curl -H 'Authorization: Bearer abcdefghijklmnopqrstuvwxyz'")
	})
	message, _ := entry["msg"].(string)
	if strings.Contains(message, "abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("a bearer token survived in the message: %s", message)
	}

	entry = capture(t, func(log *slog.Logger) {
		log.Info("output", "detail",
			"-----BEGIN OPENSSH PRIVATE KEY-----\nabcdef\n-----END OPENSSH PRIVATE KEY-----")
	})
	if strings.Contains(entry["detail"].(string), "abcdef") {
		t.Fatalf("a private key survived: %v", entry["detail"])
	}
}

func TestGroupsAreRedactedFieldByField(t *testing.T) {
	entry := capture(t, func(log *slog.Logger) {
		log.Info("ssh", slog.Group("connection", "user", "root", "password", "hunter2"))
	})
	group, ok := entry["connection"].(map[string]any)
	if !ok {
		t.Fatalf("the group is missing: %v", entry)
	}
	if group["password"] != Redacted {
		t.Fatalf("a password inside a group was logged: %v", group)
	}
	// The rest of the group must survive, or the log loses the context that
	// made it worth writing.
	if group["user"] != "root" {
		t.Fatalf("an ordinary field inside a group was redacted: %v", group)
	}
}

func TestSensitiveGroupIsRedactedWholesale(t *testing.T) {
	// A group whose own name is sensitive is replaced entirely rather than
	// walked: if the group is called "credentials", nothing in it is worth the
	// risk of a key name the pattern does not catch.
	entry := capture(t, func(log *slog.Logger) {
		log.Info("ssh", slog.Group("credentials", "user", "root", "secret_value", "hunter2"))
	})
	if entry["credentials"] != Redacted {
		t.Fatalf("a group named credentials was not redacted: %v", entry["credentials"])
	}
}

func TestWithAttrsRedacts(t *testing.T) {
	// A logger that carries a secret in its base attributes must redact it on
	// every line, not only where it was added.
	var buf bytes.Buffer
	log := New(&buf, "debug", "json").With("token", "skf_secret")
	log.Info("first")
	log.Info("second")
	if strings.Contains(buf.String(), "skf_secret") {
		t.Fatalf("a base attribute leaked:\n%s", buf.String())
	}
}

func TestScrubIsUsableOnItsOwn(t *testing.T) {
	// Build output and command output pass through Scrub before reaching the UI.
	got := Scrub("export GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz0123456789")
	if strings.Contains(got, "ghp_abcdefghij") {
		t.Fatalf("a GitHub token survived: %s", got)
	}
}

// A variable nobody marked is stored as a secret when it looks like one.
//
// The case this exists for is a pasted .env file, which is where a person's
// API keys live. Stored as ordinary variables they came back from the API, sat
// on the page for every member of the team, and were handed by the MCP server
// to a language model.
func TestLooksSecretCatchesWhatAPastedEnvFileHolds(t *testing.T) {
	for _, tc := range []struct {
		key, value string
		secret     bool
	}{
		{"OPENAI_API_KEY", "sk-proj-abcdefghijklmnopqrstuvwxyz0123", true},
		{"STRIPE_SECRET_KEY", "sk_live_x", true},
		{"GITHUB_TOKEN", "ghp_abcdefghijklmnopqrstuvwxyz0123", true},
		{"SESSION_PASSWORD", "hunter2", true},
		{"JWT_PRIVATE_KEY", "-----BEGIN PRIVATE KEY-----\nMIIE\n-----END PRIVATE KEY-----", true},
		// The password is in the value and the key says nothing.
		{"DATABASE_URL", "postgres://shop:hunter2@db.internal:5432/shop", true},
		{"REDIS_URL", "redis://:hunter2@cache:6379", true},
		// A value that looks like a key, under an innocent name.
		{"UPSTREAM", "sk-abcdefghijklmnopqrstuvwxyz012345", true},

		// Ordinary configuration stays readable.
		{"NODE_ENV", "production", false},
		{"LOG_LEVEL", "debug", false},
		{"PUBLIC_URL", "https://shop.example.test", false},
		// A URL with a user and no password is not a credential.
		{"UPSTREAM_URL", "https://api@example.test/v1", false},
		{"DATABASE_URL", "postgres://db.internal:5432/shop", false},
	} {
		if got := LooksSecret(tc.key, tc.value); got != tc.secret {
			t.Errorf("LooksSecret(%q, %q) = %v, want %v", tc.key, tc.value, got, tc.secret)
		}
	}
}
