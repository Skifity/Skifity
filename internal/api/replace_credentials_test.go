package api

import (
	"net/http"
	"testing"

	"skifity/internal/cloud/cloudtest"
	"skifity/internal/dnsprov"
	"skifity/internal/store"
)

// Replacing the credential of a Git, cloud or DNS connection is an
// administrator's of the team the connection is in. Another team's
// administrator is told it does not exist; a member and a viewer of the team
// are refused, and nothing changes for any of them.
func TestReplacingACredentialIsTheTeamAdministrators(t *testing.T) {
	h := newHarness(t)
	h.withCloud(cloudtest.New(t), &recordingProvisioner{})
	h.withDNS(dnsprov.Cloudflare, "example.com")
	acme := h.newTenant("acme")
	other := h.newTenant("other")
	member := h.newMember(acme, "dev", store.RoleMember)
	viewer := h.newMember(acme, "viewer", store.RoleViewer)

	git := h.connection(acme, "acme-github", "")
	_, cloudView, _ := h.addConnection(acme, cloudtest.Token)
	dnsID := connectDNS(t, h, acme)

	routes := []struct {
		path string
		body map[string]string
	}{
		{"/git-sources/" + git.ID, map[string]string{"token": "ghp_faketokenfortests7777777777777777"}},
		{"/cloud-providers/" + cloudView.ID, map[string]string{"token": cloudtest.RotatedToken}},
		{"/dns-providers/" + dnsID, map[string]string{"token": fakeDNSToken}},
	}
	for _, route := range routes {
		if status, body := h.do(other, http.MethodPatch, "/api/teams/"+other.team.ID+route.path, route.body); status != http.StatusNotFound {
			t.Errorf("another team's administrator: PATCH %s answered %d, want 404\n%s", route.path, status, body)
		}
		for _, as := range []tenant{member, viewer} {
			if status, _ := h.do(as, http.MethodPatch, "/api/teams/"+acme.team.ID+route.path, route.body); status != http.StatusForbidden {
				t.Errorf("a %s: PATCH %s answered %d, want 403", as.user.Name, route.path, status)
			}
		}
	}

	if after, _ := h.db.GetGitSource(t.Context(), git.ID); after.ConfigEnc != git.ConfigEnc {
		t.Error("the Git connection was changed")
	}
	if row, _ := h.db.GetCloudProvider(t.Context(), acme.team.ID, cloudView.ID); row.TokenHint != cloudView.TokenHint {
		t.Error("the cloud connection was changed")
	}
	for _, action := range []string{"git_source.updated", "cloud_provider.token_replaced", "dns_provider.credentials_replaced"} {
		if events, _ := h.db.ListAudit(t.Context(), acme.team.ID, action, "", 5); len(events) != 0 {
			t.Errorf("%d %s events for refused requests", len(events), action)
		}
	}
}
