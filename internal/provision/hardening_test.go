package provision

import (
	"strings"
	"testing"

	"skifity/internal/api"
	"skifity/internal/errdoc"
	"skifity/internal/sshx"
	"skifity/internal/store"
)

// A cloud image as it usually comes: passwords on, root with a password,
// nothing installing updates, no firewall yet.
const exposedServer = `os_id=ubuntu
ssh_password=yes
ssh_kbd=no
ssh_root=yes
ssh_pubkey=yes
ssh_dropins=yes
fail2ban=absent
auto_updates=off
firewall=none
reboot_required=yes
`

// The same machine after somebody took care of it.
const hardenedServer = `os_id=debian
ssh_password=no
ssh_kbd=no
ssh_root=prohibit-password
ssh_pubkey=yes
ssh_dropins=yes
fail2ban=absent
auto_updates=on
firewall=ufw
reboot_required=no
`

func findings(list []api.HardeningFinding) map[string]api.HardeningFinding {
	out := map[string]api.HardeningFinding{}
	for _, f := range list {
		out[f.Code] = f
	}
	return out
}

func TestAnExposedServerIsToldWhatIsOpen(t *testing.T) {
	list := EvaluateHardening(ParseHardening(exposedServer))
	got := findings(list)
	want := map[string][2]string{
		"ssh_passwords": {"on", HardeningRisk},
		"ssh_root":      {"password", HardeningRisk},
		"fail2ban":      {"absent", HardeningWarn},
		"auto_updates":  {"off", HardeningWarn},
		"firewall":      {"none", HardeningRisk},
		"reboot":        {"required", HardeningWarn},
	}
	for code, w := range want {
		f, ok := got[code]
		if !ok {
			t.Errorf("no %s finding", code)
			continue
		}
		if f.State != w[0] || f.Level != w[1] {
			t.Errorf("%s is %s/%s, want %s/%s", code, f.State, f.Level, w[0], w[1])
		}
		if f.Level != HardeningOK && f.Fix == "" {
			t.Errorf("%s is a problem with no fix", code)
		}
	}
	// Worst first, so the page leads with what matters.
	for i := 1; i < len(list); i++ {
		rank := map[string]int{HardeningRisk: 0, HardeningWarn: 1, HardeningOK: 2}
		if rank[list[i].Level] < rank[list[i-1].Level] {
			t.Fatalf("the findings are not worst first: %+v", list)
		}
	}
}

// Without passwords there is nothing to guess, so a missing fail2ban is not
// a warning worth somebody's evening.
func TestAHardenedServerIsAllClear(t *testing.T) {
	for _, f := range EvaluateHardening(ParseHardening(hardenedServer)) {
		if f.Level != HardeningOK {
			t.Errorf("%s is %s on a hardened server: %s", f.Code, f.Level, f.Detail)
		}
		if f.Code == "firewall" && f.Firewall != "ufw" {
			t.Errorf("the firewall found is %q", f.Firewall)
		}
	}
}

// sshd -T could not be read — no sudo, an unusual path — and the page says
// it does not know rather than calling the server safe.
func TestAnUnreadableSSHDaemonIsNotCalledSafe(t *testing.T) {
	got := findings(EvaluateHardening(ParseHardening("ssh_password=unknown\nfirewall=ufw\n")))
	if f := got["ssh_passwords"]; f.State != "unknown" || f.Level == HardeningOK {
		t.Errorf("an unknown SSH daemon is %s/%s", f.State, f.Level)
	}
}

// The scripts run as root on somebody's machine. They are constants, so
// nothing is interpolated into them; the audit writes nothing but its own
// temporary file, and the change checks itself before the daemon reloads.
func TestTheHardeningScriptsAreFixedAndCareful(t *testing.T) {
	for _, change := range []string{"tee ", "systemctl reload", "sed -i", "> /etc", ">/etc"} {
		if strings.Contains(HardeningScript, change) {
			t.Errorf("the audit changes something: %q", change)
		}
	}
	check := strings.Index(SSHPasswordsOffScript, `"$SSHD" -t`)
	reload := strings.Index(SSHPasswordsOffScript, "systemctl reload")
	if check < 0 || reload < 0 || check > reload {
		t.Error("the daemon is reloaded before its new configuration is checked")
	}
	if !strings.Contains(SSHPasswordsOffScript, "no_pubkey") {
		t.Error("passwords can be turned off on a daemon that takes no keys, which locks everybody out")
	}
}

// keyedServer is a server this panel added: its key sealed in the store and
// accepted by the SSH server standing in for the machine.
func keyedServer(t *testing.T, p *Provisioner, db *store.DB, teamID string) (store.Server, *sshx.TestServer) {
	t.Helper()
	pair, err := sshx.GenerateKeyPair("skifity-test")
	if err != nil {
		t.Fatal(err)
	}
	machine, err := sshx.NewTestServer(sshx.TestServerOptions{AuthorizedKey: pair.PublicKey})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { machine.Close() })
	host, port := machine.Addr()
	server := store.Server{TeamID: teamID, Name: "node-1", Host: host, SSHPort: port, SSHUser: "root", Role: "worker", Status: store.ServerReady}
	if err := db.CreateServer(t.Context(), &server); err != nil {
		t.Fatal(err)
	}
	sealed, err := p.keyring.Seal([]byte(pair.PrivateKey), serverKeyContext(server.ID))
	if err != nil {
		t.Fatal(err)
	}
	server.SSHKeyEnc = sealed
	if err := db.UpdateServer(t.Context(), &server); err != nil {
		t.Fatal(err)
	}
	return server, machine
}

func TestAServerIsAuditedOverItsOwnKey(t *testing.T) {
	p, db, _, teamID := testHarness(t)
	server, machine := keyedServer(t, p, db, teamID)
	machine.Respond("passwordauthentication", exposedServer, 0)

	report, err := p.AuditServer(t.Context(), server.ID)
	if err != nil {
		t.Fatalf("AuditServer: %v", err)
	}
	if !report.CanTurnOffPasswords {
		t.Error("a server taking passwords, with keys and a drop-in directory, is not offered the fix")
	}
	if got := findings(report.Findings)["ssh_passwords"]; got.Level != HardeningRisk {
		t.Errorf("the audit read %+v", got)
	}
}

func TestTurningPasswordsOffIsCheckedAndReported(t *testing.T) {
	p, db, _, teamID := testHarness(t)
	server, machine := keyedServer(t, p, db, teamID)

	// The daemon takes no keys: nothing is written, and the reason is given.
	machine.Respond("00-skifity-hardening.conf", "result=no_pubkey\n", 0)
	_, err := p.TurnOffSSHPasswords(t.Context(), server.ID)
	if problem := errdoc.From(err); problem.Code != "server.ssh_hardening_refused" ||
		!strings.Contains(problem.Cause, "does not accept keys") {
		t.Fatalf("turning passwords off on a keyless daemon answered %v", err)
	}

	// It takes; the answer is the server checked again.
	machine.Respond("00-skifity-hardening.conf", "result=done\n", 0)
	machine.Respond("passwordauthentication", hardenedServer, 0)
	report, err := p.TurnOffSSHPasswords(t.Context(), server.ID)
	if err != nil {
		t.Fatalf("TurnOffSSHPasswords: %v", err)
	}
	if report.CanTurnOffPasswords || findings(report.Findings)["ssh_passwords"].Level != HardeningOK {
		t.Errorf("after the change the report is %+v", report)
	}
}

// A server Skifity did not add has no key to sign in with.
func TestAServerWithoutAKeyIsNotAudited(t *testing.T) {
	p, db, _, teamID := testHarness(t)
	server := store.Server{TeamID: teamID, Name: "found", Host: "10.0.0.9", SSHPort: 22, Role: "worker", Status: store.ServerReady}
	if err := db.CreateServer(t.Context(), &server); err != nil {
		t.Fatal(err)
	}
	if _, err := p.AuditServer(t.Context(), server.ID); errdoc.From(err).Code != "server.not_ours" {
		t.Errorf("auditing a server with no key answered %v", err)
	}
}
