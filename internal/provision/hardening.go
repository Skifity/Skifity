package provision

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"skifity/internal/api"
	"skifity/internal/errdoc"
	"skifity/internal/sshx"
	"skifity/internal/store"
)

// How a server that is already in the cluster stands up to the internet.
//
// Adding a server opens the firewall the cluster needs and nothing else; what
// the machine was like before — an SSH daemon that takes passwords, root
// allowed to sign in with one, no automatic security updates — stays as it
// was, and nothing ever said so. Dokploy audits this; so does this, read-only,
// with one change on offer: turning SSH passwords off, which is the one that
// matters most and the one Skifity can make safely, because it signs in with
// a key.

// HardeningScript reads a server's settings without changing any of them.
//
// sshd -T prints the configuration the daemon is actually running with,
// Include files and Match defaults resolved, which reading sshd_config by
// hand gets wrong on every distribution that ships a drop-in directory.
const HardeningScript = `
set -u
SUDO=""
if [ "$(id -u)" != "0" ]; then SUDO="sudo -n"; fi

if [ -r /etc/os-release ]; then . /etc/os-release; echo "os_id=${ID:-unknown}"; fi

SSHD=""
for candidate in /usr/sbin/sshd /usr/local/sbin/sshd /sbin/sshd; do
  if [ -x "$candidate" ]; then SSHD="$candidate"; break; fi
done
if [ -n "$SSHD" ] && $SUDO "$SSHD" -T >/tmp/.skifity-sshd 2>/dev/null; then
  echo "ssh_password=$(awk '$1=="passwordauthentication"{print $2}' /tmp/.skifity-sshd)"
  echo "ssh_kbd=$(awk '$1=="kbdinteractiveauthentication"||$1=="challengeresponseauthentication"{print $2; exit}' /tmp/.skifity-sshd)"
  echo "ssh_root=$(awk '$1=="permitrootlogin"{print $2}' /tmp/.skifity-sshd)"
  echo "ssh_pubkey=$(awk '$1=="pubkeyauthentication"{print $2}' /tmp/.skifity-sshd)"
  rm -f /tmp/.skifity-sshd
else
  echo "ssh_password=unknown"
fi
if [ -d /etc/ssh/sshd_config.d ] && grep -Eqi '^[[:space:]]*Include[[:space:]]+/etc/ssh/sshd_config\.d/' /etc/ssh/sshd_config 2>/dev/null; then
  echo "ssh_dropins=yes"
else
  echo "ssh_dropins=no"
fi

if command -v fail2ban-client >/dev/null 2>&1; then
  if systemctl is-active --quiet fail2ban 2>/dev/null; then echo "fail2ban=active"; else echo "fail2ban=inactive"; fi
else
  echo "fail2ban=absent"
fi

UPDATES=absent
if [ -r /etc/apt/apt.conf.d/20auto-upgrades ]; then
  if grep -Eq 'Unattended-Upgrade[^0-9]*"?1' /etc/apt/apt.conf.d/20auto-upgrades; then UPDATES=on; else UPDATES=off; fi
elif systemctl list-unit-files dnf-automatic.timer dnf-automatic-install.timer >/dev/null 2>&1; then
  if systemctl is-active --quiet dnf-automatic.timer 2>/dev/null || systemctl is-active --quiet dnf-automatic-install.timer 2>/dev/null; then
    UPDATES=on
  else
    UPDATES=off
  fi
fi
echo "auto_updates=$UPDATES"

FIREWALL=none
if command -v ufw >/dev/null 2>&1 && $SUDO ufw status 2>/dev/null | head -n1 | grep -qi 'status: active'; then
  FIREWALL=ufw
elif command -v firewall-cmd >/dev/null 2>&1 && $SUDO firewall-cmd --state 2>/dev/null | grep -qi running; then
  FIREWALL=firewalld
elif $SUDO iptables -S INPUT 2>/dev/null | grep -Eq '^-P INPUT (DROP|REJECT)|--dport (80|443) .*-j ACCEPT'; then
  FIREWALL=iptables
fi
echo "firewall=$FIREWALL"

if [ -f /var/run/reboot-required ]; then echo "reboot_required=yes"; else echo "reboot_required=no"; fi
`

// SSHPasswordsOffScript turns off every way into SSH that is not a key, and
// root signing in with a password. It checks the configuration before the
// daemon reloads, and that the change took: a drop-in the main file never
// includes would otherwise be a change that said it happened and had not.
//
// The file is named to sort first, because sshd keeps the first value it
// reads for a setting, and cloud images ship 50-cloud-init.conf turning
// passwords back on.
const SSHPasswordsOffScript = `
set -u
SUDO=""
if [ "$(id -u)" != "0" ]; then SUDO="sudo -n"; fi
SSHD=""
for candidate in /usr/sbin/sshd /usr/local/sbin/sshd /sbin/sshd; do
  if [ -x "$candidate" ]; then SSHD="$candidate"; break; fi
done
if [ -z "$SSHD" ]; then echo "result=no_sshd"; exit 0; fi
if [ "$($SUDO "$SSHD" -T 2>/dev/null | awk '$1=="pubkeyauthentication"{print $2}')" != "yes" ]; then
  echo "result=no_pubkey"; exit 0
fi
if ! grep -Eqi '^[[:space:]]*Include[[:space:]]+/etc/ssh/sshd_config\.d/' /etc/ssh/sshd_config 2>/dev/null; then
  echo "result=no_dropins"; exit 0
fi
DROPIN=/etc/ssh/sshd_config.d/00-skifity-hardening.conf
printf '%s\n' \
  '# Written by Skifity: sign in with a key, never a password.' \
  'PasswordAuthentication no' \
  'KbdInteractiveAuthentication no' \
  'PermitRootLogin prohibit-password' | $SUDO tee "$DROPIN" >/dev/null
if ! $SUDO "$SSHD" -t 2>/dev/null; then
  $SUDO rm -f "$DROPIN"
  echo "result=invalid"; exit 0
fi
if [ "$($SUDO "$SSHD" -T 2>/dev/null | awk '$1=="passwordauthentication"{print $2}')" != "no" ]; then
  $SUDO rm -f "$DROPIN"
  echo "result=not_applied"; exit 0
fi
$SUDO systemctl reload ssh 2>/dev/null || $SUDO systemctl reload sshd 2>/dev/null || $SUDO kill -HUP "$(cat /run/sshd.pid 2>/dev/null)" 2>/dev/null
echo "result=done"
`

// Hardening is what HardeningScript found. An empty value was not found.
type Hardening struct {
	OS             string
	SSHPassword    string // yes, no, unknown
	SSHKeyboard    string // yes, no
	SSHRootLogin   string // yes, no, prohibit-password, without-password, forced-commands-only
	SSHPubkey      string // yes, no
	SSHDropins     bool
	Fail2ban       string // active, inactive, absent
	AutoUpdates    string // on, off, absent
	Firewall       string // ufw, firewalld, iptables, none
	RebootRequired bool
}

// ParseHardening reads HardeningScript's output.
func ParseHardening(output string) Hardening {
	var h Hardening
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		value = strings.ToLower(strings.Trim(strings.TrimSpace(value), `"'`))
		switch key {
		case "os_id":
			h.OS = value
		case "ssh_password":
			h.SSHPassword = value
		case "ssh_kbd":
			h.SSHKeyboard = value
		case "ssh_root":
			h.SSHRootLogin = value
		case "ssh_pubkey":
			h.SSHPubkey = value
		case "ssh_dropins":
			h.SSHDropins = value == "yes"
		case "fail2ban":
			h.Fail2ban = value
		case "auto_updates":
			h.AutoUpdates = value
		case "firewall":
			h.Firewall = value
		case "reboot_required":
			h.RebootRequired = value == "yes"
		}
	}
	return h
}

// Finding levels, worst first.
const (
	HardeningRisk = "risk"
	HardeningWarn = "warn"
	HardeningOK   = "ok"
)

// EvaluateHardening says what each setting means, worst first.
func EvaluateHardening(h Hardening) []api.HardeningFinding {
	var out []api.HardeningFinding
	add := func(code, state, level, detail, fix string) {
		out = append(out, api.HardeningFinding{Code: code, State: state, Level: level, Detail: detail, Fix: fix})
	}

	switch {
	case h.SSHPassword == "" || h.SSHPassword == "unknown":
		add("ssh_passwords", "unknown", HardeningWarn,
			"The SSH daemon's settings could not be read, so whether it takes passwords is not known.",
			"Run `sshd -T | grep -i passwordauthentication` on the server as root.")
	case h.SSHPassword == "yes" || h.SSHKeyboard == "yes":
		add("ssh_passwords", "on", HardeningRisk,
			"SSH accepts passwords, so anybody on the internet can keep guessing one.",
			"Turn SSH passwords off; Skifity signs in with a key, so nothing it does needs them.")
	default:
		add("ssh_passwords", "off", HardeningOK, "SSH accepts keys only.", "")
	}

	switch h.SSHRootLogin {
	case "":
	case "yes":
		add("ssh_root", "password", HardeningRisk,
			"root can sign in over SSH with a password.",
			"Allow root keys only (PermitRootLogin prohibit-password); turning SSH passwords off does that too.")
	case "no":
		add("ssh_root", "none", HardeningOK, "root cannot sign in over SSH.", "")
	default:
		add("ssh_root", "key", HardeningOK, "root can sign in over SSH with a key only.", "")
	}

	switch h.Fail2ban {
	case "active":
		add("fail2ban", "active", HardeningOK, "fail2ban is running and bans addresses that keep failing to sign in.", "")
	case "inactive":
		add("fail2ban", "inactive", HardeningWarn, "fail2ban is installed and not running.",
			"Start it: systemctl enable --now fail2ban.")
	default:
		level := HardeningWarn
		if h.SSHPassword == "no" {
			// Without passwords there is nothing to guess; fail2ban only
			// keeps the logs quieter.
			level = HardeningOK
		}
		add("fail2ban", "absent", level, "fail2ban is not installed.",
			"Install it (apt install fail2ban, or dnf install fail2ban), or turn SSH passwords off, which leaves nothing to guess.")
	}

	switch h.AutoUpdates {
	case "on":
		add("auto_updates", "on", HardeningOK, "Security updates install themselves.", "")
	case "off":
		add("auto_updates", "off", HardeningWarn, "Automatic updates are installed and turned off.",
			"Turn them on: dpkg-reconfigure -plow unattended-upgrades, or enable dnf-automatic-install.timer.")
	default:
		add("auto_updates", "absent", HardeningWarn, "Nothing installs security updates on this server by itself.",
			"Install unattended-upgrades (Debian, Ubuntu) or dnf-automatic (Fedora, RHEL and their kin).")
	}

	if h.Firewall == "" || h.Firewall == "none" {
		add("firewall", "none", HardeningRisk, "No firewall is active, so every port a process opens is open to the internet.",
			"Retry the server's setup, which configures the firewall, or enable ufw with ports 22, 80 and 443 open.")
	} else {
		add("firewall", "active", HardeningOK, "A firewall ("+h.Firewall+") is active.", "")
		out[len(out)-1].Firewall = h.Firewall
	}

	if h.RebootRequired {
		add("reboot", "required", HardeningWarn, "Updates are installed that take effect only after a reboot.",
			"Reboot the server at a quiet moment; with more than one server, one at a time.")
	} else {
		add("reboot", "none", HardeningOK, "No reboot is waiting.", "")
	}

	rank := map[string]int{HardeningRisk: 0, HardeningWarn: 1, HardeningOK: 2}
	sortStable(out, func(a, b api.HardeningFinding) bool { return rank[a.Level] < rank[b.Level] })
	return out
}

// sortStable is a stable insertion sort; the list is a handful long, and the
// order within a level is the order the checks are written in.
func sortStable(list []api.HardeningFinding, less func(a, b api.HardeningFinding) bool) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && less(list[j], list[j-1]); j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

// AuditServer reads a server's hardening over SSH. Nothing on it changes.
func (p *Provisioner) AuditServer(ctx context.Context, serverID string) (api.HardeningReport, error) {
	server, client, err := p.dialServer(ctx, serverID)
	if err != nil {
		return api.HardeningReport{}, err
	}
	defer client.Close()
	return p.audit(ctx, server, client)
}

func (p *Provisioner) audit(ctx context.Context, server store.Server, client *sshx.Client) (api.HardeningReport, error) {
	result, err := client.Run(ctx, HardeningScript)
	if err != nil {
		return api.HardeningReport{}, errdoc.ServerSSHFailed(server.Name, err)
	}
	h := ParseHardening(result.Stdout)
	return api.HardeningReport{
		ServerID:  server.ID,
		CheckedAt: time.Now().UTC(),
		Findings:  EvaluateHardening(h),
		CanTurnOffPasswords: (h.SSHPassword == "yes" || h.SSHKeyboard == "yes" || h.SSHRootLogin == "yes") &&
			h.SSHPubkey == "yes" && h.SSHDropins,
	}, nil
}

// TurnOffSSHPasswords makes SSH take keys only, then audits again.
//
// Safe because the panel is itself signed in with a key while it does this,
// and the script refuses unless the daemon accepts keys and reads the file
// it writes; an existing session is not ended by a reload either way.
func (p *Provisioner) TurnOffSSHPasswords(ctx context.Context, serverID string) (api.HardeningReport, error) {
	server, client, err := p.dialServer(ctx, serverID)
	if err != nil {
		return api.HardeningReport{}, err
	}
	defer client.Close()

	result, err := client.Run(ctx, SSHPasswordsOffScript)
	if err != nil {
		return api.HardeningReport{}, errdoc.ServerSSHFailed(server.Name, err)
	}
	outcome := ""
	for _, line := range strings.Split(result.Stdout, "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "result="); ok {
			outcome = value
		}
	}
	if outcome != "done" {
		return api.HardeningReport{}, errdoc.SSHHardeningRefused(server.Name, outcome)
	}
	return p.audit(ctx, server, client)
}

// dialServer signs in to a server with the key the panel installed on it.
func (p *Provisioner) dialServer(ctx context.Context, serverID string) (store.Server, *sshx.Client, error) {
	server, err := p.db.GetServer(ctx, serverID)
	if err != nil {
		return store.Server{}, nil, err
	}
	if server.SSHKeyEnc == "" {
		return server, nil, errdoc.ServerNotOurs(server.Name, "Checking the server's hardening")
	}
	plaintext, err := p.keyring.Open(server.SSHKeyEnc, serverKeyContext(server.ID))
	if err != nil {
		return server, nil, fmt.Errorf("read this server's stored key: %w", err)
	}
	client, err := sshx.Dial(ctx, sshx.Config{
		Host: server.Host, Port: server.SSHPort, HostKey: server.HostKey,
		Credentials: sshx.Credentials{User: server.SSHUser, PrivateKey: string(plaintext)},
	})
	if err != nil {
		if errors.Is(err, sshx.ErrHostKeyChanged) {
			return server, nil, errdoc.SSHHostKeyChanged(server.Host, server.HostKey, "a different key")
		}
		return server, nil, errdoc.ServerSSHFailed(server.Name, err)
	}
	return server, client, nil
}
