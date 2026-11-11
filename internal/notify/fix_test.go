package notify

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"

	"skifity/internal/runsafe"
)

// A notification about something broken says what to do about it, on every
// channel, apart from the body: "the build ran out of memory" is the cause,
// and the line after it is what somebody on a phone actually needs.

var broken = Message{
	Title: "Deploying shop failed", Body: "The build ran out of memory.", Level: "error",
	URL:    "https://panel.example/apps/app_1?tab=deployments&deployment=dep_2",
	Fix:    "Give the build more memory under Settings, then deploy again.",
	Fields: map[string]string{"App": "shop", "Commit": "abc1234"},
}

func TestEveryChannelSaysWhatToDo(t *testing.T) {
	previous := pushoverURL
	t.Cleanup(func() { pushoverURL = previous })

	for _, tc := range []struct {
		kind   string
		config func(server string) map[string]string
		// check reads what the service was sent, and says what is missing.
		check func(body []byte) string
	}{
		{"slack", webhookAt("/hooks/x"), func(body []byte) string {
			var payload struct {
				Attachments []struct {
					Fields []struct {
						Title, Value string
						Short        bool
					}
				}
			}
			_ = json.Unmarshal(body, &payload)
			if len(payload.Attachments) != 1 || len(payload.Attachments[0].Fields) != 3 {
				return "three fields"
			}
			first := payload.Attachments[0].Fields[0]
			if first.Title != fixLabel || first.Value != broken.Fix || first.Short {
				return "the fix as the first field, full width"
			}
			return ""
		}},
		{"mattermost", webhookAt("/hooks/x"), contains(broken.Fix)},
		{"discord", webhookAt("/api/webhooks/1/x"), func(body []byte) string {
			var payload struct {
				Embeds []struct {
					Fields []struct {
						Name, Value string
						Inline      bool
					}
				}
			}
			_ = json.Unmarshal(body, &payload)
			if len(payload.Embeds) != 1 || len(payload.Embeds[0].Fields) == 0 {
				return "fields"
			}
			first := payload.Embeds[0].Fields[0]
			if first.Name != fixLabel || first.Value != broken.Fix || first.Inline {
				return "the fix as the first field, full width"
			}
			return ""
		}},
		{"teams", webhookAt("/workflows/x"), func(body []byte) string {
			var payload struct {
				Attachments []struct {
					Content struct {
						Body []struct {
							Type  string
							Facts []struct{ Title, Value string }
						}
					}
				}
			}
			_ = json.Unmarshal(body, &payload)
			for _, block := range payload.Attachments[0].Content.Body {
				if block.Type == "FactSet" && len(block.Facts) == 3 &&
					block.Facts[0].Title == fixLabel && block.Facts[0].Value == broken.Fix {
					return ""
				}
			}
			return "the fix as the first fact"
		}},
		{"webhook", func(server string) map[string]string { return map[string]string{"url": server} },
			func(body []byte) string {
				var payload map[string]any
				_ = json.Unmarshal(body, &payload)
				if payload["fix"] != broken.Fix {
					return `a "fix" key`
				}
				return ""
			}},
		{"ntfy", func(server string) map[string]string { return map[string]string{"topic": "ops", "server": server} },
			func(body []byte) string {
				var payload struct{ Message string }
				_ = json.Unmarshal(body, &payload)
				if !strings.Contains(payload.Message, fixLabel+": "+broken.Fix) {
					return "the fix in the message"
				}
				return ""
			}},
		{"gotify", func(server string) map[string]string {
			return map[string]string{"server": server, "app_token": "AbCdEfGhIjKlMnO"}
		}, contains(fixLabel + ": " + broken.Fix)},
		{"pushover", func(server string) map[string]string {
			pushoverURL = server + "/1/messages.json"
			return map[string]string{"app_token": strings.Repeat("a", 30), "user_key": strings.Repeat("u", 30)}
		}, func(body []byte) string {
			form, _ := url.ParseQuery(string(body))
			if !strings.Contains(form.Get("message"), fixLabel+": "+broken.Fix) {
				return "the fix in the message"
			}
			return ""
		}},
	} {
		server, got := service(t)
		if err := Send(t.Context(), tc.kind, tc.config(server.URL), broken, nil); err != nil {
			t.Fatalf("%s: %v", tc.kind, err)
		}
		if missing := tc.check((*got)[0].body); missing != "" {
			t.Errorf("%s was sent no %s: %s", tc.kind, missing, (*got)[0].body)
		}
	}
}

func webhookAt(path string) func(string) map[string]string {
	return func(server string) map[string]string { return map[string]string{"webhook_url": server + path} }
}

func contains(text string) func([]byte) string {
	return func(body []byte) string {
		if !strings.Contains(string(body), text) {
			return fmt.Sprintf("%q", text)
		}
		return ""
	}
}

func TestAnEmailSaysWhatToDo(t *testing.T) {
	host, port, received := smtpReceiver(t)
	ctx, cancel := context.WithTimeout(context.WithValue(t.Context(), panelServer{}, true), 10*time.Second)
	defer cancel()
	config := map[string]string{"to": "ops@example.test", "smtp_host": host, "smtp_port": port}
	if err := sendEmail(ctx, config, broken); err != nil {
		t.Fatal(err)
	}
	var mail string
	select {
	case mail = <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("the receiver was sent no message")
	}
	_, text, _ := strings.Cut(mail, "\r\n\r\n")
	for _, want := range []string{
		broken.Body + "\r\n\r\n" + fixLabel + ": " + broken.Fix,
		"Commit: abc1234",
		broken.URL,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the email is missing %q:\n%s", want, text)
		}
	}
}

// smtpReceiver is just enough of a mail server to take one message: it offers
// no TLS and asks for no password, and hands over what it was given.
func smtpReceiver(t *testing.T) (host, port string, received <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	messages := make(chan string, 1)
	go func() {
		defer runsafe.Recover(nil, "the test SMTP server", nil)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reply := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
		reader := bufio.NewReader(conn)
		reply("220 receiver.example.test ESMTP")
		var data strings.Builder
		reading := false
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if reading {
				if line == ".\r\n" {
					reading = false
					messages <- data.String()
					reply("250 queued")
				} else {
					data.WriteString(line)
				}
				continue
			}
			switch command := strings.ToUpper(strings.TrimSpace(line)); {
			case strings.HasPrefix(command, "DATA"):
				reading = true
				reply("354 go ahead")
			case strings.HasPrefix(command, "QUIT"):
				reply("221 bye")
				return
			default:
				reply("250 ok")
			}
		}
	}()
	host, port, _ = net.SplitHostPort(listener.Addr().String())
	return host, port, messages
}

// Telegram is sent its legacy Markdown, where an underscore starts italics.
// Every id has one, so a link to a deployment — app_… and dep_… — had the text
// between them taken for italics and was cut in half; a body quoting an error
// with an asterisk or a bracket was refused, and the message never arrived.
func TestATelegramMessageKeepsItsLinkWhole(t *testing.T) {
	msg := Message{
		Title: "Deploying my_shop failed",
		Body:  "pull access denied for registry.example/my_team/shop_web: see [docs] *now*",
		Fix:   "Set the registry's credentials under Settings_Registry.",
		URL:   "https://panel.example/apps/app_01HZX?tab=deployments&deployment=dep_01HZY",
		Fields: map[string]string{
			"App": "my_shop", "Reason": "image.pull_denied", "Odd`field": "back`quote",
		},
	}
	text, entities, err := parseTelegramMarkdown(renderMarkdown(msg))
	if err != nil {
		t.Fatalf("Telegram would refuse this: %v\n%s", err, renderMarkdown(msg))
	}

	at := strings.Index(text, msg.URL)
	if at < 0 {
		t.Fatalf("the link is not whole in what Telegram shows:\n%s", text)
	}
	for _, entity := range entities {
		if entity.offset < at+len(msg.URL) && at < entity.offset+entity.length {
			t.Errorf("the link is inside a %s entity, so it is not one address any more:\n%s", entity.kind, text)
		}
	}
	for _, whole := range []string{msg.Body, msg.Fix, "image.pull_denied"} {
		if !strings.Contains(text, whole) {
			t.Errorf("%q did not come through as written:\n%s", whole, text)
		}
	}
	if len(entities) == 0 || entities[0].kind != "bold" || text[:entities[0].length] != msg.Title {
		t.Errorf("the title is not the bold first line: %+v\n%s", entities, text)
	}
}

type telegramEntity struct {
	kind           string
	offset, length int
}

// parseTelegramMarkdown reads text the way Telegram reads parse_mode
// "Markdown": a backslash before one of the four markers makes it literal,
// and anything else between a marker and the next of the same is an entity,
// read as it is. A marker never closed is the error Telegram answers with.
func parseTelegramMarkdown(source string) (string, []telegramEntity, error) {
	var text []byte
	var entities []telegramEntity
	for i := 0; i < len(source); i++ {
		c := source[i]
		if c == '\\' && i+1 < len(source) && strings.IndexByte("_*`[", source[i+1]) >= 0 {
			i++
			text = append(text, source[i])
			continue
		}
		if strings.IndexByte("_*`[", c) < 0 {
			text = append(text, c)
			continue
		}
		end := c
		if c == '[' {
			end = ']'
		}
		start := len(text)
		for i++; i < len(source) && source[i] != end; i++ {
			text = append(text, source[i])
		}
		if i == len(source) {
			return "", nil, fmt.Errorf("can't find end of the entity starting at byte offset %d", start)
		}
		if c == '[' && i+1 < len(source) && source[i+1] == '(' {
			for i += 2; i < len(source) && source[i] != ')'; i++ {
			}
		}
		if len(text) > start {
			kind := map[byte]string{'_': "italic", '*': "bold", '`': "code", '[': "link"}[c]
			entities = append(entities, telegramEntity{kind, start, len(text) - start})
		}
	}
	return string(text), entities, nil
}
