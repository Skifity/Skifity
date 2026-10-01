package cloud

import (
	"fmt"
	"strings"

	"sigs.k8s.io/yaml"
)

// The first boot of a machine the panel creates.
//
// What it does is small on purpose. Installing k3s is not in it: that is the
// same SSH join every other server goes through, so there is one way a node is
// built and one place it can go wrong. What it does do is the part every
// competitor leaves out — it gives the machine an SSH host key the panel
// generated, so the panel's very first connection is checked against a key it
// already knows rather than trusting whatever answers (see ADR-0025).
//
// cloud-init's cc_ssh module, which every image Hetzner ships runs before sshd
// starts, deletes the image's host keys (ssh_deletekeys) and writes the ones in
// ssh_keys instead of generating its own:
// https://cloudinit.readthedocs.io/en/latest/reference/modules.html#ssh.
//
// The private half is in the user data, and the user data is not private: the
// provider stores it, and anything on the machine can read it back from the
// metadata service at 169.254.169.254 for as long as the machine exists —
// including, once k3s is on it, a pod. So the key is a bootstrap key. The panel
// uses it for one connection, over which it has the machine generate a key of
// its own that never leaves it, pins that one, and never accepts the first
// again. The alternative — reading the key the machine generated from its
// console output — is not something Hetzner's API offers, and a machine
// reporting its key back over the network needs a secret in the same user data
// to be trusted, which is the same exposure with an extra service attached.

// HostKey is the SSH host key a new machine starts with.
type HostKey struct {
	// Private is the OpenSSH-format private key.
	Private string
	// Public is the authorized_keys-format public key.
	Public string
}

// cloudConfig is the part of cloud-init's schema this writes:
// https://cloudinit.readthedocs.io/en/latest/reference/modules.html.
type cloudConfig struct {
	// SSHDeleteKeys removes whatever host keys the image came with.
	SSHDeleteKeys bool `json:"ssh_deletekeys"`
	// SSHGenKeyTypes is empty so nothing is generated beside the one given.
	SSHGenKeyTypes []string `json:"ssh_genkeytypes"`
	// SSHKeys are the host keys to install.
	SSHKeys map[string]string `json:"ssh_keys"`
	// SSHPasswordAuth false turns SSH passwords off: the panel signs in with
	// its key, and root has no password to guess.
	SSHPasswordAuth bool `json:"ssh_pwauth"`
	// DisableRoot false keeps root's key working. The panel's key is put on
	// root, and a Hetzner image signs in as root.
	DisableRoot bool `json:"disable_root"`
}

// UserData renders the cloud-config a new machine starts with. The host key
// is the only secret in it, and see above for why that one is acceptable.
func UserData(hostKey HostKey) (string, error) {
	private := strings.TrimSpace(hostKey.Private)
	public := strings.TrimSpace(hostKey.Public)
	if !strings.HasPrefix(private, "-----BEGIN OPENSSH PRIVATE KEY-----") || !strings.HasPrefix(public, "ssh-ed25519 ") {
		return "", fmt.Errorf("the host key must be an ed25519 key in OpenSSH form")
	}
	config := cloudConfig{
		SSHDeleteKeys:  true,
		SSHGenKeyTypes: []string{},
		SSHKeys: map[string]string{
			"ed25519_private": private + "\n",
			"ed25519_public":  public,
		},
		SSHPasswordAuth: false,
		DisableRoot:     false,
	}
	body, err := yaml.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("write the cloud-config: %w", err)
	}
	// cloud-init reads a document as cloud-config only when its first line
	// says so.
	return "#cloud-config\n" +
		"# Written by Skifity. Replaces the image's SSH host keys with one the panel\n" +
		"# generated, so the panel's first connection is to a machine it can recognise.\n" +
		string(body), nil
}
