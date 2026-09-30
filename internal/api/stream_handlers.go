package api

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/logging"
	"skifity/internal/runsafe"
	"skifity/internal/store"
)

// sseHeartbeat keeps proxies from closing an idle stream.
const sseHeartbeat = 25 * time.Second

// handleEventStream is the panel's realtime channel. A client subscribes to the
// topics it is allowed to see; authorization happens here, once, rather than per
// event.
func (s *Server) handleEventStream(w http.ResponseWriter, r *http.Request) {
	topics, err := s.authorizeTopics(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if len(topics) == 0 {
		writeError(w, r, errdoc.BadRequest("Ask for at least one topic, for example ?topics=team:team_123."))
		return
	}

	lastSeq := int64(0)
	// The browser resends the last id it saw automatically on reconnect.
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		lastSeq, _ = strconv.ParseInt(raw, 10, 64)
	} else if raw := r.URL.Query().Get("since"); raw != "" {
		lastSeq, _ = strconv.ParseInt(raw, 10, 64)
	}

	sub := s.hub.Subscribe(r.Context(), lastSeq, topics...)
	defer sub.Close()

	writeSSEHeaders(w)
	rc := http.NewResponseController(w)
	// The stream must not be cut off by the server's write deadline.
	_ = rc.SetWriteDeadline(time.Time{})

	if _, err := fmt.Fprintf(w, "retry: 3000\n\n"); err != nil {
		return
	}
	_ = rc.Flush()

	heartbeat := time.NewTicker(sseHeartbeat)
	defer heartbeat.Stop()

	// What the subscriber had already missed when this loop started.
	//
	// The hub never blocks a publisher: a client that cannot keep up loses
	// events and the hub counts them. Nothing ever read that count, so a
	// browser that was asleep with a build log open came back to a page that
	// had quietly stopped being true and said nothing about it. Now it is told,
	// and refetches instead of trusting what it has.
	missed := sub.Dropped()

	for {
		// Checked on every turn of the loop rather than only on a heartbeat: a
		// client that is behind is behind now, and the next thing it renders
		// should already know.
		if dropped := sub.Dropped(); dropped > missed {
			missed = dropped
			if _, err := fmt.Fprintf(w, "event: desync\ndata: {\"missed\":%d}\n\n", dropped); err != nil {
				return
			}
			_ = rc.Flush()
		}

		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			// A comment frame is ignored by the client but keeps the
			// connection alive through proxies with idle timeouts.
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			_ = rc.Flush()
		case ev, ok := <-sub.Events():
			if !ok {
				return
			}
			frame, err := ev.Encode()
			if err != nil {
				s.log.Warn("could not encode event", "topic", ev.Topic, "error", err)
				continue
			}
			if _, err := w.Write(frame); err != nil {
				return
			}
			_ = rc.Flush()
		}
	}
}

// authorizeTopics turns the requested topics into the subset the caller may see.
// A topic the caller is not entitled to is an error, not a silent drop, so a
// broken client is visible rather than mysteriously quiet.
func (s *Server) authorizeTopics(r *http.Request) ([]string, error) {
	requested := strings.Split(r.URL.Query().Get("topics"), ",")
	out := make([]string, 0, len(requested))
	for _, topic := range requested {
		topic = strings.TrimSpace(topic)
		if topic == "" {
			continue
		}
		kind, id, ok := strings.Cut(topic, ":")
		if !ok {
			return nil, errdoc.BadRequest("Topic " + topic + " is not in the form kind:id.")
		}
		switch kind {
		case "team":
			if _, err := s.authorizeTeam(r, id, store.RoleMember); err != nil {
				return nil, err
			}
		case "operation":
			op, err := s.db.GetOperation(r.Context(), id)
			if err != nil {
				return nil, errdoc.NotFound("operation", id)
			}
			if _, err := s.authorizeTeam(r, op.TeamID, store.RoleMember); err != nil {
				return nil, err
			}
		case "deployment":
			deployment, err := s.db.GetDeployment(r.Context(), id)
			if err != nil {
				return nil, errdoc.NotFound("deployment", id)
			}
			if _, _, err := s.authorizeAppID(r, deployment.AppID, store.RoleMember); err != nil {
				return nil, err
			}
		case "app-logs":
			if _, _, err := s.authorizeAppID(r, id, store.RoleMember); err != nil {
				return nil, err
			}
		default:
			return nil, errdoc.BadRequest("Unknown topic kind " + kind + ".")
		}
		out = append(out, topic)
	}
	return out, nil
}

// handleAppLogs streams an app's runtime logs.
//
// Logs come straight from the Kubernetes API rather than through the event hub,
// because they can be large and there is no reason to buffer them in the panel.
func (s *Server) handleAppLogs(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	env, err := s.db.GetEnvironment(r.Context(), app.EnvironmentID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if s.cluster == nil {
		writeError(w, r, errdoc.ClusterUnreachable(nil))
		return
	}

	// "previous" reads the container that ran before the current one. When an
	// app crash-loops, the container that printed the reason has already been
	// replaced and the live stream no longer has it: this is the only copy.
	previous := queryBool(r, "previous")
	follow := queryBool(r, "follow") && !previous
	tailLines := int64(queryInt(r, "tail", 200))
	if tailLines < 1 || tailLines > 10000 {
		tailLines = 200
	}

	stream, err := s.cluster.AppLogs(r.Context(), env.Namespace, app.Slug, LogOptions{
		TailLines: tailLines, Follow: follow, Previous: previous,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	defer stream.Close()

	if !follow {
		// A plain read returns the lines as JSON, which is easier for the CLI
		// and for anything scripting against the API.
		lines := []string{}
		scanner := bufio.NewScanner(stream)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			lines = append(lines, logging.Scrub(scanner.Text()))
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"app_id": app.ID, "lines": lines, "previous": previous,
		})
		return
	}

	writeSSEHeaders(w)
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	// Read on its own goroutine, so the heartbeat below can still be written
	// while the app is saying nothing. A healthy app that is simply idle is the
	// ordinary case for this stream, and a connection with nothing on it is
	// what a proxy in front of the panel closes.
	lines := make(chan string, 64)
	readErr := make(chan error, 1)
	go func() {
		// close(lines) is deferred first so it runs last: whatever happens,
		// the reader sees the stream end rather than waiting for ever.
		defer close(lines)
		// This scrubs an app's own output, which is the least trustworthy text
		// the panel handles. A panic in it must not end every other session.
		defer runsafe.Recover(s.log, "streaming an app's logs", nil)
		scanner := bufio.NewScanner(stream)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			// Scrub before the line leaves the process: an app that prints its
			// own credentials should not have them stored in a browser's
			// memory too.
			select {
			case lines <- logging.Scrub(scanner.Text()):
			case <-r.Context().Done():
				return
			}
		}
		if err := scanner.Err(); err != nil {
			readErr <- err
		}
	}()

	heartbeat := time.NewTicker(sseHeartbeat)
	defer heartbeat.Stop()

	seq := 0
	for {
		select {
		case <-r.Context().Done():
			return
		case err := <-readErr:
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", jsonString(err.Error()))
			_ = rc.Flush()
			return
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			_ = rc.Flush()
		case line, ok := <-lines:
			if !ok {
				// The container stopped writing, which for a log that is not
				// being followed any more is simply the end.
				return
			}
			seq++
			if _, err := fmt.Fprintf(w, "id: %d\nevent: log\ndata: %s\n\n", seq, jsonString(line)); err != nil {
				return
			}
			_ = rc.Flush()
		}
	}
}

func writeSSEHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	// Traefik does not buffer by default, but an nginx in front of it would.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
}

// jsonString quotes a string for an SSE data field, which must not contain a
// raw newline.
func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
