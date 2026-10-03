package provision

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"sigs.k8s.io/yaml"

	"skifity/internal/api"
	"skifity/internal/cloud"
	"skifity/internal/cloud/cloudtest"
	"skifity/internal/crypto"
	"skifity/internal/errdoc"
	"skifity/internal/sshx"
	"skifity/internal/store"
)

// Creating a server at Hetzner Cloud, end to end, with neither Hetzner nor a
// real machine: cloudtest's fake answers the API, and sshx's in-process SSH
// server is the machine the fake "creates". The part under test is the part
// that matters: the host key is the panel's before the first connection, a
// machine presenting any other is refused before anything is sent to it, and
// only a machine the panel created is ever deleted.

// cloudRig is a provisioner wired to the fake, and the SSH server standing in
// for the machine it orders.
type cloudRig struct {
	p        *Provisioner
	db       *store.DB
	keyring  *crypto.Keyring
	teamID   string
	fake     *cloudtest.Hetzner
	ocean    *cloudtest.DigitalOcean
	machine  *sshx.TestServer
	provider store.CloudProvider

	mu sync.Mutex
	// atCreate is what the panel had pinned, and what the machine had been
	// asked, at the moment the order reached the provider.
	pinnedAtCreate   string
	attemptsAtCreate int
	userData         string
}

// newCloudRig builds the rig. present decides whether the machine takes the
// host key from its user data, as cloud-init does; false is something else
// answering at the address.
func newCloudRig(t *testing.T, present bool) *cloudRig {
	t.Helper()
	return newCloudRigAt(t, present, cloud.KindHetzner)
}

// newCloudRigAt builds the rig against one provider's fake.
func newCloudRigAt(t *testing.T, present bool, kind string) *cloudRig {
	t.Helper()
	p, db, keyring, teamID := testHarness(t)
	r := &cloudRig{p: p, db: db, keyring: keyring, teamID: teamID}

	machine, err := sshx.NewTestServer(sshx.TestServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { machine.Close() })
	machine.Respond("/etc/os-release", realPreflightOutput, 0)
	machine.Respond("allow_public", "firewall_configured=yes", 0)
	machine.Respond("get.k3s.io", "==> Installing Kubernetes (k3s)\nk3s is running", 0)
	machine.Respond("reachable", "reachable=yes", 0)
	r.machine = machine

	// The machine's own key, the one the rotation makes. The machine prints
	// its public half and presents it from the next connection on.
	own, err := sshx.GenerateKeyPair("")
	if err != nil {
		t.Fatal(err)
	}
	machine.Respond("ssh-keygen", "host_key="+strings.TrimSpace(own.PublicKey), 0)
	machine.OnCommand("ssh_host_ed25519_key", func() {
		if err := machine.SetHostKey(own.PrivateKey); err != nil {
			t.Errorf("rotate the machine's host key: %v", err)
		}
	})

	host, port := machine.Addr()
	onCreate := func(userData, serverID string, publicKeys []string) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.userData = userData
		if server, err := db.GetServer(t.Context(), serverID); err == nil {
			r.pinnedAtCreate = server.HostKey
		}
		r.attemptsAtCreate = len(machine.AuthAttempts())
		for _, key := range publicKeys {
			machine.Authorize(key)
		}
		if present {
			// cloud-init: write the host key the user data carries.
			var config struct {
				SSHKeys map[string]string `json:"ssh_keys"`
			}
			if err := yaml.Unmarshal([]byte(userData), &config); err != nil {
				t.Errorf("the user data is not YAML: %v", err)
				return
			}
			if err := machine.SetHostKey(config.SSHKeys["ed25519_private"]); err != nil {
				t.Errorf("the machine could not use the host key it was given: %v", err)
			}
		}
	}
	switch kind {
	case cloud.KindDigitalOcean:
		ocean := cloudtest.NewDigitalOcean(t)
		ocean.IPv4 = host
		ocean.OnCreate = func(d cloudtest.Droplet) { onCreate(d.UserData, serverIDOfTags(d.Tags), d.PublicKeys) }
		r.ocean = ocean
		p.cloud = ocean.Opener()
	default:
		fake := cloudtest.New(t)
		fake.IPv4 = host
		fake.OnCreate = func(s cloudtest.Server) { onCreate(s.UserData, serverIDOf(s.Labels), s.PublicKeys) }
		r.fake = fake
		p.cloud = fake.Opener()
	}
	p.cloudTiming = cloudTiming{
		Poll: 10 * time.Millisecond, Boot: 3 * time.Second, SSH: 400 * time.Millisecond,
		Gone: 200 * time.Millisecond, SSHPort: port, DialTimeout: 2 * time.Second,
	}

	r.provider = store.CloudProvider{ID: store.NewCloudProviderID(), TeamID: teamID, Kind: kind, Name: cloud.Title(kind)}
	sealed, err := keyring.Seal([]byte(cloudtest.Token), store.CloudProviderContext(r.provider.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateCloudProvider(t.Context(), &r.provider, sealed); err != nil {
		t.Fatal(err)
	}
	return r
}

func serverIDOf(labels map[string]string) string { return labels["skifity.com/server-id"] }

// serverIDOfTags is serverIDOf for a droplet, whose labels are tags.
func serverIDOfTags(tags []string) string {
	for _, tag := range tags {
		if id, ok := strings.CutPrefix(tag, "skifity:server-id:"); ok {
			return id
		}
	}
	return ""
}

func (r *cloudRig) create(t *testing.T, name string) (store.Operation, store.Operation) {
	t.Helper()
	return r.createAt(t, name, "fsn1", "cx22")
}

func (r *cloudRig) createAt(t *testing.T, name, location, serverType string) (store.Operation, store.Operation) {
	t.Helper()
	op, err := r.p.CreateCloudServer(t.Context(), api.CreateCloudServerRequest{
		TeamID: r.teamID, ProviderID: r.provider.ID, Name: name, Location: location,
		ServerType: serverType, Image: "ubuntu-24.04", SSHAccess: store.SSHFromAnywhere, Arch: cloud.ArchAMD64,
	})
	if err != nil {
		t.Fatalf("CreateCloudServer: %v", err)
	}
	return op, waitForOperation(t, r.db, op.ID)
}

func TestACreatedServerIsPinnedBeforeItIsFirstReached(t *testing.T) {
	r := newCloudRig(t, true)
	_, finished := r.create(t, "web-1")
	if finished.Status != store.OpSucceeded {
		t.Fatalf("the operation failed: %s %s\nsteps: %+v", finished.ErrorCode, finished.ErrorMsg, finished.Steps)
	}
	if len(finished.Steps) != len(cloudServerSteps) {
		t.Fatalf("%d steps ran, want %d", len(finished.Steps), len(cloudServerSteps))
	}
	for i, step := range finished.Steps {
		if step.Key != cloudServerSteps[i] || step.Status != store.StepSucceeded {
			t.Errorf("step %d is %s %s, want %s succeeded: %s", i, step.Key, step.Status, cloudServerSteps[i], step.Detail)
		}
	}

	// At the moment the order reached the provider, the panel had already
	// pinned the key it put in the user data, and nothing had connected.
	r.mu.Lock()
	pinned, attempts, userData := r.pinnedAtCreate, r.attemptsAtCreate, r.userData
	r.mu.Unlock()
	var config struct {
		SSHKeys map[string]string `json:"ssh_keys"`
	}
	if err := yaml.Unmarshal([]byte(userData), &config); err != nil {
		t.Fatal(err)
	}
	bootstrap, _, err := sshx.PublicKeyFingerprint(config.SSHKeys["ed25519_public"])
	if err != nil {
		t.Fatal(err)
	}
	if pinned == "" || pinned != bootstrap {
		t.Fatalf("pinned at create: %q; the user data's key is %q", pinned, bootstrap)
	}
	if attempts != 0 {
		t.Fatalf("%d sign-ins were attempted before the machine existed", attempts)
	}

	// Then replaced: the key in the user data is readable by the provider and
	// the metadata service, so it is not the one the server ends up pinned to.
	server, err := r.db.GetServer(t.Context(), finished.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	if server.HostKey == "" || server.HostKey == bootstrap {
		t.Errorf("the pinned host key is %q, which is the bootstrap one or none", server.HostKey)
	}
	created, err := r.db.GetCloudServer(t.Context(), server.ID)
	if err != nil || !created.HostKeyRotated {
		t.Errorf("the rotation was not recorded: %+v %v", created, err)
	}
	if server.Status != store.ServerReady || server.Host != "127.0.0.1" || server.SSHKeyEnc == "" {
		t.Errorf("the server is %s at %s, with a key: %t", server.Status, server.Host, server.SSHKeyEnc != "")
	}
	for _, method := range r.machine.AuthAttempts() {
		if method != "publickey" {
			t.Errorf("the panel signed in with %s; a created server has no password", method)
		}
	}
	if r.machine.Ran("authorized_keys") {
		t.Error("the key was installed over SSH; the provider put it on the machine")
	}

	// What was ordered, and what was left at the provider.
	machines := r.fake.Servers()
	if len(machines) != 1 {
		t.Fatalf("%d machines at the provider", len(machines))
	}
	machine := machines[0]
	if machine.Name != "web-1" || machine.ServerType != "cx22" || machine.Location != "fsn1" ||
		machine.Image != "ubuntu-24.04" || serverIDOf(machine.Labels) != server.ID || len(machine.Firewalls) != 1 {
		t.Errorf("ordered %+v", machine)
	}
	if created.MachineID != strconv.FormatInt(machine.ID, 10) {
		t.Errorf("recorded machine %q, ordered %d", created.MachineID, machine.ID)
	}
	if keys := r.fake.Keys(); len(keys) != 0 {
		t.Errorf("the imported key was left at the provider: %+v", keys)
	}
	firewalls := r.fake.Firewalls()
	if len(firewalls) != 1 {
		t.Fatalf("%d firewalls", len(firewalls))
	}
	assertFirewall(t, firewalls[0], false)
}

// The same flow at DigitalOcean: ordered with the image's own name and the
// key's id, behind its firewall from the first boot through a tag of the
// firewall's own, pinned before it is reached, and joined the same way.
func TestAServerAtDigitalOceanJoinsTheSameWay(t *testing.T) {
	r := newCloudRigAt(t, true, cloud.KindDigitalOcean)
	_, finished := r.createAt(t, "web-1", "fra1", "s-1vcpu-1gb")
	if finished.Status != store.OpSucceeded {
		t.Fatalf("the operation failed: %s %s\nsteps: %+v", finished.ErrorCode, finished.ErrorMsg, finished.Steps)
	}
	server, err := r.db.GetServer(t.Context(), finished.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	pinned, attempts := r.pinnedAtCreate, r.attemptsAtCreate
	r.mu.Unlock()
	if pinned == "" || attempts != 0 {
		t.Fatalf("pinned %q and %d sign-ins at the moment the droplet was ordered", pinned, attempts)
	}
	created, err := r.db.GetCloudServer(t.Context(), server.ID)
	if err != nil || !created.HostKeyRotated || server.HostKey == pinned {
		t.Errorf("the bootstrap host key was not replaced: %+v %v", created, err)
	}
	if server.Status != store.ServerReady {
		t.Errorf("the server is %s", server.Status)
	}

	droplets := r.ocean.Droplets()
	if len(droplets) != 1 {
		t.Fatalf("%d droplets", len(droplets))
	}
	droplet := droplets[0]
	if droplet.Image != "ubuntu-24-04-x64" || droplet.Region != "fra1" || droplet.Size != "s-1vcpu-1gb" ||
		serverIDOfTags(droplet.Tags) != server.ID || created.MachineID != strconv.FormatInt(droplet.ID, 10) {
		t.Errorf("ordered %+v, recorded %q", droplet, created.MachineID)
	}
	firewalls := r.ocean.Firewalls()
	if len(firewalls) != 1 || len(firewalls[0].Tags) != 1 || !slices.Contains(droplet.Tags, firewalls[0].Tags[0]) {
		t.Fatalf("the droplet is not behind its firewall from the first boot: %+v, tags %v", firewalls, droplet.Tags)
	}
	open := map[string]bool{}
	for _, rule := range firewalls[0].InboundRules {
		open[rule["protocol"].(string)+"/"+rule["ports"].(string)] = true
	}
	for _, port := range []string{"tcp/22", "tcp/80", "tcp/443", "tcp/6443", "tcp/10250", "udp/8472", "udp/51820", "udp/51821"} {
		if !open[port] {
			t.Errorf("%s is not open", port)
		}
	}
	if len(firewalls[0].OutboundRules) == 0 {
		t.Error("the firewall lets nothing out, so the node cannot download k3s")
	}
	if keys := r.ocean.Keys(); len(keys) != 0 {
		t.Errorf("the imported key was left at DigitalOcean: %+v", keys)
	}
}

// assertFirewall checks one machine's firewall against what the cluster needs.
func assertFirewall(t *testing.T, fw cloudtest.Firewall, controlPlane bool) {
	t.Helper()
	open := map[string][]string{}
	for _, rule := range fw.Rules {
		if rule.Direction != "in" {
			t.Errorf("an outbound rule: %+v", rule)
		}
		open[rule.Protocol+"/"+rule.Port] = rule.SourceIPs
	}
	anywhere := strings.Join(cloud.Anywhere, ",")
	for _, port := range []string{"tcp/22", "tcp/80", "tcp/443"} {
		if strings.Join(open[port], ",") != anywhere {
			t.Errorf("%s is open to %v, want anywhere", port, open[port])
		}
	}
	for _, port := range []string{"tcp/6443", "tcp/10250", "udp/8472", "udp/51820", "udp/51821"} {
		sources, ok := open[port]
		if !ok {
			t.Errorf("%s is not open to the cluster", port)
		}
		for _, source := range sources {
			if source == "0.0.0.0/0" || source == "::/0" {
				t.Errorf("%s, a cluster port, is open to the internet", port)
			}
		}
	}
	for _, port := range []string{"tcp/2379", "tcp/2380"} {
		if _, ok := open[port]; ok != controlPlane {
			t.Errorf("etcd's %s open: %t, on a control plane: %t", port, ok, controlPlane)
		}
	}
	if len(open) != 8+map[bool]int{true: 2, false: 0}[controlPlane] {
		t.Errorf("the firewall lets in more than the cluster needs: %v", open)
	}
}

// Something at the address that is not the machine the panel made is never
// signed in to: the host key is checked before any credential is offered.
func TestAMachineWithAnotherHostKeyIsRefused(t *testing.T) {
	r := newCloudRig(t, false)
	_, finished := r.create(t, "web-1")
	if finished.Status != store.OpFailed || finished.ErrorCode != "cloud.host_key_mismatch" {
		t.Fatalf("the operation is %s %s, want failed with cloud.host_key_mismatch", finished.Status, finished.ErrorCode)
	}
	if attempts := r.machine.AuthAttempts(); len(attempts) != 0 {
		t.Errorf("the panel offered credentials to a machine it did not recognise: %v", attempts)
	}
	if commands := r.machine.Commands(); len(commands) != 0 {
		t.Errorf("commands ran on a machine the panel did not recognise: %v", commands)
	}
	for _, step := range finished.Steps {
		if step.Key == StepCloudSSH && step.Status != store.StepFailed {
			t.Errorf("the SSH step is %s", step.Status)
		}
	}
}

// Removing a created server can delete its machine, and nothing else.
func TestOnlyTheMachineThePanelCreatedIsDeleted(t *testing.T) {
	r := newCloudRig(t, true)
	stranger := r.fake.AddServer(cloudtest.Server{Name: "somebody-elses", IPv4: "198.51.100.99"})
	_, finished := r.create(t, "web-1")
	if finished.Status != store.OpSucceeded {
		t.Fatalf("create: %s %s", finished.ErrorCode, finished.ErrorMsg)
	}
	serverID := finished.TargetID

	op, err := r.p.RemoveServer(t.Context(), serverID, api.RemoveServerOptions{DeleteMachine: true})
	if err != nil {
		t.Fatal(err)
	}
	removed := waitForOperation(t, r.db, op.ID)
	if removed.Status != store.OpSucceeded {
		t.Fatalf("remove: %s %s %+v", removed.ErrorCode, removed.ErrorMsg, removed.Steps)
	}
	if last := removed.Steps[len(removed.Steps)-1]; last.Key != StepCloudDelete || last.Status != store.StepSucceeded {
		t.Errorf("the last step is %+v", last)
	}
	machines := r.fake.Servers()
	if len(machines) != 1 || machines[0].ID != stranger {
		t.Errorf("after deleting, the provider has %+v; want only the machine the panel did not create", machines)
	}
	if firewalls := r.fake.Firewalls(); len(firewalls) != 0 {
		t.Errorf("the firewall was left: %+v", firewalls)
	}
	if _, err := r.db.GetServer(t.Context(), serverID); err == nil {
		t.Error("the server is still recorded")
	}
	for _, request := range r.fake.Requests() {
		if request.Method == "DELETE" && request.Path == "/servers/"+strconv.FormatInt(stranger, 10) {
			t.Error("a machine the panel did not create was asked to be deleted")
		}
	}
}

// A machine whose labels no longer say it is this server's is left alone,
// whatever id the record holds.
func TestAMachineThatDoesNotCarryThePanelsLabelIsLeftAlone(t *testing.T) {
	r := newCloudRig(t, true)
	_, finished := r.create(t, "web-1")
	if finished.Status != store.OpSucceeded {
		t.Fatalf("create: %s %s", finished.ErrorCode, finished.ErrorMsg)
	}
	machine := r.fake.Servers()[0]
	r.fake.SetLabels(machine.ID, map[string]string{"owner": "somebody-else"})

	op, err := r.p.RemoveServer(t.Context(), finished.TargetID, api.RemoveServerOptions{DeleteMachine: true})
	if err != nil {
		t.Fatal(err)
	}
	removed := waitForOperation(t, r.db, op.ID)
	if removed.Status != store.OpFailed || removed.ErrorCode != "cloud.machine_not_ours" {
		t.Fatalf("remove is %s %s, want failed with cloud.machine_not_ours", removed.Status, removed.ErrorCode)
	}
	if len(r.fake.Servers()) != 1 {
		t.Error("the machine was deleted")
	}
	// The server stays recorded, so removing it again can be tried.
	if _, err := r.db.GetServer(t.Context(), finished.TargetID); err != nil {
		t.Errorf("the server's record went with a failed delete: %v", err)
	}
}

// A server added with an address has no machine the panel may delete.
func TestAServerThePanelDidNotCreateHasNoMachineToDelete(t *testing.T) {
	p, db, _, teamID := testHarness(t)
	server := store.Server{TeamID: teamID, Name: "own-box", Host: "198.51.100.20", SSHPort: 22, SSHUser: "root", Status: store.ServerReady}
	if err := db.CreateServer(t.Context(), &server); err != nil {
		t.Fatal(err)
	}
	_, err := p.RemoveServer(t.Context(), server.ID, api.RemoveServerOptions{DeleteMachine: true})
	if code := problemCode(err); code != "cloud.not_created" {
		t.Fatalf("answered %v (%s), want cloud.not_created", err, code)
	}
	if op, err := db.LatestOperation(t.Context(), "server", server.ID); err == nil {
		t.Errorf("an operation was started: %+v", op)
	}
}

// A retry after a lost answer finds the machine by its label rather than
// ordering a second one.
func TestARetryDoesNotOrderASecondMachine(t *testing.T) {
	r := newCloudRig(t, true)
	r.p.cloudTiming.SSH = 50 * time.Millisecond
	// The machine answers nothing the first time round: the SSH step fails.
	r.machine.Close()
	_, finished := r.create(t, "web-1")
	if finished.Status != store.OpFailed || finished.ErrorCode != "cloud.ssh_timeout" {
		t.Fatalf("the first attempt is %s %s, want failed with cloud.ssh_timeout", finished.Status, finished.ErrorCode)
	}
	// Forget the id, as a crash between the order and the write would.
	created, err := r.db.GetCloudServer(t.Context(), finished.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	created.MachineID = ""
	if err := r.db.UpdateCloudServer(t.Context(), &created); err != nil {
		t.Fatal(err)
	}
	if err := r.db.ResetStepsFrom(t.Context(), finished.ID, StepCloudCreate); err != nil {
		t.Fatal(err)
	}

	op, err := r.p.RetryServer(t.Context(), finished.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	waitForOperation(t, r.db, op.ID)
	if machines := r.fake.Servers(); len(machines) != 1 {
		t.Fatalf("the retry ordered another machine: %d at the provider", len(machines))
	}
	again, err := r.db.GetCloudServer(t.Context(), finished.TargetID)
	if err != nil || again.MachineID != strconv.FormatInt(r.fake.Servers()[0].ID, 10) {
		t.Errorf("the machine was not found again: %+v %v", again, err)
	}
}

func problemCode(err error) string {
	var problem *errdoc.Problem
	if errors.As(err, &problem) {
		return problem.Code
	}
	return ""
}
