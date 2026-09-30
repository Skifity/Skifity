package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"skifity/internal/store"
)

// A channel limited to some projects.
//
// The whole promise is one sentence: an event about project A never reaches a
// channel limited to project B. The other half matters as much — an event
// about a server, which belongs to no project, reaches every channel, limited
// or not, because a server that stopped answering takes every project's apps
// with it.

// namedSink records which channels a dispatcher delivered to, by the path
// each channel's webhook points at.
type namedSink struct {
	mu      sync.Mutex
	reached []string
}

func (s *namedSink) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		s.mu.Lock()
		s.reached = append(s.reached, r.URL.Path[1:])
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *namedSink) got() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := slices.Clone(s.reached)
	slices.Sort(out)
	return out
}

func TestAProjectsEventReachesOnlyTheChannelsThatFollowIt(t *testing.T) {
	sink := &namedSink{}
	server := httptest.NewServer(sink.handler())
	defer server.Close()

	channel := func(name string, scoped bool, projects ...string) store.NotificationChannel {
		return store.NotificationChannel{
			ID: "ntf_" + name, Name: name, Kind: "webhook", Enabled: true,
			Scoped: scoped, Projects: projects, ConfigEnc: webhookConfig(server.URL + "/" + name),
		}
	}
	db := fakeStore{channels: []store.NotificationChannel{
		channel("everything", false),
		channel("shop", true, "prj_shop"),
		channel("blog", true, "prj_blog"),
		channel("both", true, "prj_blog", "prj_shop"),
		// Limited, and its only project has since been deleted: it must not
		// go back to hearing every project.
		channel("orphaned", true),
	}}
	d := NewDispatcher(db, fakeKeyring{}, quietLogger(), nil)

	for _, tc := range []struct {
		what  string
		event string
		msg   Message
		want  []string
	}{
		{
			what: "a deployment in the shop project", event: EventDeployFailed,
			msg:  Message{Title: "Deploying shop failed", ProjectID: "prj_shop"},
			want: []string{"both", "everything", "shop"},
		},
		{
			what: "a backup in the blog project", event: EventBackupFailed,
			msg:  Message{Title: "Backing up blog-db failed", ProjectID: "prj_blog"},
			want: []string{"blog", "both", "everything"},
		},
		{
			what: "a project no channel is limited to", event: EventAppUnhealthy,
			msg:  Message{Title: "docs has no running instances", ProjectID: "prj_docs"},
			want: []string{"everything"},
		},
		{
			what: "a server, which is the whole team's", event: EventServerLost,
			msg:  Message{Title: "web-2 stopped answering"},
			want: []string{"blog", "both", "everything", "orphaned", "shop"},
		},
	} {
		t.Run(tc.what, func(t *testing.T) {
			sink.mu.Lock()
			sink.reached = nil
			sink.mu.Unlock()

			d.Notify(context.Background(), "team_1", tc.event, tc.msg)
			d.Wait()

			if got := sink.got(); !slices.Equal(got, tc.want) {
				t.Fatalf("reached %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFollows(t *testing.T) {
	limited := store.NotificationChannel{Scoped: true, Projects: []string{"prj_a"}}
	everything := store.NotificationChannel{}
	orphaned := store.NotificationChannel{Scoped: true}

	for _, tc := range []struct {
		channel store.NotificationChannel
		project string
		want    bool
	}{
		{limited, "prj_a", true},
		{limited, "prj_b", false},
		{limited, "", true},
		{everything, "prj_b", true},
		{everything, "", true},
		{orphaned, "prj_a", false},
		{orphaned, "", true},
	} {
		if got := follows(tc.channel, tc.project); got != tc.want {
			t.Errorf("follows(scoped=%v %v, %q) = %v, want %v",
				tc.channel.Scoped, tc.channel.Projects, tc.project, got, tc.want)
		}
	}
}

// The project is how the panel routes a message, not something the message
// says: a webhook receiver is sent what a person reads, and an id is not that.
func TestTheProjectIsNotSentToTheChannel(t *testing.T) {
	encoded, err := json.Marshal(Message{Title: "x", ProjectID: "prj_secret"})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	for key, value := range fields {
		if value == "prj_secret" {
			t.Errorf("the project id is sent as %q", key)
		}
	}
}
