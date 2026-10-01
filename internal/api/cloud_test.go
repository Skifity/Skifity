package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"skifity/internal/cloud"
	"skifity/internal/cloud/cloudtest"
	"skifity/internal/events"
	"skifity/internal/store"
)

// A team's cloud connections and creating a server with one, through the
// router the panel serves, against cloudtest's fake Hetzner Cloud.

// recordingProvisioner is a Provisioner that remembers what it was asked and
// does nothing: what happens after the API is internal/provision's to test.
type recordingProvisioner struct {
	mu      sync.Mutex
	created []CreateCloudServerRequest
	removed []RemoveServerOptions
}

func (p *recordingProvisioner) AddServer(context.Context, AddServerRequest) (store.Operation, error) {
	return store.Operation{}, nil
}

func (p *recordingProvisioner) RetryServer(context.Context, string) (store.Operation, error) {
	return store.Operation{}, nil
}

func (p *recordingProvisioner) CreateCloudServer(_ context.Context, req CreateCloudServerRequest) (store.Operation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.created = append(p.created, req)
	return store.Operation{ID: "op_1", Kind: "server.create", TargetType: "server", TargetID: "srv_new"}, nil
}

func (p *recordingProvisioner) RemoveServer(_ context.Context, serverID string, opts RemoveServerOptions) (store.Operation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.removed = append(p.removed, opts)
	return store.Operation{ID: "op_2", Kind: "server.remove", TargetType: "server", TargetID: serverID}, nil
}

func (p *recordingProvisioner) PromoteServer(context.Context, string) (store.Operation, error) {
	return store.Operation{}, nil
}

func (p *recordingProvisioner) Cancel(context.Context, string) error { return nil }

func (p *recordingProvisioner) AuditServer(context.Context, string) (HardeningReport, error) {
	return HardeningReport{}, nil
}

func (p *recordingProvisioner) TurnOffSSHPasswords(context.Context, string) (HardeningReport, error) {
	return HardeningReport{}, nil
}

// withCloud rebuilds the harness's server around the fake and a provisioner.
func (h *harness) withCloud(fake *cloudtest.Hetzner, provisioner Provisioner) {
	h.t.Helper()
	h.api = New(Options{
		DB: h.db, Keyring: h.keyring, Auth: h.auth,
		Hub: events.NewHub(16), Logger: slog.New(slog.DiscardHandler),
		Provisioner: provisioner, Cloud: fake.Opener(),
	})
	h.server.Config.Handler = h.api
}

func (h *harness) addConnection(as tenant, token string) (int, cloudProviderView, string) {
	h.t.Helper()
	status, body := h.do(as, http.MethodPost, "/api/teams/"+as.team.ID+"/cloud-providers",
		map[string]string{"kind": "hetzner", "name": "Hetzner", "token": token})
	var view cloudProviderView
	_ = json.Unmarshal([]byte(body), &view)
	return status, view, body
}

func TestACloudTokenIsCheckedSealedAndNeverShown(t *testing.T) {
	h := newHarness(t)
	fake := cloudtest.New(t)
	h.withCloud(fake, &recordingProvisioner{})
	acme := h.newTenant("acme")

	if status, _, body := h.addConnection(acme, cloudtest.ReadOnlyToken); status != http.StatusBadRequest ||
		!strings.Contains(body, "cloud.token_read_only") {
		t.Fatalf("a read-only token answered %d: %s", status, body)
	}
	if status, _, body := h.addConnection(acme, "wrong-token"); status != http.StatusBadRequest ||
		!strings.Contains(body, "cloud.token_invalid") {
		t.Fatalf("an unknown token answered %d: %s", status, body)
	}

	status, view, body := h.addConnection(acme, cloudtest.Token)
	if status != http.StatusCreated {
		t.Fatalf("adding a good token answered %d: %s", status, body)
	}
	if strings.Contains(body, cloudtest.Token) || view.TokenHint != cloudtest.Token[len(cloudtest.Token)-4:] {
		t.Errorf("the answer shows the token, or not its hint: %s", body)
	}
	status, body = h.do(acme, http.MethodGet, "/api/teams/"+acme.team.ID+"/cloud-providers", nil)
	if status != http.StatusOK || strings.Contains(body, cloudtest.Token) || !strings.Contains(body, view.ID) {
		t.Errorf("the list answered %d: %s", status, body)
	}

	// Sealed under the connection, so a copy on another row does not open.
	row, err := h.db.GetCloudProvider(t.Context(), acme.team.ID, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(row.SealedToken, cloudtest.Token) {
		t.Fatal("the token is stored in the clear")
	}
	if plain, err := h.keyring.Open(row.SealedToken, store.CloudProviderContext(view.ID)); err != nil || string(plain) != cloudtest.Token {
		t.Errorf("the token does not open under its own connection: %v", err)
	}
	if _, err := h.keyring.Open(row.SealedToken, store.CloudProviderContext("cld_other")); err == nil {
		t.Error("the token opens under another connection's context")
	}

	// And it is on the rotation's list.
	refs, err := h.db.ListSealedSecrets(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ref := range refs {
		found = found || ref.Table == "cloud_providers" && ref.ID == view.ID && ref.ContextPrefix == "cloud_provider"
	}
	if !found {
		t.Error("a master key rotation would not rewrap the cloud token")
	}

	status, body = h.do(acme, http.MethodPost, "/api/teams/"+acme.team.ID+"/cloud-providers/"+view.ID+"/test", nil)
	if status != http.StatusOK || !strings.Contains(body, "checked_at") {
		t.Errorf("testing the connection answered %d: %s", status, body)
	}
	status, body = h.do(acme, http.MethodGet, "/api/teams/"+acme.team.ID+"/cloud-providers/"+view.ID+"/options", nil)
	if status != http.StatusOK || !strings.Contains(body, `"cax11"`) || !strings.Contains(body, `"default_image":"ubuntu-24.04"`) {
		t.Errorf("the options answered %d: %s", status, truncate(body, 300))
	}
}

func TestOneTeamCannotUseAnothersCloudConnection(t *testing.T) {
	h := newHarness(t)
	fake := cloudtest.New(t)
	h.withCloud(fake, &recordingProvisioner{})
	acme := h.newTenant("acme")
	other := h.newTenant("other")
	_, theirs, _ := h.addConnection(other, cloudtest.Token)

	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/api/teams/" + acme.team.ID + "/cloud-providers/" + theirs.ID + "/test"},
		{http.MethodGet, "/api/teams/" + acme.team.ID + "/cloud-providers/" + theirs.ID + "/options"},
		{http.MethodDelete, "/api/teams/" + acme.team.ID + "/cloud-providers/" + theirs.ID},
		{http.MethodGet, "/api/teams/" + other.team.ID + "/cloud-providers"},
	} {
		if status, body := h.do(acme, route.method, route.path, nil); status != http.StatusNotFound {
			t.Errorf("%s %s answered %d, want 404: %s", route.method, route.path, status, truncate(body, 200))
		}
	}
}

func TestOnlyAPanelAdministratorCreatesAServer(t *testing.T) {
	h := newHarness(t)
	fake := cloudtest.New(t)
	provisioner := &recordingProvisioner{}
	h.withCloud(fake, provisioner)
	owner := h.newTenant("shop")
	admin := adminTenant(h, "ops")
	_, shopConnection, _ := h.addConnection(owner, cloudtest.Token)
	_, opsConnection, _ := h.addConnection(admin, cloudtest.Token)

	order := map[string]any{
		"provider_id": shopConnection.ID, "name": "web-1", "location": "fsn1", "server_type": "cax11",
	}
	status, body := h.do(owner, http.MethodPost, "/api/teams/"+owner.team.ID+"/servers/cloud", order)
	if status != http.StatusForbidden {
		t.Fatalf("a team owner who is not a panel administrator answered %d: %s", status, body)
	}

	order["provider_id"] = opsConnection.ID
	status, body = h.do(admin, http.MethodPost, "/api/teams/"+admin.team.ID+"/servers/cloud", order)
	if status != http.StatusAccepted {
		t.Fatalf("an administrator's order answered %d: %s", status, body)
	}
	if len(provisioner.created) != 1 {
		t.Fatalf("%d orders reached the provisioner", len(provisioner.created))
	}
	got := provisioner.created[0]
	if got.Arch != cloud.ArchARM64 || got.Image != cloud.DefaultImage || got.SSHAccess != store.SSHFromAnywhere ||
		got.TeamID != admin.team.ID || got.CreatedBy != admin.user.ID {
		t.Errorf("the provisioner was asked for %+v", got)
	}
	// Nothing reaches the provider but reading its catalogue: ordering is
	// the operation's.
	for _, request := range fake.Requests() {
		if request.Method == http.MethodPost && request.Path == "/servers" {
			t.Error("the API ordered a machine itself")
		}
	}
}

func TestAnOrderIsHeldToWhatTheProviderSells(t *testing.T) {
	h := newHarness(t)
	fake := cloudtest.New(t)
	provisioner := &recordingProvisioner{}
	h.withCloud(fake, provisioner)
	admin := adminTenant(h, "ops")
	_, connection, _ := h.addConnection(admin, cloudtest.Token)
	h.node(admin, "taken")

	for name, order := range map[string]map[string]any{
		"a name that is not a hostname": {"name": "Web_1", "location": "fsn1", "server_type": "cx22"},
		"a type not sold there":         {"name": "web-1", "location": "hel1", "server_type": "cax11"},
		"a type nobody sells":           {"name": "web-1", "location": "fsn1", "server_type": "cx9000"},
		"a deprecated type":             {"name": "web-1", "location": "fsn1", "server_type": "cx11"},
		"an image not offered":          {"name": "web-1", "location": "fsn1", "server_type": "cx22", "image": "fedora-40"},
		"a location nobody has":         {"name": "web-1", "location": "mars1", "server_type": "cx22"},
		"SSH limited without a cluster": {"name": "web-1", "location": "fsn1", "server_type": "cx22", "ssh_access": "cluster"},
		"a name the cluster has":        {"name": "taken", "location": "fsn1", "server_type": "cx22"},
	} {
		order["provider_id"] = connection.ID
		status, body := h.do(admin, http.MethodPost, "/api/teams/"+admin.team.ID+"/servers/cloud", order)
		if status < 400 || status >= 500 {
			t.Errorf("%s answered %d: %s", name, status, truncate(body, 200))
		}
	}
	if len(provisioner.created) != 0 {
		t.Errorf("%d invalid orders reached the provisioner", len(provisioner.created))
	}
}

func TestAConnectionServersWereCreatedWithIsKept(t *testing.T) {
	h := newHarness(t)
	fake := cloudtest.New(t)
	h.withCloud(fake, &recordingProvisioner{})
	admin := adminTenant(h, "ops")
	_, connection, _ := h.addConnection(admin, cloudtest.Token)
	server := h.node(admin, "web-1")
	if err := h.db.CreateCloudServer(t.Context(), &store.CloudServer{
		ServerID: server.ID, ProviderID: connection.ID, Location: "fsn1", ServerType: "cx22", Image: "ubuntu-24.04",
	}); err != nil {
		t.Fatal(err)
	}

	path := "/api/teams/" + admin.team.ID + "/cloud-providers/" + connection.ID
	if status, body := h.do(admin, http.MethodDelete, path, nil); status != http.StatusConflict ||
		!strings.Contains(body, "cloud.provider_in_use") {
		t.Fatalf("removing a connection in use answered %d: %s", status, body)
	}
	if err := h.db.DeleteServer(t.Context(), server.ID); err != nil {
		t.Fatal(err)
	}
	if status, body := h.do(admin, http.MethodDelete, path, nil); status != http.StatusOK {
		t.Fatalf("removing an unused connection answered %d: %s", status, body)
	}
}

// Deleting the machine takes the name typed out, and a machine this panel
// ordered.
func TestDeletingTheMachineIsOnlyForOneThePanelCreated(t *testing.T) {
	h := newHarness(t)
	fake := cloudtest.New(t)
	provisioner := &recordingProvisioner{}
	h.withCloud(fake, provisioner)
	admin := adminTenant(h, "ops")
	_, connection, _ := h.addConnection(admin, cloudtest.Token)
	worker := func(name, host string) store.Server {
		record := store.Server{TeamID: admin.team.ID, Name: name, Host: host, SSHPort: 22, SSHUser: "root", Role: "worker", Status: "ready"}
		if err := h.db.CreateServer(t.Context(), &record); err != nil {
			t.Fatal(err)
		}
		return record
	}
	own := worker("own-box", "198.51.100.21")
	created := worker("web-1", "198.51.100.22")
	if err := h.db.CreateCloudServer(t.Context(), &store.CloudServer{
		ServerID: created.ID, ProviderID: connection.ID, MachineID: "101", Location: "fsn1", ServerType: "cx22", Image: "ubuntu-24.04",
	}); err != nil {
		t.Fatal(err)
	}

	status, body := h.do(admin, http.MethodDelete, "/api/servers/"+own.ID+"?delete_machine=true&confirm=own-box", nil)
	if status != http.StatusConflict || !strings.Contains(body, "cloud.not_created") {
		t.Errorf("deleting the machine of a server the panel did not create answered %d: %s", status, body)
	}
	status, body = h.do(admin, http.MethodDelete, "/api/servers/"+created.ID+"?delete_machine=true&confirm=web", nil)
	if status != http.StatusBadRequest {
		t.Errorf("a wrong name answered %d: %s", status, body)
	}
	if len(provisioner.removed) != 0 {
		t.Fatalf("%d removals started", len(provisioner.removed))
	}

	status, body = h.do(admin, http.MethodGet, "/api/servers/"+created.ID, nil)
	if status != http.StatusOK || !strings.Contains(body, `"cloud":{`) || !strings.Contains(body, `"provider_kind":"hetzner"`) {
		t.Errorf("the server does not say it was created at Hetzner: %d %s", status, truncate(body, 300))
	}

	status, body = h.do(admin, http.MethodDelete, "/api/servers/"+created.ID+"?delete_machine=true&confirm=web-1", nil)
	if status != http.StatusAccepted {
		t.Fatalf("deleting a created server's machine answered %d: %s", status, body)
	}
	if len(provisioner.removed) != 1 || !provisioner.removed[0].DeleteMachine {
		t.Errorf("the provisioner was asked for %+v", provisioner.removed)
	}
}
