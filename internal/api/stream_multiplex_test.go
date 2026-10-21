package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The panel's interface keeps one stream per browser tab and asks for every
// topic its cards want on it. A topic the caller may not see is refused on the
// stream, by itself, and everything else keeps arriving; each event says which
// topic it is on, since two topics can carry an event of the same name.
func TestAMultiplexedStreamRefusesOneTopicAndCarriesTheRest(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	globex := h.newTenant("globex")
	ours, theirs := "team:"+acme.team.ID, "team:"+globex.team.ID

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		h.server.URL+"/api/events?multiplex=true&topics="+ours+","+theirs, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+acme.token)
	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the stream answered %d; one refused topic must not refuse the stream", resp.StatusCode)
	}

	type frame struct {
		id    string
		Topic string          `json:"topic"`
		Type  string          `json:"type"`
		Data  json.RawMessage `json:"data"`
	}
	frames := make(chan frame, 8)
	go func(body io.Reader) {
		defer close(frames)
		scanner := bufio.NewScanner(body)
		var id string
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "id: "):
				id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				t.Errorf("a multiplexed stream names an event outside its data: %q", line)
			case strings.HasPrefix(line, "data: "):
				var f frame
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &f); err != nil {
					t.Errorf("a frame is not JSON: %q", line)
					continue
				}
				f.id, id = id, ""
				frames <- f
			}
		}
	}(resp.Body)
	next := func() frame {
		t.Helper()
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatal("the stream ended")
			}
			return f
		case <-ctx.Done():
			t.Fatal("no frame arrived")
		}
		return frame{}
	}

	// The refusal comes first, before anything is published: by then the
	// subscription for the rest is in place.
	refusal := next()
	if refusal.Type != "refused" || refusal.Topic != theirs || refusal.id != "" {
		t.Fatalf("the first frame is %+v, not a refusal of %s", refusal, theirs)
	}
	if !strings.Contains(string(refusal.Data), `"code"`) {
		t.Errorf("the refusal does not say why: %s", refusal.Data)
	}

	h.api.hub.Publish(theirs, "deployment", map[string]string{"id": "theirs"})
	published := h.api.hub.Publish(ours, "deployment", map[string]string{"id": "ours"})

	got := next()
	if got.Topic != ours || got.Type != "deployment" || !strings.Contains(string(got.Data), `"ours"`) {
		t.Fatalf("the next frame is %+v; only acme's own event should arrive", got)
	}
	if got.id != strconv.FormatInt(published.Seq, 10) {
		t.Errorf("the event carries id %q, not its sequence number %d", got.id, published.Seq)
	}
}
