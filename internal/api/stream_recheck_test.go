package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"skifity/internal/auth"
	"skifity/internal/store"
)

// The interface's stream stays open for as long as the browser tab does, so
// the decision made when it opened is asked again while it is open. Nothing
// did: a tab signed out somewhere else, or whose reader was limited to some
// projects, went on being sent the whole team's events.

// sseFrame is one message on an event stream, as it was written.
type sseFrame struct {
	id, event, data string
}

// openStream asks for /api/events?query, signed in by sign, and returns its
// frames as they arrive. The channel is closed when the stream ends.
func (h *harness) openStream(t *testing.T, query string, sign func(*http.Request)) <-chan sseFrame {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.server.URL+"/api/events?"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	sign(req)
	frames := make(chan sseFrame, 16)
	answered := make(chan int, 1)
	// The request is made on the goroutine that reads it, so the response
	// is closed where it was opened, which is where bodyclose looks.
	go func() {
		defer close(frames)
		resp, err := h.server.Client().Do(req)
		if err != nil {
			answered <- 0
			return
		}
		defer resp.Body.Close()
		answered <- resp.StatusCode
		scanner := bufio.NewScanner(resp.Body)
		var frame sseFrame
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case line == "":
				if frame.data != "" {
					select {
					case frames <- frame:
					case <-ctx.Done():
						return
					}
				}
				frame = sseFrame{}
			case strings.HasPrefix(line, "id: "):
				frame.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				frame.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				frame.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	if status := <-answered; status != http.StatusOK {
		t.Fatalf("the stream answered %d", status)
	}
	return frames
}

// nextFrame waits for the stream's next message.
func nextFrame(t *testing.T, frames <-chan sseFrame) sseFrame {
	t.Helper()
	select {
	case frame, ok := <-frames:
		if !ok {
			t.Fatal("the stream ended")
		}
		return frame
	case <-time.After(5 * time.Second):
		t.Fatal("nothing arrived on the stream")
	}
	return sseFrame{}
}

// streamEnds waits for the stream to end, whatever it still sends first.
func streamEnds(t *testing.T, frames <-chan sseFrame, why string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-frames:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal(why)
		}
	}
}

// multiplexed reads a frame of a multiplexed stream.
func multiplexed(t *testing.T, frame sseFrame) (topic, eventType string, data string) {
	t.Helper()
	var decoded struct {
		Topic string          `json:"topic"`
		Type  string          `json:"type"`
		Data  json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(frame.data), &decoded); err != nil {
		t.Fatalf("a frame is not JSON: %q", frame.data)
	}
	return decoded.Topic, decoded.Type, string(decoded.Data)
}

func quickStreamRecheck(t *testing.T) {
	t.Helper()
	was := streamRecheck
	streamRecheck = 20 * time.Millisecond
	t.Cleanup(func() { streamRecheck = was })
}

func TestAStreamEndsWithTheSessionThatOpenedIt(t *testing.T) {
	quickStreamRecheck(t)
	for _, c := range []struct {
		name string
		end  func(h *harness, user store.User, session store.Session) error
	}{
		{"signed out somewhere else", func(h *harness, _ store.User, session store.Session) error {
			return h.db.DeleteSession(context.Background(), session.ID)
		}},
		{"account disabled", func(h *harness, user store.User, _ store.Session) error {
			user.Disabled = true
			return h.db.UpdateUser(context.Background(), &user)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			acme := h.newTenant("acme")
			signedIn, session := h.sessionFor(t, acme.user, auth.MethodPassword)
			topic := "team:" + acme.team.ID
			frames := h.openStream(t, "multiplex=true&topics="+topic, func(req *http.Request) {
				for _, cookie := range signedIn.cookies {
					req.AddCookie(cookie)
				}
			})

			// Several rechecks pass for a session that is still good, and
			// the stream goes on carrying what it carried.
			time.Sleep(150 * time.Millisecond)
			h.api.hub.Publish(topic, "project.created", map[string]string{"id": "one"})
			if got, kind, _ := multiplexed(t, nextFrame(t, frames)); got != topic || kind != "project.created" {
				t.Fatalf("a signed-in stream carried %s %s", got, kind)
			}

			if err := c.end(h, acme.user, session); err != nil {
				t.Fatal(err)
			}
			streamEnds(t, frames, "the stream outlived the session that opened it")
		})
	}
}

func TestAStreamStopsCarryingWhatItsReaderMayNoLongerSee(t *testing.T) {
	quickStreamRecheck(t)
	h := newHarness(t)
	acme := h.newTenant("acme")
	web := h.app(acme, "web")
	ops := h.newMember(acme, "ops", store.RoleMember)
	team, logs := "team:"+acme.team.ID, "app-logs:"+web.ID
	asOps := func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+ops.token) }

	shared := h.openStream(t, "multiplex=true&topics="+team+","+logs, asOps)
	own := h.openStream(t, "topics="+team, asOps)

	// Limited to the one project: the team's topic carries every project's
	// events, and a limited member is refused it; the app's logs are in
	// their project and stay theirs.
	if err := h.db.SetMembership(t.Context(), acme.team.ID, ops.user.ID, store.RoleMember, []string{acme.project.ID}); err != nil {
		t.Fatal(err)
	}

	// The shared stream says so, by itself, and goes on with the rest.
	refusal := nextFrame(t, shared)
	if topic, kind, data := multiplexed(t, refusal); topic != team || kind != "refused" || refusal.id != "" {
		t.Fatalf("the first frame after the limit is %s %s, not a refusal of %s", topic, kind, team)
	} else if !strings.Contains(data, `"code"`) {
		t.Errorf("the refusal does not say why: %s", data)
	}
	h.api.hub.Publish(team, "project.created", map[string]string{"id": "elsewhere"})
	h.api.hub.Publish(logs, "log", "a line")
	if topic, kind, _ := multiplexed(t, nextFrame(t, shared)); topic != logs || kind != "log" {
		t.Fatalf("the next frame is %s %s; only the app's logs should still arrive", topic, kind)
	}

	// A stream of nothing but that topic ends, as asking for it now would be
	// refused.
	streamEnds(t, own, "a stream of a topic its reader may no longer see stayed open")
}
