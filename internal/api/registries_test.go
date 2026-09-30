package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"skifity/internal/registry"
	"skifity/internal/store"
)

// A team's registry credentials: asked of the registry before they are kept,
// sealed once kept, never sent back, and an administrator's to change.

func stubRegistryLogin(t *testing.T, answer func(host, username, password string) error) {
	t.Helper()
	previous := checkRegistryLogin
	checkRegistryLogin = func(_ context.Context, host, username, password string) error {
		return answer(host, username, password)
	}
	t.Cleanup(func() { checkRegistryLogin = previous })
}

func TestARegistryLoginIsCheckedSealedAndNeverSentBack(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	stubRegistryLogin(t, func(host, username, password string) error {
		if host == "ghcr.io" && username == "acme-bot" && password == "ghp_not_a_real_token" {
			return nil
		}
		return registry.ErrLoginRefused
	})
	path := "/api/teams/" + acme.team.ID + "/registries"

	status, body := h.do(acme, http.MethodPut, path, map[string]any{
		"host": "https://ghcr.io/", "username": "acme-bot", "password": "ghp_not_a_real_token",
	})
	if status != http.StatusOK || !strings.Contains(body, `"checked":true`) || !strings.Contains(body, `"host":"ghcr.io"`) {
		t.Fatalf("saving good credentials answered %d: %s", status, body)
	}
	status, body = h.do(acme, http.MethodPut, path, map[string]any{
		"host": "ghcr.io", "username": "acme-bot", "password": "typo",
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "registry.login_refused") {
		t.Fatalf("wrong credentials answered %d: %s", status, body)
	}

	rows, err := h.db.ListRegistryCredentials(t.Context(), acme.team.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("stored %+v (%v)", rows, err)
	}
	if strings.Contains(rows[0].SealedPassword, "ghp_") {
		t.Error("the password is stored in the clear")
	}
	if password, err := h.keyring.Open(rows[0].SealedPassword, store.RegistryContext(acme.team.ID, "ghcr.io")); err != nil ||
		string(password) != "ghp_not_a_real_token" {
		t.Errorf("the refused attempt replaced the good password: %q %v", password, err)
	}

	_, body = h.do(acme, http.MethodGet, path, nil)
	if strings.Contains(body, "ghp_") || strings.Contains(body, "password") {
		t.Errorf("the list carries the password: %s", body)
	}
}

// A registry on a private network is one the guarded client will not dial.
// That is not a reason to refuse it: it is saved, and the answer says it was
// not checked.
func TestARegistryThePanelCannotReachIsSavedUnchecked(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	stubRegistryLogin(t, func(string, string, string) error { return errors.New("dial refused: private address") })
	status, body := h.do(acme, http.MethodPut, "/api/teams/"+acme.team.ID+"/registries", map[string]any{
		"host": "registry.acme.internal:5000", "username": "ci", "password": "not-a-real-password",
	})
	if status != http.StatusOK || !strings.Contains(body, `"checked":false`) || !strings.Contains(body, "private address") {
		t.Fatalf("an unreachable registry answered %d: %s", status, body)
	}
}

func TestOnlyAnAdministratorChangesTheTeamsRegistries(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	globex := h.newTenant("globex")
	stubRegistryLogin(t, func(string, string, string) error { return nil })
	path := "/api/teams/" + acme.team.ID + "/registries"
	body := map[string]any{"host": "docker.io", "username": "acme", "password": "dckr_pat_not_real"}

	member := h.newMember(acme, "dev", store.RoleMember)
	if status, _ := h.do(member, http.MethodPut, path, body); status != http.StatusForbidden {
		t.Errorf("a member saved the team's registry credentials: %d", status)
	}
	if status, _ := h.do(globex, http.MethodPut, path, body); status != http.StatusNotFound {
		t.Errorf("another team saved credentials here: %d", status)
	}
	if status, _ := h.do(globex, http.MethodGet, path, nil); status != http.StatusNotFound {
		t.Errorf("another team listed them: %d", status)
	}
	if status, answer := h.do(acme, http.MethodPut, path, body); status != http.StatusOK {
		t.Fatalf("the owner could not save: %d %s", status, answer)
	}
	if status, answer := h.do(member, http.MethodGet, path, nil); status != http.StatusOK || !strings.Contains(answer, "docker.io") {
		t.Errorf("a member cannot see which registries the team pulls from: %d %s", status, answer)
	}
	rows, _ := h.db.ListRegistryCredentials(t.Context(), acme.team.ID)
	if status, _ := h.do(member, http.MethodDelete, path+"/"+rows[0].ID, nil); status != http.StatusForbidden {
		t.Errorf("a member removed them: %d", status)
	}
	if status, _ := h.do(acme, http.MethodDelete, path+"/"+rows[0].ID, nil); status != http.StatusOK {
		t.Errorf("the owner could not remove them: %d", status)
	}
}
