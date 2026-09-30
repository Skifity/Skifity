package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"skifity/internal/store"
)

// Changing a notification channel: its secrets stay write-only, its settings
// stay sealed under its name, and only a team administrator does it.

// createChannel adds a Telegram channel through the API and returns its id.
func (h *harness) createChannel(as tenant, body map[string]any) string {
	h.t.Helper()
	status, answer := h.do(as, http.MethodPost, "/api/teams/"+as.team.ID+"/notifications", body)
	if status != http.StatusCreated {
		h.t.Fatalf("create a channel: %d\n%s", status, answer)
	}
	var created store.NotificationChannel
	if err := json.Unmarshal([]byte(answer), &created); err != nil {
		h.t.Fatal(err)
	}
	return created.ID
}

// storedConfig opens a channel's settings the way the dispatcher does.
func (h *harness) storedConfig(channelID string) map[string]string {
	h.t.Helper()
	channel, err := h.db.GetNotificationChannel(h.t.Context(), channelID)
	if err != nil {
		h.t.Fatal(err)
	}
	raw, err := h.keyring.Open(channel.ConfigEnc, "notification_channel:"+channel.TeamID+":"+channel.Name)
	if err != nil {
		h.t.Fatalf("the channel's settings do not open under its own name: %v", err)
	}
	var config map[string]string
	if err := json.Unmarshal(raw, &config); err != nil {
		h.t.Fatal(err)
	}
	return config
}

const fakeBotToken = "123456:fake-bot-token-for-tests"

func TestAChannelIsChangedAndKeepsTheSecretsLeftEmpty(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	id := h.createChannel(acme, map[string]any{
		"kind": "telegram", "name": "ops",
		"config": map[string]string{"bot_token": fakeBotToken, "chat_id": "-100"},
		"events": []string{"deploy.failed"},
	})
	path := "/api/teams/" + acme.team.ID + "/notifications/" + id

	// The form is given what it may show, and told the token is stored.
	status, body := h.do(acme, http.MethodGet, path, nil)
	if status != http.StatusOK {
		t.Fatalf("read the channel: %d\n%s", status, body)
	}
	if strings.Contains(body, fakeBotToken) {
		t.Fatalf("the bot token was sent back to the form:\n%s", body)
	}
	var view struct {
		Config  map[string]string `json:"config"`
		Secrets []string          `json:"secrets"`
	}
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatal(err)
	}
	if view.Config["chat_id"] != "-100" || !slices.Equal(view.Secrets, []string{"bot_token"}) {
		t.Fatalf("the form is shown %v and told of %v", view.Config, view.Secrets)
	}

	// Everything changes at once, the token box untouched: renamed, another
	// chat, paused, limited to a project.
	status, body = h.do(acme, http.MethodPut, path, map[string]any{
		"name": "shop alerts", "events": []string{"deploy.failed", "app.unhealthy"}, "enabled": false,
		"projects": []string{acme.project.ID},
		"config":   map[string]string{"bot_token": "", "chat_id": "-200"},
	})
	if status != http.StatusOK {
		t.Fatalf("change the channel: %d\n%s", status, body)
	}
	if strings.Contains(body, fakeBotToken) {
		t.Fatalf("the bot token is in the answer:\n%s", body)
	}
	channel, err := h.db.GetNotificationChannel(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if channel.Name != "shop alerts" || channel.Events != "deploy.failed,app.unhealthy" || channel.Enabled ||
		!channel.Scoped || !slices.Equal(channel.Projects, []string{acme.project.ID}) || channel.Kind != "telegram" {
		t.Fatalf("the channel is %+v", channel)
	}
	// Renamed, and sealed again under the new name, or the dispatcher could
	// never open it.
	config := h.storedConfig(id)
	if config["bot_token"] != fakeBotToken || config["chat_id"] != "-200" {
		t.Fatalf("the settings are %v; want the token kept and the chat changed", config)
	}

	// A request that says nothing about enabled or the settings keeps both.
	status, body = h.do(acme, http.MethodPut, path, map[string]any{"name": "shop alerts"})
	if status != http.StatusOK {
		t.Fatalf("rename only: %d\n%s", status, body)
	}
	channel, _ = h.db.GetNotificationChannel(t.Context(), id)
	if channel.Enabled || channel.Scoped || channel.Events != "" {
		t.Fatalf("the channel is %+v; a PUT is the whole channel but enabled is kept when absent", channel)
	}
	if config := h.storedConfig(id); config["bot_token"] != fakeBotToken || config["chat_id"] != "-200" {
		t.Fatalf("a PUT with no settings changed them: %v", config)
	}

	// A new token replaces the old one.
	if status, body := h.do(acme, http.MethodPut, path, map[string]any{
		"config": map[string]string{"bot_token": "654321:another-fake-token", "chat_id": "-200"},
	}); status != http.StatusOK {
		t.Fatalf("replace the token: %d\n%s", status, body)
	}
	if config := h.storedConfig(id); config["bot_token"] != "654321:another-fake-token" {
		t.Fatalf("the new token was not stored: %v", config)
	}
}

func TestAChangeIsCheckedBeforeItIsStored(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	globex := h.newTenant("globex")
	id := h.createChannel(acme, map[string]any{
		"kind": "telegram", "config": map[string]string{"bot_token": fakeBotToken, "chat_id": "-100"},
	})
	path := "/api/teams/" + acme.team.ID + "/notifications/" + id

	for what, change := range map[string]map[string]any{
		"an emptied chat id":        {"config": map[string]string{"chat_id": ""}},
		"a limit to no project":     {"projects": []string{}},
		"another team's project":    {"projects": []string{globex.project.ID}},
		"a project that is not one": {"projects": []string{"prj_00000000000000000000"}},
		"a different kind":          {"kind": "discord"},
	} {
		status, body := h.do(acme, http.MethodPut, path, change)
		if status < 400 || status >= 500 {
			t.Errorf("%s was answered %d, want a refusal.\n%s", what, status, truncate(body, 200))
		}
	}
	channel, _ := h.db.GetNotificationChannel(t.Context(), id)
	config := h.storedConfig(id)
	if channel.Kind != "telegram" || channel.Scoped || config["chat_id"] != "-100" {
		t.Fatalf("a refused change was stored: %+v %v", channel, config)
	}

	// Creating a channel limited to another team's project is refused too.
	status, body := h.do(acme, http.MethodPost, "/api/teams/"+acme.team.ID+"/notifications", map[string]any{
		"kind": "webhook", "config": map[string]string{"url": "https://hooks.example.test/x"},
		"projects": []string{globex.project.ID},
	})
	if status != http.StatusNotFound {
		t.Fatalf("a channel limited to another team's project answered %d.\n%s", status, truncate(body, 200))
	}
}

// Only an administrator of the team reads a channel's settings or changes
// them. A member limited to projects is refused like any team-wide route; a
// viewer sees the list and not the settings; another team is told there is no
// such thing.
func TestOnlyAnAdministratorReadsOrChangesAChannel(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	globex := h.newTenant("globex")
	id := h.createChannel(acme, map[string]any{
		"kind": "telegram", "name": "ops", "config": map[string]string{"bot_token": fakeBotToken, "chat_id": "-100"},
	})
	path := "/api/teams/" + acme.team.ID + "/notifications/" + id
	change := map[string]any{"name": "taken", "config": map[string]string{"chat_id": "-999"}}

	for _, who := range []struct {
		name   string
		as     tenant
		status int
	}{
		{"a viewer", h.newMember(acme, "viewer", store.RoleViewer), http.StatusForbidden},
		{"a member", h.newMember(acme, "member", store.RoleMember), http.StatusForbidden},
		{"a member limited to the project", h.limitedMember(acme, "contractor", store.RoleMember, acme.project.ID), http.StatusForbidden},
		{"another team", globex, http.StatusNotFound},
	} {
		if status, body := h.do(who.as, http.MethodGet, path, nil); status != who.status {
			t.Errorf("%s reading the channel was answered %d, want %d.\n%s", who.name, status, who.status, truncate(body, 200))
		}
		if status, body := h.do(who.as, http.MethodPut, path, change); status != who.status {
			t.Errorf("%s changing the channel was answered %d, want %d.\n%s", who.name, status, who.status, truncate(body, 200))
		}
	}
	// Globex through its own team, naming acme's channel.
	if status, _ := h.do(globex, http.MethodPut, "/api/teams/"+globex.team.ID+"/notifications/"+id, change); status != http.StatusNotFound {
		t.Errorf("another team changing the channel through its own team was answered %d, want 404", status)
	}

	channel, _ := h.db.GetNotificationChannel(t.Context(), id)
	if channel.Name != "ops" || h.storedConfig(id)["chat_id"] != "-100" {
		t.Fatalf("somebody who may not changed the channel: %+v", channel)
	}

	admin := h.newMember(acme, "admin", store.RoleAdmin)
	if status, body := h.do(admin, http.MethodPut, path, change); status != http.StatusOK {
		t.Fatalf("an administrator changing the channel was answered %d.\n%s", status, truncate(body, 200))
	}
}
