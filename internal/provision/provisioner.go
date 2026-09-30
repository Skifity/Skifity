package provision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"skifity/internal/api"
	"skifity/internal/cluster"
	"skifity/internal/crypto"
	"skifity/internal/errdoc"
	"skifity/internal/events"
	"skifity/internal/kube"
	"skifity/internal/notify"
	"skifity/internal/plugins"
	"skifity/internal/settings"
	"skifity/internal/sshx"
	"skifity/internal/store"
	"skifity/internal/version"
)

// Step keys, in the order they run. They are stable strings because the UI
// translates them and the database stores them.
const (
	StepConnect      = "connect"
	StepPreflight    = "preflight"
	StepInstallKey   = "install-key"
	StepFirewall     = "firewall"
	StepConnectivity = "connectivity"
	StepInstallK3s   = "install-k3s"
	StepWaitReady    = "wait-ready"
)

// addServerSteps is the plan the UI draws before anything runs.
var addServerSteps = []string{
	StepConnect, StepPreflight, StepInstallKey, StepFirewall,
	StepConnectivity, StepInstallK3s, StepWaitReady,
}

// removeServerSteps is the plan for taking a server out.
var removeServerSteps = []string{"cordon", "drain", "delete-node", "uninstall"}

// Options is what a Provisioner needs. A struct rather than eight positional
// arguments, because the last three are easy to swap by accident.
type Options struct {
	DB      *store.DB
	Keyring *crypto.Keyring
	Hub     *events.Hub
	Cluster *cluster.Cluster
	// Notifier may be nil, and then nothing is sent.
	Notifier notify.Notifier
	// Plugins hear when a server joins or leaves. A zero value sends nothing.
	Plugins plugins.Dispatcher
	// ClusterTokenPath is where the installer left this cluster's k3s join
	// token. See clusterToken.
	ClusterTokenPath string
	Logger           *slog.Logger
}

// Provisioner implements api.Provisioner.
type Provisioner struct {
	db               *store.DB
	keyring          *crypto.Keyring
	hub              *events.Hub
	cluster          *cluster.Cluster
	notifier         notify.Notifier
	plugins          plugins.Dispatcher
	clusterTokenPath string
	log              *slog.Logger

	// running tracks in-flight operations so they can be cancelled.
	mu      sync.Mutex
	running map[string]context.CancelFunc
}

// New builds a Provisioner.
func New(opts Options) *Provisioner {
	return &Provisioner{
		db: opts.DB, keyring: opts.Keyring, hub: opts.Hub, cluster: opts.Cluster,
		notifier: opts.Notifier, plugins: opts.Plugins,
		clusterTokenPath: opts.ClusterTokenPath, log: opts.Logger,
		running: map[string]context.CancelFunc{},
	}
}

// AddServer registers a server and starts provisioning it.
//
// The record and the operation are created synchronously so the UI has
// something to show immediately; the work itself runs in the background,
// because it takes minutes and a browser request does not.
func (p *Provisioner) AddServer(ctx context.Context, req api.AddServerRequest) (store.Operation, error) {
	server := store.Server{
		TeamID:  req.TeamID,
		Name:    req.Name,
		Host:    req.Host,
		SSHPort: req.SSHPort,
		SSHUser: req.SSHUser,
		Status:  store.ServerPending,
		Role:    "worker",
	}
	if req.ControlPlane {
		server.Role = "control-plane"
	}
	labels := map[string]string{}
	if req.Location != "" {
		labels[version.LabelKey("location")] = kube.Slugify(req.Location)
	}
	if req.Size != "" {
		labels[version.LabelKey("size")] = kube.Slugify(req.Size)
	}
	if encoded, err := json.Marshal(labels); err == nil {
		server.Labels = string(encoded)
	}

	// A user-supplied key is stored straight away; a password is used once and
	// never stored, so there is nothing to seal in that case.
	if req.PrivateKey != "" {
		sealed, err := p.keyring.Seal([]byte(req.PrivateKey), serverKeyContext(server.ID))
		if err != nil {
			return store.Operation{}, err
		}
		server.SSHKeyEnc = sealed
	}

	if err := p.db.CreateServer(ctx, &server); err != nil {
		return store.Operation{}, err
	}
	// The id is only known after the insert, and the context binds to it.
	if req.PrivateKey != "" {
		sealed, err := p.keyring.Seal([]byte(req.PrivateKey), serverKeyContext(server.ID))
		if err != nil {
			return store.Operation{}, err
		}
		server.SSHKeyEnc = sealed
		if err := p.db.UpdateServer(ctx, &server); err != nil {
			return store.Operation{}, err
		}
	}

	op := store.Operation{
		TeamID: req.TeamID, Kind: "server.add",
		TargetType: "server", TargetID: server.ID, CreatedBy: req.CreatedBy,
	}
	if err := p.db.CreateOperation(ctx, &op, addServerSteps); err != nil {
		return store.Operation{}, err
	}

	p.start(op, func(runCtx context.Context) {
		p.runAddServer(runCtx, op, server.ID, req)
	})
	return p.db.GetOperation(ctx, op.ID)
}

// RetryServer resumes a failed provision from the step that failed.
func (p *Provisioner) RetryServer(ctx context.Context, serverID string) (store.Operation, error) {
	server, err := p.db.GetServer(ctx, serverID)
	if err != nil {
		return store.Operation{}, err
	}
	op, err := p.db.LatestOperation(ctx, "server", serverID)
	if err != nil {
		return store.Operation{}, errdoc.New("server.no_operation", "There is nothing to retry").
			WithCause("No previous attempt to add this server was recorded.").
			WithImpact("Nothing was changed.").
			WithFix("Remove the server and add it again.")
	}
	if op.Status == store.OpRunning {
		return op, nil
	}

	// Reset from the first step that is not finished, so the steps that already
	// succeeded are not repeated.
	from := StepConnect
	for _, step := range op.Steps {
		if step.Status != store.StepSucceeded {
			from = step.Key
			break
		}
	}
	if err := p.db.ResetStepsFrom(ctx, op.ID, from); err != nil {
		return store.Operation{}, err
	}
	if err := p.db.SetOperationStatus(ctx, op.ID, store.OpRunning, "", ""); err != nil {
		return store.Operation{}, err
	}

	// Retrying uses the stored key: the password was deliberately never kept.
	req := api.AddServerRequest{
		TeamID: server.TeamID, Name: server.Name, Host: server.Host,
		SSHPort: server.SSHPort, SSHUser: server.SSHUser,
		ControlPlane: server.Role == "control-plane",
	}
	p.start(op, func(runCtx context.Context) {
		p.runAddServer(runCtx, op, server.ID, req)
	})
	return p.db.GetOperation(ctx, op.ID)
}

// runAddServer is the state machine. Every step is idempotent, so a retry can
// start from any point without undoing what came before.
func (p *Provisioner) runAddServer(ctx context.Context, op store.Operation, serverID string, req api.AddServerRequest) {
	defer p.finish(op.ID)

	_ = p.db.SetOperationStatus(ctx, op.ID, store.OpRunning, "", "")
	_ = p.db.SetServerStatus(ctx, serverID, store.ServerProvisioning, "")
	p.publishOperation(ctx, op.ID)

	done, err := p.completedSteps(ctx, op.ID)
	if err != nil {
		p.failOperation(ctx, op, serverID, "internal", err)
		return
	}

	state := &addState{operationID: op.ID, serverID: serverID, request: req}

	steps := []struct {
		key string
		run func(context.Context, *addState) error
	}{
		{StepConnect, p.stepConnect},
		{StepPreflight, p.stepPreflight},
		{StepInstallKey, p.stepInstallKey},
		{StepFirewall, p.stepFirewall},
		{StepConnectivity, p.stepConnectivity},
		{StepInstallK3s, p.stepInstallK3s},
		{StepWaitReady, p.stepWaitReady},
	}

	for _, step := range steps {
		if done[step.key] {
			// Already succeeded in an earlier attempt. The connection is still
			// needed by later steps, so reconnect quietly.
			if step.key == StepConnect && state.client == nil {
				if err := p.stepConnect(ctx, state); err != nil {
					p.failStep(ctx, op, serverID, step.key, err)
					return
				}
			}
			continue
		}
		p.setStep(ctx, op, step.key, store.StepRunning, store.StepNote{}, "")
		if err := step.run(ctx, state); err != nil {
			p.failStep(ctx, op, serverID, step.key, err)
			return
		}
		p.setStep(ctx, op, step.key, store.StepSucceeded, state.lastNote, "")
		state.lastNote = store.StepNote{}
	}

	if state.client != nil {
		_ = state.client.Close()
	}
	_ = p.db.SetOperationStatus(ctx, op.ID, store.OpSucceeded, "", "")
	_ = p.db.SetServerStatus(ctx, serverID, store.ServerReady, "")
	p.publishOperation(ctx, op.ID)
	p.log.Info("server added", "server", serverID, "host", req.Host)

	if p.notifier != nil {
		p.notifier.Notify(ctx, req.TeamID, notify.EventServerAdded, notify.Message{
			Title:  req.Name + " joined the cluster",
			Body:   "The server is ready and can run apps.",
			Level:  "success",
			Path:   "/servers/" + serverID,
			Fields: map[string]string{"Server": req.Name, "Host": req.Host},
		})
	}
	// A plugin that watches capacity — one that resizes something, or updates
	// an inventory — subscribed to this and was never told.
	p.plugins.Notify(ctx, plugins.EventServerAdded, req.TeamID, map[string]any{
		"server_id": serverID, "server": req.Name, "host": req.Host,
		"location": req.Location, "size": req.Size,
	})
}

// addState carries what one step learned to the ones after it.
type addState struct {
	// operationID is where progress and log lines are published.
	operationID string
	serverID    string
	request     api.AddServerRequest
	client      *sshx.Client
	// key is the panel-owned key installed on this server.
	key       sshx.KeyPair
	preflight Preflight
	// joinToken and serverURL come from the existing cluster.
	joinToken string
	serverURL string
	// lastNote is shown against the step that just finished: the English, the
	// key the panel translates it by, the values in the sentence, and any extra
	// lines underneath. The free-text detail beside it is now only set by a
	// failure, which is the one thing here that is not the panel's own words.
	lastNote store.StepNote
}

func (p *Provisioner) stepConnect(ctx context.Context, state *addState) error {
	server, err := p.db.GetServer(ctx, state.serverID)
	if err != nil {
		return err
	}

	creds := sshx.Credentials{
		User:       server.SSHUser,
		Password:   state.request.Password,
		Passphrase: state.request.Passphrase,
	}
	// Prefer the panel's own key when there is one: a retry has no password.
	if server.SSHKeyEnc != "" {
		plaintext, err := p.keyring.Open(server.SSHKeyEnc, serverKeyContext(server.ID))
		if err != nil {
			return fmt.Errorf("read this server's stored key: %w", err)
		}
		creds.PrivateKey = string(plaintext)
	} else if state.request.PrivateKey != "" {
		creds.PrivateKey = state.request.PrivateKey
	}
	if creds.Password == "" && creds.PrivateKey == "" {
		return errdoc.New("ssh.no_credentials", "There are no credentials to connect with").
			WithCause("The password was used once during the first attempt and deliberately not stored.").
			WithImpact("The server cannot be reached to continue.").
			WithFix("Remove this server and add it again with its password or a private key.")
	}

	client, err := sshx.Dial(ctx, sshx.Config{
		Host: server.Host, Port: server.SSHPort,
		Credentials: creds, HostKey: server.HostKey,
	})
	if err != nil {
		switch {
		case errors.Is(err, sshx.ErrHostKeyChanged):
			return errdoc.SSHHostKeyChanged(server.Host, server.HostKey, "a different key")
		case errors.Is(err, sshx.ErrAuthFailed):
			return errdoc.SSHAuthFailed(server.Host, server.SSHUser, creds.PrivateKey != "")
		default:
			return errdoc.SSHUnreachable(server.Host, server.SSHPort, err)
		}
	}
	state.client = client

	// Trust on first use: record what we saw so a later change is detectable.
	if server.HostKey == "" {
		server.HostKey = client.HostKey
		if err := p.db.UpdateServer(ctx, &server); err != nil {
			return err
		}
	}
	state.lastNote = store.StepNote{
		Message: "Connected to " + server.Host,
		Key:     "connected",
		Args:    []string{server.Host},
		Details: []store.StepDetail{{
			Text: "Host key " + client.HostKey,
			Key:  "hostKey",
			Args: []string{client.HostKey},
		}},
	}
	return nil
}

func (p *Provisioner) stepPreflight(ctx context.Context, state *addState) error {
	result, err := state.client.Run(ctx, PreflightScript)
	if err != nil {
		return fmt.Errorf("run the preflight checks: %w", err)
	}
	report := ParsePreflight(result.Stdout)
	state.preflight = report

	requirements := DefaultRequirements()
	if state.request.ControlPlane {
		requirements = ControlPlaneRequirements()
	}
	requirements.FlannelBackend = p.flannelBackend(ctx)
	problems := Evaluate(report, requirements, state.request.ControlPlane)

	server, err := p.db.GetServer(ctx, state.serverID)
	if err != nil {
		return err
	}
	server.OSInfo = strings.TrimSpace(report.OSName + " " + report.OSVersion)
	server.Arch = report.Arch
	server.CPUCores = report.CPUCores
	server.MemoryMB = report.MemoryMB
	server.DiskGB = report.DiskGB
	server.ExternalIP = report.PublicIP
	server.InternalIP = report.PrivateIP
	if err := p.db.UpdateServer(ctx, &server); err != nil {
		return err
	}

	if Fatal(problems) {
		for _, problem := range problems {
			if problem.Fatal {
				return errdoc.PreflightFailed(problem.Code, problem.Detail, problem.Fix,
					problem.Args.Detail, problem.Args.Fix).
					With("os", server.OSInfo).
					With("memory_mb", fmt.Sprint(report.MemoryMB)).
					With("disk_gb", fmt.Sprint(report.DiskGB))
			}
		}
	}

	message, args := errdoc.Sprintf("%s, %d cores, %d MB memory, %d GB free",
		server.OSInfo, report.CPUCores, report.MemoryMB, report.DiskGB)
	state.lastNote = store.StepNote{Message: message, Key: "preflightOk", Args: args}
	// The warnings, in the reader's language. They used to be joined into one
	// English string in a column the interface only read for a failed step, so
	// every non-fatal preflight finding was collected and shown to nobody.
	for _, problem := range problems {
		state.lastNote.Details = append(state.lastNote.Details,
			store.StepDetail{
				Text: problem.Detail, Key: "preflight." + problem.Code + ".detail",
				Args: problem.Args.Detail,
			},
			store.StepDetail{
				Text: problem.Fix, Key: "preflight." + problem.Code + ".fix",
				Args: problem.Args.Fix,
			})
	}
	return nil
}

// stepInstallKey installs the panel's own key and stops using the password.
//
// One key per server, generated by the panel, means the user's password is used
// exactly once and never stored, and a compromised server does not give access
// to the others.
func (p *Provisioner) stepInstallKey(ctx context.Context, state *addState) error {
	server, err := p.db.GetServer(ctx, state.serverID)
	if err != nil {
		return err
	}
	if server.SSHKeyEnc != "" {
		state.lastNote = store.StepNote{Message: "This server already has a key from Skifity", Key: "keyAlreadyThere"}
		return nil
	}

	pair, err := sshx.GenerateKeyPair(version.Binary + "@panel:" + server.Host)
	if err != nil {
		return err
	}
	state.key = pair

	result, err := state.client.Run(ctx, InstallKeyScript(pair.PublicKey, server.SSHUser))
	if err != nil {
		return fmt.Errorf("install the panel's key: %w", err)
	}
	if result.ExitCode != 0 {
		return errdoc.New("ssh.key_install_failed", "The panel's key could not be installed").
			WithCause("Writing to authorized_keys on %s failed: %s", server.Host, result.Combined()).
			WithImpact("The server was not added.").
			WithFix("Check that %s can write to its own home directory and that the disk is not full.", server.SSHUser).
			Retry()
	}

	// Prove the key works before throwing the password away, or a retry would
	// have no way in at all.
	verify, err := sshx.Dial(ctx, sshx.Config{
		Host: server.Host, Port: server.SSHPort, HostKey: server.HostKey,
		Credentials: sshx.Credentials{User: server.SSHUser, PrivateKey: pair.PrivateKey},
	})
	if err != nil {
		return errdoc.New("ssh.key_verify_failed", "The panel's key was installed but does not work").
			WithCause("Connecting with the new key failed: %s", err.Error()).
			WithImpact("The server was not added. The password was not discarded.").
			WithFix("Check the server's sshd settings: PubkeyAuthentication must be yes, and the home directory must not be group-writable.").
			Retry()
	}
	_ = verify.Close()

	sealed, err := p.keyring.Seal([]byte(pair.PrivateKey), serverKeyContext(server.ID))
	if err != nil {
		return err
	}
	server.SSHKeyEnc = sealed
	if err := p.db.UpdateServer(ctx, &server); err != nil {
		return err
	}

	// The password is now out of scope and was never written anywhere.
	state.request.Password = ""
	state.lastNote = store.StepNote{
		Message: "Installed a key just for this server; the password was not stored",
		Key:     "keyInstalled",
		Details: []store.StepDetail{{
			Text: "Fingerprint " + pair.Fingerprint,
			Key:  "fingerprint",
			Args: []string{pair.Fingerprint},
		}},
	}
	return nil
}

func (p *Provisioner) stepFirewall(ctx context.Context, state *addState) error {
	members, err := p.memberAddresses(ctx, state.serverID)
	if err != nil {
		return err
	}
	if len(members) == 0 {
		state.lastNote = store.StepNote{Message: "No other servers yet, so only ports 80 and 443 were opened", Key: "firewallFirstServer"}
	}

	result, err := state.client.Run(ctx, FirewallScript(members, state.request.ControlPlane))
	if err != nil {
		return fmt.Errorf("configure the firewall: %w", err)
	}
	if result.ExitCode != 0 {
		return errdoc.New("firewall.failed", "The firewall could not be configured").
			WithCause("The firewall commands failed on %s: %s", state.request.Host, result.Combined()).
			WithImpact("The server was not added.").
			WithFix("Check that the account Skifity connects with can run ufw or iptables. Root, or sudo without a password, is needed.").
			Retry()
	}
	if strings.Contains(result.Stdout, "firewall_configured=yes") {
		state.lastNote = store.StepNote{Message: "Opened the cluster ports to the other servers only, using ufw", Key: "firewallUfw"}
	} else {
		state.lastNote = store.StepNote{Message: "Opened the cluster ports to the other servers only, using iptables", Key: "firewallIptables"}
	}
	return nil
}

// stepConnectivity checks that the new server can actually reach the cluster.
//
// This is the check that catches a provider firewall, which SSH cannot open,
// and it is worth a step of its own because the alternative is a k3s install
// that fails five minutes later with a timeout nobody can interpret.
func (p *Provisioner) stepConnectivity(ctx context.Context, state *addState) error {
	primary, err := p.primaryServer(ctx, state.serverID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			state.lastNote = store.StepNote{Message: "This is the first server, so there is nothing to connect to yet", Key: "connectivityFirstServer"}
			return nil
		}
		return err
	}

	target := primary.ExternalIP
	if target == "" {
		target = primary.Host
	}
	result, err := state.client.Run(ctx, ConnectivityScript(target, 6443))
	if err != nil {
		return fmt.Errorf("check connectivity to the cluster: %w", err)
	}
	if !strings.Contains(result.Stdout, "reachable=yes") {
		return errdoc.PortBlocked(target, 6443, "tcp").
			With("from", state.request.Host).
			With("to", target).
			With("hint", "This is usually a firewall in your provider's control panel rather than on the server itself. Skifity can configure ufw and iptables over SSH, but it cannot reach a firewall that sits in front of the machine.")
	}
	message, args := errdoc.Sprintf("Reached %s on port 6443", target)
	state.lastNote = store.StepNote{Message: message, Key: "reached", Args: args}
	return nil
}

func (p *Provisioner) stepInstallK3s(ctx context.Context, state *addState) error {
	server, err := p.db.GetServer(ctx, state.serverID)
	if err != nil {
		return err
	}

	publicIP := server.ExternalIP
	if publicIP == "" {
		publicIP = server.Host
	}

	// Where the new server should join, if anywhere.
	//
	// The panel's own records are not the answer on their own. A panel
	// installed by install.sh runs in a cluster it has no server row for, so
	// "no control-plane server in the database" used to mean "this is the
	// first server" — and the new machine was given --cluster-init and built a
	// second, separate cluster next to the real one.
	primaryIP, err := p.controlPlaneAddress(ctx, state.serverID)
	if err != nil {
		return err
	}

	var script string
	switch primaryIP {
	case "":
		// Genuinely the first server: no cluster exists yet. The token is
		// generated here and kept encrypted, so later servers can join without
		// anyone reading it off a machine.
		token, err := p.clusterToken(ctx, server.TeamID)
		if err != nil {
			return err
		}
		script = InstallServerScript(p.k3sVersion(ctx), token, publicIP, p.flannelBackend(ctx), nil)

	default:
		token, err := p.clusterToken(ctx, server.TeamID)
		if err != nil {
			return err
		}
		state.serverURL = fmt.Sprintf("https://%s:6443", primaryIP)
		state.joinToken = token

		labels := map[string]string{}
		if state.request.Location != "" {
			labels[version.LabelKey("location")] = kube.Slugify(state.request.Location)
		}
		if state.request.Size != "" {
			labels[version.LabelKey("size")] = kube.Slugify(state.request.Size)
		}
		if state.request.ControlPlane {
			script = JoinServerScript(p.k3sVersion(ctx), token, state.serverURL, publicIP, p.flannelBackend(ctx))
		} else {
			script = JoinAgentScript(p.k3sVersion(ctx), token, state.serverURL, publicIP, labels)
		}
	}

	// Installation takes minutes and prints progress; streaming it is what
	// turns a blank wait into something a user can watch.
	result, err := state.client.RunStreaming(ctx, script, func(stream, line string) {
		p.hub.Publish(events.OperationTopic(state.operationID), "log",
			map[string]string{"step": StepInstallK3s, "stream": stream, "line": line})
	})
	if err != nil {
		return fmt.Errorf("install Kubernetes: %w", err)
	}
	if result.ExitCode != 0 {
		return errdoc.K3sInstallFailed(server.Host, result.ExitCode, result.Combined())
	}

	state.lastNote = store.StepNote{Message: "Kubernetes is installed and running", Key: "k3sRunning"}
	return nil
}

func (p *Provisioner) stepWaitReady(ctx context.Context, state *addState) error {
	server, err := p.db.GetServer(ctx, state.serverID)
	if err != nil {
		return err
	}
	if p.cluster == nil {
		state.lastNote = store.StepNote{Message: "Installed, but the panel is not connected to the cluster to confirm", Key: "k3sUnconfirmed"}
		return nil
	}

	// The node registers under its hostname, which is not necessarily the name
	// the user gave it, so find it by address.
	deadline := time.Now().Add(5 * time.Minute)
	for {
		summary, err := p.cluster.Client().Summary(ctx)
		if err == nil {
			for _, node := range summary.Nodes {
				if node.ExternalIP != server.ExternalIP && node.InternalIP != server.InternalIP &&
					node.Name != server.Name {
					continue
				}
				if !node.Ready {
					break
				}
				server.NodeName = node.Name
				server.Status = store.ServerReady
				if err := p.db.UpdateServer(ctx, &server); err != nil {
					return err
				}
				if err := p.applyNodeLabels(ctx, server); err != nil {
					p.log.Warn("could not label the node", "node", node.Name, "error", err)
				}
				message, args := errdoc.Sprintf("%s joined the cluster and is ready", node.Name)
				state.lastNote = store.StepNote{Message: message, Key: "joined", Args: args}
				return nil
			}
		}
		if time.Now().After(deadline) {
			return errdoc.New("cluster.node_not_ready", "The server did not join the cluster").
				WithCause("Kubernetes was installed on %s, but no node became ready within five minutes.", server.Host).
				WithImpact("The server is registered but is not running anything yet.").
				WithFix("Check that this server can reach the first server on port 6443, and that the two can reach each other on UDP 51820. A firewall in your provider's control panel is the usual cause.").
				WithDocs("/docs/adding-servers#when-a-step-fails").
				Retry()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// applyNodeLabels puts the user's location and size labels onto the node, so
// apps can be steered without anyone touching kubectl.
func (p *Provisioner) applyNodeLabels(ctx context.Context, server store.Server) error {
	if p.cluster == nil || server.NodeName == "" {
		return nil
	}
	var labels map[string]string
	if server.Labels != "" {
		if err := json.Unmarshal([]byte(server.Labels), &labels); err != nil {
			return fmt.Errorf("read the stored node labels: %w", err)
		}
	}
	labels[version.LabelKey("server-id")] = server.ID
	labels[version.LabelKey("managed")] = "true"

	node, err := p.cluster.Client().Clientset().CoreV1().Nodes().Get(ctx, server.NodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("read the node: %w", err)
	}
	if node.Labels == nil {
		node.Labels = map[string]string{}
	}
	for k, v := range labels {
		node.Labels[k] = v
	}
	if _, err := p.cluster.Client().Clientset().CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("label the node: %w", err)
	}
	return nil
}

// clusterTokenKey is where the join token is kept, encrypted.
const clusterTokenKey = "cluster.join_token"

// clusterToken returns the k3s join token for the cluster the panel manages.
//
// Three sources, in this order, because each is the only one that can be right
// in its own case:
//
//  1. The file the installer left. A cluster the installer created has a token
//     the panel cannot invent, and a server joining with any other token is
//     refused. This wins even over a stored one, because the cluster's own
//     token is the truth.
//  2. The setting, for a cluster the panel built itself.
//  3. A new random token, but only when there is no cluster yet. Generating one
//     for an existing cluster is how a second cluster gets built by accident,
//     so that case is an error with a way out instead.
func (p *Provisioner) clusterToken(ctx context.Context, teamID string) (string, error) {
	return p.joinToken(ctx, func() (bool, error) {
		// Only Kubernetes can answer this. A cluster in the panel's own records
		// is one the panel built, and its token would be in the setting below.
		address, err := p.kubernetesControlPlane(ctx)
		return address != "", err
	})
}

// joinToken is clusterToken's decision, with "does a cluster already exist"
// passed in. It is asked lazily, because the usual answer comes from the first
// two sources and costs no round trip.
func (p *Provisioner) joinToken(ctx context.Context, clusterExists func() (bool, error)) (string, error) {
	if token := p.tokenFromDisk(); token != "" {
		if err := p.rememberToken(ctx, token); err != nil {
			p.log.Warn("could not store the cluster join token", "error", err)
		}
		return token, nil
	}

	sealed, encrypted, err := p.db.GetSetting(ctx, clusterTokenKey)
	if err != nil {
		return "", err
	}
	if sealed != "" {
		if !encrypted {
			return sealed, nil
		}
		plaintext, err := p.keyring.Open(sealed, settings.Context(clusterTokenKey))
		if err != nil {
			return "", fmt.Errorf("read the cluster join token: %w", err)
		}
		return string(plaintext), nil
	}

	exists, err := clusterExists()
	if err != nil {
		return "", err
	}
	if exists {
		return "", errdoc.ClusterTokenMissing(p.clusterTokenPath)
	}

	token, err := crypto.RandomToken(32)
	if err != nil {
		return "", err
	}
	if err := p.rememberToken(ctx, token); err != nil {
		return "", err
	}
	return token, nil
}

// tokenFromDisk reads the token the installer copied next to the master key.
func (p *Provisioner) tokenFromDisk() string {
	if p.clusterTokenPath == "" {
		return ""
	}
	data, err := os.ReadFile(p.clusterTokenPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			p.log.Warn("could not read the cluster join token",
				"path", p.clusterTokenPath, "error", err)
		}
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (p *Provisioner) rememberToken(ctx context.Context, token string) error {
	sealed, err := p.keyring.Seal([]byte(token), settings.Context(clusterTokenKey))
	if err != nil {
		return err
	}
	return p.db.SetSetting(ctx, clusterTokenKey, sealed, true, "system")
}

// k3sVersion reads the pinned version, or empty for the stable channel.
// flannelBackend returns the pod network every node in this cluster uses.
//
// It is one setting for the whole cluster rather than a per-server choice,
// because nodes that disagree about the backend join without error and then
// never exchange a packet.
func (p *Provisioner) flannelBackend(ctx context.Context) string {
	value, _, err := p.db.GetSetting(ctx, settings.KeyFlannelBackend)
	if err != nil {
		return settings.FlannelWireGuard
	}
	return value
}

func (p *Provisioner) k3sVersion(ctx context.Context) string {
	value, _, err := p.db.GetSetting(ctx, settings.KeyK3sVersion)
	if err != nil {
		return ""
	}
	return value
}

// controlPlaneAddress returns the address a new server should join, or "" when
// there is no cluster to join yet.
//
// It asks two sources in order, because either can be the only one that knows.
// The panel's own records cover a cluster the panel built. The Kubernetes API
// covers a cluster the installer built, which the panel runs inside and has no
// server rows for — and that is the common case, because install.sh is how
// most people start.
func (p *Provisioner) controlPlaneAddress(ctx context.Context, excludeID string) (string, error) {
	server, err := p.primaryServer(ctx, excludeID)
	switch {
	case err == nil:
		if server.ExternalIP != "" {
			return server.ExternalIP, nil
		}
		return server.Host, nil
	case !errors.Is(err, store.ErrNotFound):
		return "", err
	}

	return p.kubernetesControlPlane(ctx)
}

// kubernetesControlPlane asks the cluster itself, which is the only source that
// knows about a cluster the panel did not create.
func (p *Provisioner) kubernetesControlPlane(ctx context.Context) (string, error) {
	if p.cluster == nil {
		// No cluster connection at all, so there is nothing to join and
		// nothing that could tell us otherwise.
		return "", nil
	}
	nodes, err := p.cluster.Client().Clientset().CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: "node-role.kubernetes.io/control-plane",
	})
	if err != nil {
		// Being unable to ask is not the same as the answer being no: guessing
		// "no" here is what builds a second cluster.
		return "", fmt.Errorf("check whether a cluster already exists: %w", err)
	}
	address := firstControlPlaneAddress(nodes.Items)
	if address != "" {
		p.log.Info("joining the cluster this panel already runs in", "address", address)
	}
	return address, nil
}

// firstControlPlaneAddress picks the node a new server should point at.
func firstControlPlaneAddress(nodes []corev1.Node) string {
	for _, node := range nodes {
		if address := nodeAddress(node); address != "" {
			return address
		}
	}
	return ""
}

// nodeAddress prefers the address another machine can reach.
func nodeAddress(node corev1.Node) string {
	var internal string
	for _, address := range node.Status.Addresses {
		switch address.Type {
		case corev1.NodeExternalIP:
			return address.Address
		case corev1.NodeInternalIP:
			if internal == "" {
				internal = address.Address
			}
		}
	}
	return internal
}

// primaryServer returns the control plane server to join, excluding one.
func (p *Provisioner) primaryServer(ctx context.Context, excludeID string) (store.Server, error) {
	servers, err := p.db.ListAllServers(ctx)
	if err != nil {
		return store.Server{}, err
	}
	for _, s := range servers {
		if s.ID == excludeID || s.Role != "control-plane" {
			continue
		}
		if s.Status == store.ServerReady || s.Status == store.ServerNotReady {
			return s, nil
		}
	}
	return store.Server{}, store.ErrNotFound
}

// memberAddresses lists the addresses of the other cluster members, so the
// firewall opens cluster ports to them and nobody else.
func (p *Provisioner) memberAddresses(ctx context.Context, excludeID string) ([]string, error) {
	servers, err := p.db.ListAllServers(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, s := range servers {
		if s.ID == excludeID {
			continue
		}
		for _, addr := range []string{s.ExternalIP, s.InternalIP} {
			if addr != "" && !contains(out, addr) {
				out = append(out, addr)
			}
		}
	}
	return out, nil
}

func contains(list []string, value string) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}

func serverKeyContext(serverID string) string { return "server_key:" + serverID }
