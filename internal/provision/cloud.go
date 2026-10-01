package provision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"skifity/internal/api"
	"skifity/internal/cloud"
	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/sshx"
	"skifity/internal/store"
	"skifity/internal/version"
)

// Creating a server at a cloud provider, and deleting it again.
//
// Three steps come before the ordinary join, and they are all about getting
// to a machine the panel can trust:
//
//  1. cloud-create registers the panel's key for this server with the
//     provider, puts a firewall in front of it, generates the SSH host key the
//     machine will start with and pins it, and orders the machine with that
//     key in its user data.
//  2. cloud-boot waits for the machine to run, learns its address, and lets
//     that address through the firewalls in front of the other machines.
//  3. cloud-ssh waits for SSH to answer with the pinned key — never trusting
//     another, and sending no credentials to one — and then has the machine
//     replace that key with one it generates itself, because the first one
//     is readable by the provider and by anything on the machine. See
//     ADR-0025 and cloud/cloudinit.go.
//
// From there it is the same join as every other server: connect, preflight,
// firewall, network check, k3s, ready. The connect step finds the host key
// already pinned, so there is no trust on first use anywhere in it.

// The steps before the join, and the one after a removal.
const (
	StepCloudCreate = "cloud-create"
	StepCloudBoot   = "cloud-boot"
	StepCloudSSH    = "cloud-ssh"
	StepCloudDelete = "cloud-delete"
)

// opCreateCloudServer is the operation that orders and joins a machine.
const opCreateCloudServer = "server.create"

// cloudServerSteps is the plan the interface draws for it.
var cloudServerSteps = append([]string{StepCloudCreate, StepCloudBoot, StepCloudSSH}, addServerSteps...)

// cloudTiming is how long the panel waits for a machine, and how often it
// asks. A provider's rate limit is per project and per hour (Hetzner's is
// 3600), so the interval is not a second.
type cloudTiming struct {
	Poll        time.Duration
	Boot        time.Duration
	SSH         time.Duration
	Gone        time.Duration
	SSHPort     int
	DialTimeout time.Duration
}

var defaultCloudTiming = cloudTiming{
	Poll: 5 * time.Second, Boot: 10 * time.Minute, SSH: 10 * time.Minute, Gone: 3 * time.Minute,
	SSHPort: 22, DialTimeout: 15 * time.Second,
}

// The labels the panel puts on everything it creates at a provider. The
// server id is what deleting checks: a machine without this panel's id for
// this server on it is not deleted, whatever id the record holds.
func cloudLabels(serverID string) map[string]string {
	return map[string]string{
		version.LabelKey("managed"):   "true",
		version.LabelKey("server-id"): serverID,
	}
}

// CreateCloudServer records a server, orders its machine and joins it, as
// one operation. The API has already checked the request against the
// provider's catalogue.
func (p *Provisioner) CreateCloudServer(ctx context.Context, req api.CreateCloudServerRequest) (store.Operation, error) {
	server := store.Server{
		TeamID: req.TeamID, Name: req.Name,
		// No address until the provider gives one. The name holds the place,
		// and holds the team's (host, port) pair for it.
		Host: req.Name, SSHPort: p.cloudTiming.SSHPort, SSHUser: "root",
		Status: store.ServerPending, Role: "worker", Arch: req.Arch,
	}
	if req.ControlPlane {
		server.Role = "control-plane"
	}
	server.Labels = nodeLabelsJSON(req.Location, req.ServerType)
	if err := p.db.CreateServer(ctx, &server); err != nil {
		return store.Operation{}, err
	}

	// The panel's key for this machine alone, made before anything is
	// ordered: the public half goes to the provider, and the private half is
	// sealed here, under this server, like every other server's.
	pair, err := sshx.GenerateKeyPair(version.Binary + "@panel:" + req.Name)
	if err == nil {
		server.SSHKeyEnc, err = p.keyring.Seal([]byte(pair.PrivateKey), serverKeyContext(server.ID))
	}
	if err == nil {
		err = p.db.UpdateServer(ctx, &server)
	}
	created := store.CloudServer{
		ServerID: server.ID, ProviderID: req.ProviderID, Location: req.Location,
		ServerType: req.ServerType, Image: req.Image, SSHAccess: req.SSHAccess,
	}
	if err == nil {
		err = p.db.CreateCloudServer(ctx, &created)
	}
	if err != nil {
		_ = p.db.DeleteServer(ctx, server.ID)
		return store.Operation{}, err
	}

	op := store.Operation{
		TeamID: req.TeamID, Kind: opCreateCloudServer,
		TargetType: "server", TargetID: server.ID, CreatedBy: req.CreatedBy,
	}
	if err := p.db.CreateOperation(ctx, &op, cloudServerSteps); err != nil {
		return store.Operation{}, err
	}
	join := api.AddServerRequest{
		TeamID: req.TeamID, CreatedBy: req.CreatedBy, Name: req.Name, Host: server.Host,
		SSHPort: server.SSHPort, SSHUser: server.SSHUser,
		Location: req.Location, Size: req.ServerType, ControlPlane: req.ControlPlane,
	}
	p.start(op, func(runCtx context.Context) {
		p.runCreateCloudServer(runCtx, op, server.ID, join)
	})
	return p.db.GetOperation(ctx, op.ID)
}

// nodeLabelsJSON is the location and size a node is labelled with, which is
// what lets an app be pinned to a location without anyone reading kubectl.
func nodeLabelsJSON(location, size string) string {
	labels := map[string]string{}
	if location != "" {
		labels[version.LabelKey("location")] = kube.Slugify(location)
	}
	if size != "" {
		labels[version.LabelKey("size")] = kube.Slugify(size)
	}
	encoded, err := json.Marshal(labels)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func (p *Provisioner) runCreateCloudServer(ctx context.Context, op store.Operation, serverID string, req api.AddServerRequest) {
	p.runJoin(ctx, op, serverID, req, []joinStep{
		{StepCloudCreate, p.stepCloudCreate},
		{StepCloudBoot, p.stepCloudBoot},
		{StepCloudSSH, p.stepCloudSSH},
	})
}

// openProvider opens the connection a server was created with.
func (p *Provisioner) openProvider(ctx context.Context, teamID, providerID string) (cloud.Provider, error) {
	row, err := p.db.GetCloudProvider(ctx, teamID, providerID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, errdoc.NotFound("cloud connection", providerID)
		}
		return nil, err
	}
	token, err := p.keyring.Open(row.SealedToken, store.CloudProviderContext(row.ID))
	if err != nil {
		return nil, fmt.Errorf("read the cloud connection's token: %w", err)
	}
	return p.cloud(row.Kind, string(token))
}

// cloudRecord reads a created server with the connection it was made with.
func (p *Provisioner) cloudRecord(ctx context.Context, serverID string) (store.Server, store.CloudServer, cloud.Provider, error) {
	server, err := p.db.GetServer(ctx, serverID)
	if err != nil {
		return server, store.CloudServer{}, nil, err
	}
	created, err := p.db.GetCloudServer(ctx, serverID)
	if err != nil {
		return server, created, nil, err
	}
	provider, err := p.openProvider(ctx, server.TeamID, created.ProviderID)
	return server, created, provider, err
}

// stepCloudCreate orders the machine, once.
func (p *Provisioner) stepCloudCreate(ctx context.Context, state *addState) error {
	server, created, provider, err := p.cloudRecord(ctx, state.serverID)
	if err != nil {
		return err
	}
	title := cloud.Title(created.ProviderKind)
	labels := cloudLabels(server.ID)

	// Ordered already, by an earlier attempt: find out whether it is still
	// there rather than ordering a second one.
	if created.MachineID != "" {
		if _, err := provider.Machine(ctx, created.MachineID); err != nil {
			if errors.Is(err, cloud.ErrNotFound) {
				return errdoc.CloudMachineGone(title, created.MachineID, server.Name)
			}
			return err
		}
		message, args := errdoc.Sprintf("%s was already ordered at %s", server.Name, title)
		state.lastNote = store.StepNote{Message: message, Key: "cloudAlreadyCreated", Args: args}
		return nil
	}
	// An earlier attempt whose answer was lost: the order went through and the
	// id never reached the database. The label finds it.
	if machine, found, err := provider.FindMachine(ctx, labels); err != nil {
		return err
	} else if found {
		created.MachineID = machine.ID
		if err := p.db.UpdateCloudServer(ctx, &created); err != nil {
			return err
		}
		message, args := errdoc.Sprintf("%s was already ordered at %s", server.Name, title)
		state.lastNote = store.StepNote{Message: message, Key: "cloudAlreadyCreated", Args: args}
		return nil
	}

	// The panel's key, put on root by the provider. Given this way rather
	// than in the user data because a machine created with a key is one the
	// provider sets no root password for, and sends nobody.
	private, err := p.keyring.Open(server.SSHKeyEnc, serverKeyContext(server.ID))
	if err != nil {
		return fmt.Errorf("read this server's stored key: %w", err)
	}
	public, err := sshx.PublicKeyOf(string(private), "")
	if err != nil {
		return err
	}
	key, err := provider.ImportSSHKey(ctx, "skifity-"+server.ID, public, labels)
	if err != nil {
		return err
	}
	created.SSHKeyID = key.ID

	// The firewall, in front of the machine from its first boot.
	members, err := p.clusterAddresses(ctx, store.Server{ID: server.ID})
	if err != nil {
		return err
	}
	if created.SSHAccess == store.SSHFromCluster && len(members) == 0 {
		// Port 22 open to nobody is a machine nobody can set up, the panel
		// included. Better said before anything is paid for.
		return errdoc.BadRequest("SSH can be limited to the cluster's servers only once the panel knows their addresses, and it knows none yet. Create this server with SSH open to anywhere.")
	}
	firewall, err := provider.EnsureFirewall(ctx, firewallName(server), labels,
		firewallRules(members, server.Role == "control-plane", created.SSHAccess))
	if err != nil {
		return err
	}
	created.FirewallID = firewall
	if err := p.db.UpdateCloudServer(ctx, &created); err != nil {
		return err
	}

	// The host key the machine starts with, generated here and pinned before
	// the machine exists. The first connection is checked against it.
	hostKey, err := sshx.GenerateKeyPair(server.Name)
	if err != nil {
		return err
	}
	userData, err := cloud.UserData(cloud.HostKey{Private: hostKey.PrivateKey, Public: hostKey.PublicKey})
	if err != nil {
		return err
	}
	server.HostKey = hostKey.Fingerprint
	created.HostKeyRotated = false
	if err := p.db.UpdateServer(ctx, &server); err != nil {
		return err
	}

	machine, err := provider.CreateMachine(ctx, cloud.MachineSpec{
		Name: server.Name, Location: created.Location, Type: created.ServerType, Image: created.Image,
		UserData: userData, SSHKeys: []string{key.Name}, Firewalls: []string{firewall}, Labels: labels,
	})
	if err != nil {
		return err
	}
	created.MachineID = machine.ID
	if err := p.db.UpdateCloudServer(ctx, &created); err != nil {
		return err
	}
	message, args := errdoc.Sprintf("Ordered %s at %s: %s in %s", server.Name, title, created.ServerType, created.Location)
	state.lastNote = store.StepNote{
		Message: message, Key: "cloudCreated", Args: args,
		Details: []store.StepDetail{{Text: "Host key " + hostKey.Fingerprint, Key: "hostKey", Args: []string{hostKey.Fingerprint}}},
	}
	return nil
}

// firewallName is unique in the provider's project and says whose it is.
func firewallName(server store.Server) string {
	suffix := server.ID
	if len(suffix) > 6 {
		suffix = suffix[len(suffix)-6:]
	}
	return "skifity-" + server.Name + "-" + suffix
}

// stepCloudBoot waits for the machine to run and learns its address.
func (p *Provisioner) stepCloudBoot(ctx context.Context, state *addState) error {
	server, created, provider, err := p.cloudRecord(ctx, state.serverID)
	if err != nil {
		return err
	}
	title := cloud.Title(created.ProviderKind)

	deadline := time.Now().Add(p.cloudTiming.Boot)
	var machine cloud.Machine
	for {
		machine, err = provider.Machine(ctx, created.MachineID)
		if err != nil {
			if errors.Is(err, cloud.ErrNotFound) {
				return errdoc.CloudMachineGone(title, created.MachineID, server.Name)
			}
			return err
		}
		if machine.Running() {
			break
		}
		if time.Now().After(deadline) {
			return errdoc.CloudBootTimeout(title, server.Name, p.cloudTiming.Boot)
		}
		if err := sleep(ctx, p.cloudTiming.Poll); err != nil {
			return err
		}
	}

	server.Host = machine.IPv4
	server.ExternalIP = machine.IPv4
	if err := p.db.UpdateServer(ctx, &server); err != nil {
		return err
	}
	state.request.Host = machine.IPv4

	// Every machine the panel created lets the cluster's addresses in, and
	// this is a new one. A failure here stops, because the next steps would
	// only fail later and less clearly.
	if err := p.refreshCloudFirewalls(ctx, ""); err != nil {
		return err
	}

	// The provider copied the key onto the machine; its copy of it is
	// clutter in somebody's project now.
	if created.SSHKeyID != "" {
		if err := provider.DeleteSSHKey(ctx, created.SSHKeyID); err != nil {
			p.log.Warn("could not remove the imported key from the provider", "server", server.ID, "error", err)
		} else {
			created.SSHKeyID = ""
			if err := p.db.UpdateCloudServer(ctx, &created); err != nil {
				return err
			}
		}
	}

	message, args := errdoc.Sprintf("%s is running at %s", server.Name, machine.IPv4)
	state.lastNote = store.StepNote{Message: message, Key: "cloudRunning", Args: args}
	return nil
}

// stepCloudSSH waits for SSH with the pinned key, then replaces that key.
func (p *Provisioner) stepCloudSSH(ctx context.Context, state *addState) error {
	server, created, _, err := p.cloudRecord(ctx, state.serverID)
	if err != nil {
		return err
	}
	private, err := p.keyring.Open(server.SSHKeyEnc, serverKeyContext(server.ID))
	if err != nil {
		return fmt.Errorf("read this server's stored key: %w", err)
	}
	creds := sshx.Credentials{User: server.SSHUser, PrivateKey: string(private)}

	client, err := p.waitForSSH(ctx, server, creds, p.cloudTiming.SSH)
	if err != nil {
		return err
	}
	defer client.Close()

	if created.HostKeyRotated {
		message, args := errdoc.Sprintf("Signed in to %s with the host key it was given", server.Host)
		state.lastNote = store.StepNote{Message: message, Key: "cloudSignedIn", Args: args}
		return nil
	}

	// The key the machine started with is in the user data: the provider
	// keeps it, and the metadata service hands it to anything on the machine
	// that asks. It did its job — this connection is to the right machine —
	// and is replaced now by one the machine makes and never lets out.
	result, err := client.Run(ctx, HostKeyRotateScript)
	if err != nil {
		return errdoc.CloudHostKeyNotReplaced(server.Name, err.Error())
	}
	line := ""
	for _, output := range strings.Split(result.Stdout, "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(output), "host_key="); ok {
			line = value
		}
	}
	if result.ExitCode != 0 || line == "" {
		return errdoc.CloudHostKeyNotReplaced(server.Name, strings.TrimSpace(result.Combined()))
	}
	fingerprint, keyType, err := sshx.PublicKeyFingerprint(line)
	if err != nil || keyType != "ssh-ed25519" {
		return errdoc.CloudHostKeyNotReplaced(server.Name, "the machine did not print an ed25519 public key")
	}

	// Pinned now, over a connection already known to be to this machine, and
	// then proved: a new connection has to present it.
	server.HostKey = fingerprint
	if err := p.db.UpdateServer(ctx, &server); err != nil {
		return err
	}
	created.HostKeyRotated = true
	if err := p.db.UpdateCloudServer(ctx, &created); err != nil {
		return err
	}
	verify, err := p.waitForSSH(ctx, server, creds, 10*p.cloudTiming.Poll)
	if err != nil {
		return errdoc.CloudHostKeyNotReplaced(server.Name, errdoc.From(err).Error())
	}
	_ = verify.Close()

	state.lastNote = store.StepNote{
		Message: "Signed in with the host key the panel gave the machine, then had the machine replace it with its own",
		Key:     "cloudHostKeyReplaced",
		Details: []store.StepDetail{{Text: "Host key " + fingerprint, Key: "hostKey", Args: []string{fingerprint}}},
	}
	return nil
}

// waitForSSH dials until the machine answers with the pinned host key and
// takes the panel's key.
//
// A different host key is not trusted, however long it is presented: the
// host key is checked before the client authenticates, so nothing is sent to
// whatever presents it. It is waited through rather than failed at once, for
// the window in which an image's own sshd might answer before cloud-init has
// written the key — and if it is still there at the end, that is the failure.
func (p *Provisioner) waitForSSH(ctx context.Context, server store.Server, creds sshx.Credentials, wait time.Duration) (*sshx.Client, error) {
	deadline := time.Now().Add(wait)
	var last error
	for {
		client, err := sshx.Dial(ctx, sshx.Config{
			Host: server.Host, Port: server.SSHPort, HostKey: server.HostKey,
			Credentials: creds, Timeout: p.cloudTiming.DialTimeout,
		})
		if err == nil {
			return client, nil
		}
		last = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if time.Now().After(deadline) {
			break
		}
		if err := sleep(ctx, p.cloudTiming.Poll); err != nil {
			return nil, err
		}
	}
	if errors.Is(last, sshx.ErrHostKeyChanged) {
		return nil, errdoc.CloudHostKeyMismatch(server.Name, server.HostKey, presentedKey(last))
	}
	return nil, errdoc.CloudSSHTimeout(server.Name, wait, last)
}

// presentedKey reads the fingerprint sshx names in a host key refusal.
func presentedKey(err error) string {
	message := err.Error()
	if i := strings.LastIndex(message, "SHA256:"); i >= 0 {
		return strings.Fields(message[i:])[0]
	}
	return "a different key"
}

// HostKeyRotateScript has a machine generate a new SSH host key, put it in
// place of the one it was given, and print the public half.
//
// sshd reads its host keys when it starts, so it is reloaded; the connection
// this runs over is not ended by a reload. The key is ed25519 because that is
// the algorithm the panel's client asks for first (sshx.Dial).
const HostKeyRotateScript = `
set -eu
DIR=$(mktemp -d)
trap 'rm -rf "$DIR"' EXIT
ssh-keygen -q -t ed25519 -N '' -C '' -f "$DIR/key"
install -m 600 "$DIR/key" /etc/ssh/ssh_host_ed25519_key
install -m 644 "$DIR/key.pub" /etc/ssh/ssh_host_ed25519_key.pub
SSHD=/usr/sbin/sshd
if [ -x "$SSHD" ] && ! "$SSHD" -t 2>/dev/null; then
  echo "sshd does not accept its configuration with the new key" >&2
  exit 1
fi
systemctl reload ssh 2>/dev/null || systemctl reload sshd 2>/dev/null || kill -HUP "$(cat /run/sshd.pid)"
echo "host_key=$(cat /etc/ssh/ssh_host_ed25519_key.pub)"
`

// deleteCloudMachine deletes a created server's machine, its firewall and
// any key left at the provider, after checking the machine is the one the
// panel ordered for this server.
func (p *Provisioner) deleteCloudMachine(ctx context.Context, server store.Server) (store.StepNote, error) {
	created, err := p.db.GetCloudServer(ctx, server.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.StepNote{}, errdoc.CloudNotCreated(server.Name)
		}
		return store.StepNote{}, err
	}
	provider, err := p.openProvider(ctx, server.TeamID, created.ProviderID)
	if err != nil {
		return store.StepNote{}, err
	}
	title := cloud.Title(created.ProviderKind)

	message, args := errdoc.Sprintf("Nothing was ordered at %s for %s, so there was no machine to delete", title, server.Name)
	note := store.StepNote{Message: message, Key: "cloudNothingToDelete", Args: args}
	if created.MachineID != "" {
		machine, err := provider.Machine(ctx, created.MachineID)
		switch {
		case errors.Is(err, cloud.ErrNotFound):
			message, args := errdoc.Sprintf("%s had already been deleted at %s", server.Name, title)
			note = store.StepNote{Message: message, Key: "cloudAlreadyDeleted", Args: args}
		case err != nil:
			return store.StepNote{}, err
		case machine.Labels[version.LabelKey("server-id")] != server.ID:
			// The record says this id; the machine does not agree. Somebody
			// changed one or the other, and the machine is left alone.
			return store.StepNote{}, errdoc.CloudMachineNotOurs(title, created.MachineID, server.Name)
		default:
			if err := provider.DeleteMachine(ctx, created.MachineID); err != nil {
				return store.StepNote{}, err
			}
			p.waitUntilGone(ctx, provider, created.MachineID)
			message, args := errdoc.Sprintf("Deleted %s at %s", server.Name, title)
			note = store.StepNote{Message: message, Key: "cloudDeleted", Args: args}
		}
	}

	// A firewall with nothing behind it and a key nobody uses are free, and
	// clutter. Neither failing is a reason to keep a server that is gone.
	if created.FirewallID != "" {
		if err := provider.DeleteFirewall(ctx, created.FirewallID); err != nil {
			p.log.Warn("could not delete a removed server's firewall", "server", server.ID, "error", err)
		}
	}
	if created.SSHKeyID != "" {
		if err := provider.DeleteSSHKey(ctx, created.SSHKeyID); err != nil {
			p.log.Warn("could not delete a removed server's key at the provider", "server", server.ID, "error", err)
		}
	}
	return note, nil
}

// waitUntilGone waits, a while, for a deleted machine to go: its firewall can
// only be deleted once nothing is behind it.
func (p *Provisioner) waitUntilGone(ctx context.Context, provider cloud.Provider, machineID string) {
	deadline := time.Now().Add(p.cloudTiming.Gone)
	for time.Now().Before(deadline) {
		if _, err := provider.Machine(ctx, machineID); errors.Is(err, cloud.ErrNotFound) {
			return
		}
		if sleep(ctx, p.cloudTiming.Poll) != nil {
			return
		}
	}
}

// refreshCloudFirewalls sets the firewall in front of every machine the panel
// created to the cluster as it is now. excludeID leaves out a server that is
// leaving; its address stops being let in.
//
// Every team's: the cluster is one, and a machine one team created has to let
// in a node another team added.
func (p *Provisioner) refreshCloudFirewalls(ctx context.Context, excludeID string) error {
	created, err := p.db.ListCloudServers(ctx)
	if err != nil || len(created) == 0 {
		return err
	}
	exclude := store.Server{ID: excludeID}
	if excludeID != "" {
		if server, err := p.db.GetServer(ctx, excludeID); err == nil {
			exclude = server
		}
	}
	members, err := p.clusterAddresses(ctx, exclude)
	if err != nil {
		return err
	}

	opened := map[string]cloud.Provider{}
	var failed []error
	for _, c := range created {
		if c.FirewallID == "" || c.ServerID == excludeID {
			continue
		}
		server, err := p.db.GetServer(ctx, c.ServerID)
		if err != nil {
			continue
		}
		provider, ok := opened[c.ProviderID]
		if !ok {
			provider, err = p.openProvider(ctx, server.TeamID, c.ProviderID)
			if err != nil {
				failed = append(failed, err)
				continue
			}
			opened[c.ProviderID] = provider
		}
		rules := firewallRules(members, server.Role == "control-plane", c.SSHAccess)
		if err := provider.SetFirewallRules(ctx, c.FirewallID, rules); err != nil {
			failed = append(failed, err)
		}
	}
	return errors.Join(failed...)
}

// clusterAddresses are the addresses of every member of the cluster, as
// CIDRs: every server the panel has a record of, and every node Kubernetes
// reports — which includes the machine install.sh built, which the panel has
// no row for, and the node the panel's own requests leave from.
func (p *Provisioner) clusterAddresses(ctx context.Context, exclude store.Server) ([]string, error) {
	seen := map[string]bool{}
	add := func(address string) {
		if ip := net.ParseIP(strings.TrimSpace(address)); ip != nil && !ip.IsUnspecified() && !ip.IsLoopback() {
			seen[ip.String()] = true
		}
	}

	servers, err := p.db.ListAllServers(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range servers {
		if s.ID == exclude.ID {
			continue
		}
		add(s.ExternalIP)
		add(s.InternalIP)
		add(s.Host)
	}

	if p.cluster != nil {
		nodes, err := p.cluster.Client().Clientset().CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			// A firewall written from half the members locks the other half
			// out, so an unanswered question is a failure, not a smaller list.
			return nil, fmt.Errorf("list the cluster's nodes: %w", err)
		}
		for _, node := range nodes.Items {
			if exclude.NodeName != "" && node.Name == exclude.NodeName {
				continue
			}
			for _, address := range node.Status.Addresses {
				if address.Type == corev1.NodeInternalIP || address.Type == corev1.NodeExternalIP {
					add(address.Address)
				}
			}
		}
	}

	for _, address := range []string{exclude.ExternalIP, exclude.InternalIP, exclude.Host} {
		if ip := net.ParseIP(address); ip != nil {
			delete(seen, ip.String())
		}
	}

	out := make([]string, 0, len(seen))
	for address := range seen {
		if strings.Contains(address, ":") {
			out = append(out, address+"/128")
		} else {
			out = append(out, address+"/32")
		}
	}
	sort.Strings(out)
	return out, nil
}

// firewallRules are what the provider's firewall lets in to one machine.
//
// The same list the machine's own firewall opens (FirewallScript) and the
// installer expects, because they have to agree: HTTP and HTTPS from anywhere,
// since that is what the machine is for; SSH from anywhere or from the
// cluster only; and every cluster port in ClusterPorts — the API server on
// 6443, the kubelet on 10250, the pod network's VXLAN on UDP 8472 and
// WireGuard on UDP 51820 and 51821, and etcd's 2379 and 2380 on a control
// plane server — from the cluster's own addresses and nobody else. Nothing is
// said about outbound traffic, which a provider's firewall then allows: a node
// downloads k3s and pulls images.
func firewallRules(members []string, controlPlane bool, sshAccess string) []cloud.Rule {
	ssh := cloud.Anywhere
	sshWho := "anywhere; passwords are off, the panel signs in with its key"
	if sshAccess == store.SSHFromCluster {
		ssh = members
		sshWho = "the cluster's servers, where the panel runs"
	}
	rules := []cloud.Rule{
		{Protocol: "tcp", Port: "22", Sources: ssh, Description: "SSH from " + sshWho},
		{Protocol: "tcp", Port: "80", Sources: cloud.Anywhere, Description: "HTTP to the apps"},
		{Protocol: "tcp", Port: "443", Sources: cloud.Anywhere, Description: "HTTPS to the apps"},
	}
	for _, port := range ClusterPorts {
		if port.ControlPlaneOnly && !controlPlane {
			continue
		}
		rules = append(rules, cloud.Rule{
			Protocol: port.Protocol, Port: strconv.Itoa(port.Port), Sources: members,
			Description: port.Purpose + ", from the cluster's servers",
		})
	}
	return rules
}

// sleep waits, or stops when the operation does.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
