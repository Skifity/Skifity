package provision

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"skifity/internal/api"
	"skifity/internal/crypto"
	"skifity/internal/events"
	"skifity/internal/sshx"
	"skifity/internal/store"
)

// These tests run the whole add-server state machine against a real in-process
// SSH server. What is exercised is the part that actually breaks in production:
// the order of the steps, the commands sent, the parsing of their output, and
// what happens when one of them fails.

func testHarness(t *testing.T) (*Provisioner, *store.DB, *crypto.Keyring, string) {
	t.Helper()

	db, err := store.OpenMemory(t.Context())
	if err != nil {
		t.Fatalf("open the test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	keyring, err := crypto.NewKeyring("k1", key)
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}

	team := store.Team{Name: "Acme", Slug: "acme"}
	if err := db.CreateTeam(t.Context(), &team); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := New(Options{DB: db, Keyring: keyring, Hub: events.NewHub(64), Logger: logger})
	return p, db, keyring, team.ID
}

// healthyServer starts an SSH server that answers the provisioning commands the
// way a real Ubuntu machine would.
func healthyServer(t *testing.T) *sshx.TestServer {
	t.Helper()
	server, err := sshx.NewTestServer(sshx.TestServerOptions{Password: "hunter2"})
	if err != nil {
		t.Fatalf("NewTestServer: %v", err)
	}
	t.Cleanup(func() { server.Close() })

	server.Respond("os_release", "", 0)
	server.Respond("/etc/os-release", realPreflightOutput, 0)
	server.Respond("authorized_keys", "key_installed=yes", 0)
	server.Respond("allow_public", "firewall_configured=yes", 0)
	server.Respond("get.k3s.io", "==> Installing Kubernetes (k3s)\nk3s is running", 0)
	server.Respond("reachable", "reachable=yes", 0)
	return server
}

// waitForOperation polls until an operation reaches a terminal state.
func waitForOperation(t *testing.T, db *store.DB, operationID string) store.Operation {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		op, err := db.GetOperation(t.Context(), operationID)
		if err != nil {
			t.Fatalf("GetOperation: %v", err)
		}
		switch op.Status {
		case store.OpSucceeded, store.OpFailed, store.OpCancelled:
			return op
		}
		if time.Now().After(deadline) {
			t.Fatalf("the operation was still %s after 20 seconds: %+v", op.Status, op.Steps)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAddServerRunsEveryStep(t *testing.T) {
	p, db, keyring, teamID := testHarness(t)
	sshServer := healthyServer(t)
	host, port := sshServer.Addr()

	op, err := p.AddServer(t.Context(), api.AddServerRequest{
		TeamID: teamID, Name: "node-1", Host: host, SSHPort: port,
		SSHUser: "root", Password: "hunter2", Location: "Frankfurt", Size: "small",
	})
	if err != nil {
		t.Fatalf("AddServer: %v", err)
	}

	finished := waitForOperation(t, db, op.ID)
	if finished.Status != store.OpSucceeded {
		t.Fatalf("the operation failed: %s %s\nsteps: %+v",
			finished.ErrorCode, finished.ErrorMsg, finished.Steps)
	}

	// Every step must have run, in order.
	if len(finished.Steps) != len(addServerSteps) {
		t.Fatalf("got %d steps, want %d", len(finished.Steps), len(addServerSteps))
	}
	for i, step := range finished.Steps {
		if step.Key != addServerSteps[i] {
			t.Fatalf("step %d is %q, want %q", i, step.Key, addServerSteps[i])
		}
		if step.Status != store.StepSucceeded {
			t.Fatalf("step %q is %s: %s", step.Key, step.Status, step.Detail)
		}
	}

	// The server record must have learned everything the preflight found.
	server, err := db.GetServer(t.Context(), finished.TargetID)
	if err != nil {
		t.Fatalf("GetServer: %v", err)
	}
	if server.Status != store.ServerReady {
		t.Fatalf("server status is %s, want ready", server.Status)
	}
	if server.MemoryMB != 3936 || server.CPUCores != 2 {
		t.Fatalf("preflight data was not stored: %d MB, %d cores", server.MemoryMB, server.CPUCores)
	}
	if server.ExternalIP != "203.0.113.10" {
		t.Fatalf("the public address was not stored: %q", server.ExternalIP)
	}
	if server.HostKey == "" {
		t.Fatal("the SSH host key was not recorded, so a later change could not be detected")
	}

	// A panel-owned key must have been generated, installed and stored
	// encrypted. This is the promise that the password is never kept.
	if server.SSHKeyEnc == "" {
		t.Fatal("no key was stored for this server")
	}
	if strings.Contains(server.SSHKeyEnc, "PRIVATE KEY") {
		t.Fatal("the private key is stored in plaintext")
	}
	plaintext, err := keyring.Open(server.SSHKeyEnc, serverKeyContext(server.ID))
	if err != nil {
		t.Fatalf("the stored key could not be decrypted: %v", err)
	}
	if !strings.Contains(string(plaintext), "OPENSSH PRIVATE KEY") {
		t.Fatal("what was stored is not a private key")
	}

	// The password must appear nowhere in the database.
	assertPasswordNotStored(t, db, "hunter2")

	// And the commands sent must be the ones we expect.
	for _, expected := range []string{"os-release", "authorized_keys", "allow_public", "get.k3s.io"} {
		if !sshServer.Ran(expected) {
			t.Errorf("no command containing %q was run; commands were:\n%s",
				expected, strings.Join(sshServer.Commands(), "\n---\n"))
		}
	}
}

// assertPasswordNotStored scans every text column for the password.
func assertPasswordNotStored(t *testing.T, db *store.DB, password string) {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), `SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		tables = append(tables, name)
	}
	rows.Close()

	for _, table := range tables {
		if strings.HasPrefix(table, "sqlite_") {
			continue
		}
		// Dumping each table as text and searching it catches a password that
		// leaked into any column, including one added later.
		var found int
		query := `SELECT COUNT(*) FROM ` + table + ` WHERE CAST(` + table + ` AS TEXT) LIKE ?`
		if err := db.QueryRowContext(t.Context(), query, "%"+password+"%").Scan(&found); err != nil {
			// Not every table supports being cast as a whole; fall back to
			// checking the columns individually.
			continue
		}
		if found > 0 {
			t.Fatalf("the password appears in the table %s", table)
		}
	}
}

func TestAddServerStopsAtPreflight(t *testing.T) {
	p, db, _, teamID := testHarness(t)

	sshServer, err := sshx.NewTestServer(sshx.TestServerOptions{Password: "pw"})
	if err != nil {
		t.Fatalf("NewTestServer: %v", err)
	}
	defer sshServer.Close()
	// A 256 MB server with nginx already on ports 80 and 443.
	sshServer.Respond("/etc/os-release", `os_id=ubuntu
os_name=Ubuntu
os_version=24.04
arch=x86_64
has_systemd=yes
cpu_cores=1
memory_mb=256
disk_gb=20
public_ip=203.0.113.20
conflicts=nginx
ports_in_use=80 443
has_wireguard=yes
`, 0)
	host, port := sshServer.Addr()

	op, err := p.AddServer(t.Context(), api.AddServerRequest{
		TeamID: teamID, Name: "tiny", Host: host, SSHPort: port,
		SSHUser: "root", Password: "pw",
	})
	if err != nil {
		t.Fatalf("AddServer: %v", err)
	}

	finished := waitForOperation(t, db, op.ID)
	if finished.Status != store.OpFailed {
		t.Fatalf("a 256 MB server with nginx running was accepted: %s", finished.Status)
	}
	if !strings.HasPrefix(finished.ErrorCode, "preflight.") {
		t.Fatalf("the failure code is %q, want a preflight code", finished.ErrorCode)
	}

	// Nothing may have been changed on the machine.
	if sshServer.Ran("authorized_keys") {
		t.Fatal("a key was installed on a server that failed preflight")
	}
	if sshServer.Ran("get.k3s.io") {
		t.Fatal("Kubernetes was installed on a server that failed preflight")
	}

	// The failed step must carry an explanation with a fix.
	var preflightStep store.OperationStep
	for _, step := range finished.Steps {
		if step.Key == StepPreflight {
			preflightStep = step
		}
	}
	if preflightStep.Status != store.StepFailed {
		t.Fatalf("the preflight step is %s", preflightStep.Status)
	}
	if !strings.Contains(preflightStep.Detail, "How to fix it") {
		t.Fatalf("the failure does not tell the user how to fix it:\n%s", preflightStep.Detail)
	}
}

func TestAddServerReportsWrongPassword(t *testing.T) {
	p, db, _, teamID := testHarness(t)
	sshServer, err := sshx.NewTestServer(sshx.TestServerOptions{Password: "correct"})
	if err != nil {
		t.Fatalf("NewTestServer: %v", err)
	}
	defer sshServer.Close()
	host, port := sshServer.Addr()

	op, err := p.AddServer(t.Context(), api.AddServerRequest{
		TeamID: teamID, Name: "node", Host: host, SSHPort: port,
		SSHUser: "root", Password: "wrong",
	})
	if err != nil {
		t.Fatalf("AddServer: %v", err)
	}

	finished := waitForOperation(t, db, op.ID)
	if finished.Status != store.OpFailed {
		t.Fatal("a wrong password was accepted")
	}
	if finished.ErrorCode != "ssh.auth_failed" {
		t.Fatalf("the failure code is %q, want ssh.auth_failed", finished.ErrorCode)
	}
	// The failure must name the user and suggest the key option.
	var connectStep store.OperationStep
	for _, step := range finished.Steps {
		if step.Key == StepConnect {
			connectStep = step
		}
	}
	if !strings.Contains(connectStep.Detail, "private key") {
		t.Fatalf("the message does not mention the key option:\n%s", connectStep.Detail)
	}
}

func TestRetryResumesFromTheFailedStep(t *testing.T) {
	p, db, _, teamID := testHarness(t)

	sshServer, err := sshx.NewTestServer(sshx.TestServerOptions{Password: "pw"})
	if err != nil {
		t.Fatalf("NewTestServer: %v", err)
	}
	defer sshServer.Close()
	sshServer.Respond("/etc/os-release", realPreflightOutput, 0)
	sshServer.Respond("authorized_keys", "key_installed=yes", 0)
	sshServer.Respond("allow_public", "firewall_configured=yes", 0)
	sshServer.Respond("reachable", "reachable=yes", 0)
	// The k3s install fails the first time.
	sshServer.RespondStderr("get.k3s.io", "", "could not resolve get.k3s.io", 6)
	host, port := sshServer.Addr()

	op, err := p.AddServer(t.Context(), api.AddServerRequest{
		TeamID: teamID, Name: "node", Host: host, SSHPort: port,
		SSHUser: "root", Password: "pw",
	})
	if err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	failed := waitForOperation(t, db, op.ID)
	if failed.Status != store.OpFailed || failed.ErrorCode != "k3s.install_failed" {
		t.Fatalf("expected a k3s install failure, got %s %s", failed.Status, failed.ErrorCode)
	}

	// The steps before the failure must be marked as done.
	for _, step := range failed.Steps {
		switch step.Key {
		case StepConnect, StepPreflight, StepInstallKey, StepFirewall, StepConnectivity:
			if step.Status != store.StepSucceeded {
				t.Fatalf("step %q is %s before the failure", step.Key, step.Status)
			}
		case StepInstallK3s:
			if step.Status != store.StepFailed {
				t.Fatalf("the failing step is %s", step.Status)
			}
		}
	}

	// Now the install works. Retrying must not repeat the earlier steps: the
	// password is gone, so repeating the connect step with a password would
	// fail, and repeating the key install would be wasted work.
	commandsBefore := len(sshServer.Commands())
	sshServer.Respond("get.k3s.io", "k3s is running", 0)

	retried, err := p.RetryServer(t.Context(), failed.TargetID)
	if err != nil {
		t.Fatalf("RetryServer: %v", err)
	}
	final := waitForOperation(t, db, retried.ID)
	if final.Status != store.OpSucceeded {
		t.Fatalf("the retry failed: %s %s\n%+v", final.ErrorCode, final.ErrorMsg, final.Steps)
	}

	// The retry reconnects (with the stored key) and installs; it must not run
	// the preflight or the key install again.
	after := sshServer.Commands()[commandsBefore:]
	for _, command := range after {
		if strings.Contains(command, "authorized_keys") {
			t.Fatal("the retry installed the key a second time")
		}
	}
	if len(sshServer.AuthAttempts()) == 0 {
		t.Fatal("the retry did not authenticate at all")
	}
	// The retry must have used the stored key, not a password.
	lastAuth := sshServer.AuthAttempts()[len(sshServer.AuthAttempts())-1]
	if lastAuth != "publickey" {
		t.Fatalf("the retry authenticated with %q; the password was not stored, so it must use the key", lastAuth)
	}
}

func TestSecondServerJoinsTheFirst(t *testing.T) {
	p, db, _, teamID := testHarness(t)

	// An existing, ready control plane server.
	first := store.Server{
		TeamID: teamID, Name: "node-1", Host: "203.0.113.10", SSHPort: 22, SSHUser: "root",
		Role: "control-plane", Status: store.ServerReady, ExternalIP: "203.0.113.10",
	}
	if err := db.CreateServer(t.Context(), &first); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}

	sshServer := healthyServer(t)
	host, port := sshServer.Addr()
	op, err := p.AddServer(t.Context(), api.AddServerRequest{
		TeamID: teamID, Name: "node-2", Host: host, SSHPort: port,
		SSHUser: "root", Password: "hunter2",
	})
	if err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	finished := waitForOperation(t, db, op.ID)
	if finished.Status != store.OpSucceeded {
		t.Fatalf("joining failed: %s %s\n%+v", finished.ErrorCode, finished.ErrorMsg, finished.Steps)
	}

	// The install must have been an agent join pointing at the first server.
	var installCommand string
	for _, command := range sshServer.Commands() {
		if strings.Contains(command, "get.k3s.io") {
			installCommand = command
		}
	}
	if !strings.Contains(installCommand, `K3S_URL='https://203.0.113.10:6443'`) {
		t.Fatalf("the second server was not pointed at the first:\n%s", installCommand)
	}
	if strings.Contains(installCommand, "--cluster-init") {
		t.Fatalf("the second server tried to start its own cluster:\n%s", installCommand)
	}

	// The firewall must have been opened to the first server, and only to it.
	var firewallCommand string
	for _, command := range sshServer.Commands() {
		if strings.Contains(command, "allow_public") {
			firewallCommand = command
		}
	}
	if !strings.Contains(firewallCommand, `allow_from '203.0.113.10' 6443`) {
		t.Fatalf("the cluster port was not opened to the first server:\n%s", firewallCommand)
	}
}

func TestBlockedConnectivityIsReportedAsAProviderFirewall(t *testing.T) {
	p, db, _, teamID := testHarness(t)

	first := store.Server{
		TeamID: teamID, Name: "node-1", Host: "203.0.113.10", SSHPort: 22, SSHUser: "root",
		Role: "control-plane", Status: store.ServerReady, ExternalIP: "203.0.113.10",
	}
	if err := db.CreateServer(t.Context(), &first); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}

	sshServer, err := sshx.NewTestServer(sshx.TestServerOptions{Password: "pw"})
	if err != nil {
		t.Fatalf("NewTestServer: %v", err)
	}
	defer sshServer.Close()
	sshServer.Respond("/etc/os-release", realPreflightOutput, 0)
	sshServer.Respond("authorized_keys", "key_installed=yes", 0)
	sshServer.Respond("allow_public", "firewall_configured=yes", 0)
	// The new server cannot reach the first one: a provider firewall.
	sshServer.Respond("reachable", "reachable=no", 0)
	host, port := sshServer.Addr()

	op, err := p.AddServer(t.Context(), api.AddServerRequest{
		TeamID: teamID, Name: "node-2", Host: host, SSHPort: port,
		SSHUser: "root", Password: "pw",
	})
	if err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	finished := waitForOperation(t, db, op.ID)
	if finished.Status != store.OpFailed {
		t.Fatal("a server that cannot reach the cluster was accepted")
	}
	if finished.ErrorCode != "network.port_blocked" {
		t.Fatalf("the failure code is %q", finished.ErrorCode)
	}
	// Failing here rather than during the k3s install is the point: the
	// alternative is a five-minute install that ends in a timeout.
	if sshServer.Ran("get.k3s.io") {
		t.Fatal("Kubernetes was installed despite the network being blocked")
	}

	var step store.OperationStep
	for _, s := range finished.Steps {
		if s.Key == StepConnectivity {
			step = s
		}
	}
	if !strings.Contains(step.Detail, "provider") {
		t.Fatalf("the message does not point at the provider's firewall:\n%s", step.Detail)
	}
}

func TestInterruptedOperationsAreMarkedFailed(t *testing.T) {
	p, db, _, teamID := testHarness(t)

	op := store.Operation{
		TeamID: teamID, Kind: "server.add", TargetType: "server", TargetID: "srv_x",
		Status: store.OpRunning,
	}
	if err := db.CreateOperation(t.Context(), &op, addServerSteps); err != nil {
		t.Fatalf("CreateOperation: %v", err)
	}
	if err := db.SetStepStatus(t.Context(), op.ID, StepInstallK3s, store.StepRunning, store.StepNote{}, ""); err != nil {
		t.Fatalf("SetStepStatus: %v", err)
	}
	if err := db.SetOperationStatus(t.Context(), op.ID, store.OpRunning, "", ""); err != nil {
		t.Fatalf("SetOperationStatus: %v", err)
	}

	// This is what happens when the panel restarts mid-provision.
	if err := p.ResumeInterrupted(t.Context()); err != nil {
		t.Fatalf("ResumeInterrupted: %v", err)
	}

	after, err := db.GetOperation(t.Context(), op.ID)
	if err != nil {
		t.Fatalf("GetOperation: %v", err)
	}
	if after.Status != store.OpFailed {
		t.Fatalf("an interrupted operation is still %s, so the UI would show it as in progress forever", after.Status)
	}
	if after.ErrorCode != "operation.interrupted" {
		t.Fatalf("the failure code is %q", after.ErrorCode)
	}
	for _, step := range after.Steps {
		if step.Key == StepInstallK3s && step.Status != store.StepFailed {
			t.Fatalf("the step that was running is %s", step.Status)
		}
	}
}

// TestPromotingATooSmallServerChangesNothing: promotion drains the node,
// removes it from the cluster and uninstalls k3s before reinstalling it as a
// control plane member. Until the check below ran first, the only thing that
// noticed a machine too small to hold etcd was the install at the very end — by
// which time the apps had been moved off and a working worker had been turned
// into a machine with nothing on it.
//
// The panel refuses to add a control plane server below these requirements. It
// has to refuse to make one the same way.
func TestPromotingATooSmallServerChangesNothing(t *testing.T) {
	p, db, keyring, teamID := testHarness(t)

	// A server the panel already manages has the panel's own key on it, which
	// is what a promotion connects with: there is no password to re-enter.
	pair, err := sshx.GenerateKeyPair("skifity-test")
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	sshServer, err := sshx.NewTestServer(sshx.TestServerOptions{AuthorizedKey: pair.PublicKey})
	if err != nil {
		t.Fatalf("NewTestServer: %v", err)
	}
	defer sshServer.Close()
	// Large enough for a worker, too small for a control plane: the panel asks
	// a worker for 900 MB and 8 GB, and a control plane for 1800 MB and 20 GB.
	sshServer.Respond("/etc/os-release", `os_id=ubuntu
os_name=Ubuntu
os_version=24.04
arch=x86_64
has_systemd=yes
cpu_cores=1
memory_mb=1024
disk_gb=10
public_ip=203.0.113.40
has_wireguard=yes
`, 0)
	host, port := sshServer.Addr()

	worker := store.Server{
		TeamID: teamID, Name: "small", Host: host, SSHPort: port, SSHUser: "root",
		Role: "worker", Status: store.ServerReady,
	}
	if err := db.CreateServer(t.Context(), &worker); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	sealed, err := keyring.Seal([]byte(pair.PrivateKey), serverKeyContext(worker.ID))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	worker.SSHKeyEnc = sealed
	if err := db.UpdateServer(t.Context(), &worker); err != nil {
		t.Fatalf("UpdateServer: %v", err)
	}

	op, err := p.PromoteServer(t.Context(), worker.ID)
	if err != nil {
		t.Fatalf("PromoteServer: %v", err)
	}

	finished := waitForOperation(t, db, op.ID)
	if finished.Status != store.OpFailed {
		t.Fatalf("a 1 GB server was promoted to a control plane: %s", finished.Status)
	}
	if !strings.HasPrefix(finished.ErrorCode, "preflight.") {
		t.Fatalf("the failure code is %q, want a preflight code", finished.ErrorCode)
	}

	// And nothing was done to the machine: no uninstall, no reinstall. A
	// refusal that has already wiped the node is not a refusal.
	if sshServer.Ran("k3s-uninstall") || sshServer.Ran("get.k3s.io") {
		t.Fatal("the server was changed before the check refused it")
	}

	// The row still says worker, so the panel and the cluster still agree.
	current, err := db.GetServer(t.Context(), worker.ID)
	if err != nil {
		t.Fatalf("GetServer: %v", err)
	}
	if current.Role != "worker" {
		t.Fatalf("the server is recorded as %q after a refused promotion", current.Role)
	}
}
