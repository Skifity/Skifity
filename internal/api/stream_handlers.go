package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/logging"
	"skifity/internal/store"
)

// sseHeartbeat keeps proxies from closing an idle stream.
const sseHeartbeat = 25 * time.Second

// An open stream is asked again, every streamRecheck, whether whoever opened
// it is still signed in and may still see each topic it carries. The decision
// made when it opened used to be the only one, and the interface's stream
// stays open for as long as the browser tab does: signing out somewhere else,
// a session that ran out, an account disabled, a member removed from the team
// or limited to some projects all left it carrying the team's events.
//
// A recheck that cannot be answered — the panel's database busy for a moment —
// is not a no: closing on it sends every open tab back to refetch everything
// at the moment the panel can least afford it. streamPatience of them in a row
// is. Variables, so a test need not wait.
var (
	streamRecheck  = 15 * time.Second
	streamPatience = 3
)

// maxStreamsPerUser is how many realtime streams one account may hold open at
// once. The interface keeps a handful (the list it is on, the log it is
// reading); a script, or a browser with a hundred tabs, that opens streams
// without end would keep a goroutine, a buffer and a replay for each, in the one
// process that runs everything. Past this the oldest tab is not dropped: the
// new one is refused, and says why.
const maxStreamsPerUser = 40

// maxStreamTopics is how many topics one stream may ask for. A browser tab asks
// for a handful; each one is a lookup and an authorization, and a multiplexed
// stream decides every one of them rather than stopping at the first refusal.
const maxStreamTopics = 64

// streamCounter counts open streams per account.
type streamCounter struct {
	mu   sync.Mutex
	open map[string]int
}

// acquire takes a place for the account, or says there is none left.
func (c *streamCounter) acquire(user string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.open == nil {
		c.open = map[string]int{}
	}
	if c.open[user] >= maxStreamsPerUser {
		return false
	}
	c.open[user]++
	return true
}

func (c *streamCounter) release(user string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.open[user] <= 1 {
		delete(c.open, user)
		return
	}
	c.open[user]--
}

// handleEventStream is the panel's realtime channel. A client subscribes to the
// topics it is allowed to see; authorization happens here, when the stream
// opens and again every streamRecheck, rather than per event.
//
// ?multiplex=true is how the panel's own interface asks. It keeps one stream
// per browser tab for every page and card that wants live updates — one
// connection per card ran a browser out of its six connections to a host over
// plain HTTP — so each event says which topic it belongs to, and a topic the
// caller may not see is refused on the stream, by itself, rather than failing
// the request: one card asking for something it should not have must not stop
// every other card in the tab.
func (s *Server) handleEventStream(w http.ResponseWriter, r *http.Request) {
	if len(requestedTopics(r)) > maxStreamTopics {
		writeError(w, r, errdoc.BadRequest(fmt.Sprintf("Ask for at most %d topics on one stream.", maxStreamTopics)))
		return
	}
	multiplex := queryBool(r, "multiplex")
	var (
		topics  []string
		refused []refusedTopic
	)
	if multiplex {
		topics, refused = s.authorizeEachTopic(r)
	} else {
		var err error
		if topics, err = s.authorizeTopics(r); err != nil {
			writeError(w, r, err)
			return
		}
	}
	if len(topics) == 0 && len(refused) == 0 {
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

	// Whoever it is, counted by account: a token is an account too.
	if user, ok := UserFrom(r.Context()); ok {
		if !s.streams.acquire(user.ID) {
			w.Header().Set("Retry-After", "10")
			writeError(w, r, errdoc.TooManyStreams(maxStreamsPerUser))
			return
		}
		defer s.streams.release(user.ID)
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
	for _, topic := range refused {
		frame, err := taggedFrame(0, topic.Topic, "refused", topic.Problem)
		if err != nil {
			continue
		}
		if _, err := w.Write(frame); err != nil {
			return
		}
	}
	_ = rc.Flush()

	heartbeat := time.NewTicker(sseHeartbeat)
	defer heartbeat.Stop()
	recheck := time.NewTicker(streamRecheck)
	defer recheck.Stop()
	// Topics withdrawn while the stream was open. The hub still routes them
	// here, and what it had already queued is on its way: both are dropped
	// rather than written.
	withdrawn := map[string]bool{}
	unanswered := 0

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
			frame := fmt.Appendf(nil, "event: desync\ndata: {\"missed\":%d}\n\n", dropped)
			if multiplex {
				frame, _ = taggedFrame(0, "", "desync", map[string]int64{"missed": dropped})
			}
			if _, err := w.Write(frame); err != nil {
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
		case <-recheck.C:
			signedIn, lost, err := s.recheckStream(r, topics, withdrawn)
			switch {
			case err != nil:
				unanswered++
				if unanswered < streamPatience {
					s.log.Warn("could not recheck an event stream; it stays open for now", "error", err)
					continue
				}
				s.log.Warn("closed an event stream that could not be rechecked", "error", err)
				return
			case !signedIn:
				// The browser reconnects by itself, is refused as anybody
				// without a session is, and the tab signs out.
				return
			}
			unanswered = 0
			if len(lost) > 0 && !multiplex {
				// A stream of its own is refused outright when it opens, so
				// one that loses its topic ends: asking again is refused.
				return
			}
			for _, topic := range lost {
				withdrawn[topic.Topic] = true
				frame, err := taggedFrame(0, topic.Topic, "refused", topic.Problem)
				if err != nil {
					continue
				}
				if _, err := w.Write(frame); err != nil {
					return
				}
			}
			if len(lost) > 0 {
				_ = rc.Flush()
			}
		case ev, ok := <-sub.Events():
			if !ok {
				return
			}
			if withdrawn[ev.Topic] {
				continue
			}
			frame, err := ev.Encode()
			if multiplex {
				frame, err = taggedFrame(ev.Seq, ev.Topic, ev.Type, ev.Data)
			}
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

// recheckStream signs the stream's request in again, with the credential it
// was opened with, and decides again each topic it still carries. signedIn is
// false when that credential no longer signs anybody in; lost is the topics
// they may no longer see. An error means it could not be asked.
//
// A cookie session is looked at without being extended, as a database
// tunnel's is: a tab left open is not the person doing anything, and must not
// keep an idle session alive.
func (s *Server) recheckStream(r *http.Request, topics []string, withdrawn map[string]bool) (signedIn bool, lost []refusedTopic, err error) {
	// Not the request's context: that one already says who opened the
	// stream, and would go on saying it for a credential that signs nobody in.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx, err = s.identifyAs(ctx, r, s.auth.CheckSession)
	if err != nil {
		if refused(err) {
			return false, nil, nil
		}
		return false, nil, err
	}
	if _, ok := UserFrom(ctx); !ok {
		return false, nil, nil
	}
	again := r.WithContext(ctx)
	for _, topic := range topics {
		if withdrawn[topic] {
			continue
		}
		if err := s.authorizeTopic(again, topic); err != nil {
			if !refused(err) {
				return true, nil, err
			}
			lost = append(lost, refusedTopic{Topic: topic, Problem: toProblem(err)})
		}
	}
	return true, lost, nil
}

// authorizeTopics turns the requested topics into the subset the caller may see.
// A topic the caller is not entitled to is an error, not a silent drop, so a
// broken client is visible rather than mysteriously quiet.
func (s *Server) authorizeTopics(r *http.Request) ([]string, error) {
	requested := requestedTopics(r)
	out := make([]string, 0, len(requested))
	for _, topic := range requested {
		if err := s.authorizeTopic(r, topic); err != nil {
			return nil, err
		}
		out = append(out, topic)
	}
	return out, nil
}

// refusedTopic is a topic a multiplexed stream was asked for and will not
// carry, with the reason a stream of its own would have been refused.
type refusedTopic struct {
	Topic   string
	Problem *errdoc.Problem
}

// authorizeEachTopic is authorizeTopics for a multiplexed stream: every topic
// is decided on its own, and a refusal is said on the stream rather than
// ending it. Still not a silent drop: the client is told which, and why.
func (s *Server) authorizeEachTopic(r *http.Request) ([]string, []refusedTopic) {
	var allowed []string
	var refused []refusedTopic
	for _, topic := range requestedTopics(r) {
		if err := s.authorizeTopic(r, topic); err != nil {
			refused = append(refused, refusedTopic{Topic: topic, Problem: toProblem(err)})
			continue
		}
		allowed = append(allowed, topic)
	}
	return allowed, refused
}

func requestedTopics(r *http.Request) []string {
	var out []string
	for _, topic := range strings.Split(r.URL.Query().Get("topics"), ",") {
		if topic = strings.TrimSpace(topic); topic != "" {
			out = append(out, topic)
		}
	}
	return out
}

// authorizeTopic decides one topic.
func (s *Server) authorizeTopic(r *http.Request, topic string) error {
	kind, id, ok := strings.Cut(topic, ":")
	if !ok {
		return errdoc.BadRequest("Topic " + topic + " is not in the form kind:id.")
	}
	switch kind {
	case "team":
		if _, err := s.authorizeTeam(r, id, store.RoleViewer); err != nil {
			return err
		}
	case "operation":
		op, err := s.db.GetOperation(r.Context(), id)
		if err != nil {
			return errdoc.NotFound("operation", id)
		}
		if err := s.authorizeOperation(r, op, store.RoleViewer); err != nil {
			return err
		}
	case "deployment":
		deployment, err := s.db.GetDeployment(r.Context(), id)
		if err != nil {
			return errdoc.NotFound("deployment", id)
		}
		if _, _, err := s.authorizeAppID(r, deployment.AppID, store.RoleViewer); err != nil {
			return err
		}
	case "app-logs":
		if _, _, err := s.authorizeAppID(r, id, store.RoleViewer); err != nil {
			return err
		}
	default:
		return errdoc.BadRequest("Unknown topic kind " + kind + ".")
	}
	return nil
}

// taggedFrame is one event on a multiplexed stream: an unnamed SSE message
// whose data says which topic and which event it is, around the data a stream
// of its own would have sent. A frame with no sequence number, a refusal or a
// desync, carries no id, so it does not move the client's place.
func taggedFrame(seq int64, topic, eventType string, data any) ([]byte, error) {
	payload, err := json.Marshal(struct {
		Topic string `json:"topic,omitempty"`
		Type  string `json:"type"`
		Data  any    `json:"data"`
	}{topic, eventType, data})
	if err != nil {
		return nil, fmt.Errorf("encode event: %w", err)
	}
	if seq > 0 {
		return fmt.Appendf(nil, "id: %d\ndata: %s\n\n", seq, payload), nil
	}
	return fmt.Appendf(nil, "data: %s\n\n", payload), nil
}

// logEntry is one line of an app's log as the API sends it: which instance
// wrote it, and when by that instance's server's clock.
type logEntry struct {
	Instance string    `json:"instance"`
	At       time.Time `json:"at,omitzero"`
	Line     string    `json:"line"`
}

// text is the line as a plain line of text, with its time in front when that
// was asked for, as kubectl's --timestamps writes it.
func (e logEntry) text(timestamps bool) string {
	if timestamps && !e.At.IsZero() {
		return e.At.UTC().Format(time.RFC3339Nano) + " " + e.Line
	}
	return e.Line
}

// scrubbedEntry is a line as it may leave the panel. An app that prints its
// own credentials should not have them stored in a browser's memory too.
func scrubbedEntry(entry kube.LogEntry) logEntry {
	return logEntry{Instance: entry.Instance, At: entry.At, Line: logging.Scrub(entry.Line)}
}

// logInstances says which instances a log read is reading, and how many it
// leaves out.
type logInstances struct {
	Instances []string `json:"instances"`
	Omitted   int      `json:"omitted"`
}

func readingInstances(stream *kube.LogStream) logInstances {
	names, omitted := stream.Instances()
	if names == nil {
		names = []string{}
	}
	return logInstances{Instances: names, Omitted: omitted}
}

// longestLogSince is as far back as a log read may be bounded to. Further
// than that is not "since", it is everything the node still has.
const longestLogSince = 30 * 24 * time.Hour

// parseLogSince reads how far back to read a log: 15m, 1h, 90s or 2d. Empty is
// no bound.
func parseLogSince(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	duration, err := time.ParseDuration(raw)
	if err != nil {
		if days, ok := strings.CutSuffix(raw, "d"); ok {
			if n, convErr := strconv.Atoi(days); convErr == nil {
				duration, err = time.Duration(n)*24*time.Hour, nil
			}
		}
	}
	if err != nil || duration < time.Second || duration > longestLogSince {
		return 0, errdoc.BadRequest("since is how far back to read, such as 15m, 1h or 2d, from one second up to 30 days.")
	}
	return int64(duration / time.Second), nil
}

// handleAppLogs reads an app's runtime logs: every instance's, merged in the
// order the lines were written, or one instance's.
//
// Logs come straight from the Kubernetes API rather than through the event hub,
// because they can be large and there is no reason to buffer them in the panel.
func (s *Server) handleAppLogs(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleViewer)
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
	timestamps := queryBool(r, "timestamps")
	tailLines := int64(queryInt(r, "tail", 200))
	if tailLines < 1 || tailLines > 10000 {
		tailLines = 200
	}
	since, err := parseLogSince(r.URL.Query().Get("since"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	instance := strings.TrimSpace(r.URL.Query().Get("instance"))

	// One of the app's other processes, by name: a worker's output is not
	// in the web's.
	target := app.Slug
	if process := r.URL.Query().Get("process"); process != "" && process != "web" {
		if !s.hasProcess(r, app.ID, process) {
			writeError(w, r, errdoc.NotFound("process", process))
			return
		}
		target = kube.ProcessDeploymentName(app.Slug, process)
	}

	stream, err := s.cluster.AppLogs(r.Context(), env.Namespace, target, LogOptions{
		TailLines: tailLines, Follow: follow, Previous: previous,
		Instance: instance, SinceSeconds: since,
	})
	if err != nil {
		// No instance has started, so there is no log yet: what it is
		// waiting for, rather than a failure to read one.
		var notStarted *kube.NotStarted
		var unknown *kube.UnknownInstance
		switch {
		case errors.As(err, &unknown):
			err = errdoc.NotFound("instance", unknown.Name)
		case errors.As(err, &notStarted) && instance != "":
			err = errdoc.InstanceNotStarted(notStarted.Instance, notStarted.Reason)
		case errors.As(err, &notStarted):
			err = errdoc.AppNotStarted(notStarted.Instance, notStarted.Reason)
		}
		writeError(w, r, err)
		return
	}
	defer stream.Close()

	if !follow {
		// A plain read returns the lines as JSON, which is easier for the CLI
		// and for anything scripting against the API. lines is each line's
		// text alone, as it was before entries said which instance wrote it.
		lines := []string{}
		entries := []logEntry{}
		for raw := range stream.Entries() {
			entry := scrubbedEntry(raw)
			entries = append(entries, entry)
			lines = append(lines, entry.text(timestamps))
		}
		reading := readingInstances(stream)
		writeJSON(w, http.StatusOK, map[string]any{
			"app_id": app.ID, "lines": lines, "entries": entries, "previous": previous,
			"instances": reading.Instances, "instances_omitted": reading.Omitted,
		})
		return
	}

	writeSSEHeaders(w)
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	sendInstances := func() bool {
		data, err := json.Marshal(readingInstances(stream))
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: instances\ndata: %s\n\n", data); err != nil {
			return false
		}
		_ = rc.Flush()
		return true
	}
	if !sendInstances() {
		return
	}

	// A healthy app that is simply idle is the ordinary case for this
	// stream, and a connection with nothing on it is what a proxy in front
	// of the panel closes.
	heartbeat := time.NewTicker(sseHeartbeat)
	defer heartbeat.Stop()

	seq := 0
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			_ = rc.Flush()
		case <-stream.Changed():
			if !sendInstances() {
				return
			}
		case raw, ok := <-stream.Entries():
			if !ok {
				// The read stopped by itself: the cluster stopped answering
				// with nothing left to read.
				if err := stream.Err(); err != nil {
					fmt.Fprintf(w, "event: error\ndata: %s\n\n", jsonString(err.Error()))
					_ = rc.Flush()
				}
				return
			}
			seq++
			entry := scrubbedEntry(raw)
			data, err := json.Marshal(entry)
			if err != nil {
				continue
			}
			// The entry says which instance and when. The log event beside it
			// is the line alone: a client written before entries existed reads
			// every event's data as a string, and skips what is not one.
			if _, err := fmt.Fprintf(w, "id: %d\nevent: entry\ndata: %s\n\nevent: log\ndata: %s\n\n",
				seq, data, jsonString(entry.text(timestamps))); err != nil {
				return
			}
			// A first read of thousands of lines is sent in a few writes
			// rather than one each.
			if len(stream.Entries()) == 0 {
				_ = rc.Flush()
			}
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
