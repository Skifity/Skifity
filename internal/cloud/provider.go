// Package cloud creates and deletes machines at a cloud provider, for the one
// thing the panel wants a machine for: joining it to the cluster.
//
// It is deliberately small. A provider here does five things — prove a token
// works, say what can be ordered, take a public key, keep a firewall in front
// of a machine, and create, read and delete the machine — and nothing that
// follows is the provider's: installing k3s is the same SSH join every other
// server goes through (internal/provision), so there is one way a node comes
// to exist and one way it is checked.
//
// Hetzner Cloud is the only provider. The interface is shaped so DigitalOcean
// (droplets, account SSH keys, cloud firewalls, tags) and Vultr (instances,
// SSH keys, firewall groups, tags) fit it without a change here: each has the
// same five nouns under other names.
package cloud

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// KindHetzner is Hetzner Cloud.
const KindHetzner = "hetzner"

// Kinds are the providers this build can talk to, in the order a form offers
// them.
var Kinds = []string{KindHetzner}

// Title is a provider's name as a person writes it.
func Title(kind string) string {
	switch kind {
	case KindHetzner:
		return "Hetzner Cloud"
	}
	return kind
}

// Supported reports whether this build has a provider of that kind.
func Supported(kind string) bool { return slices.Contains(Kinds, kind) }

// ErrNotFound is a machine, a key or a firewall the provider no longer has.
var ErrNotFound = errors.New("the provider has no such resource")

// Provider is one account's API at one cloud, opened with its token.
type Provider interface {
	// Kind is the provider's kind, such as "hetzner".
	Kind() string
	// Check proves the token is accepted and can create machines. A token
	// that can only read is refused here rather than at the first create.
	Check(ctx context.Context) error
	// Catalogue is what can be ordered: locations, machine types with their
	// prices, and images.
	Catalogue(ctx context.Context) (Catalogue, error)
	// ImportSSHKey makes a public key usable for new machines. Importing a key
	// the account already has returns that one.
	ImportSSHKey(ctx context.Context, name, publicKey string, labels map[string]string) (SSHKey, error)
	// DeleteSSHKey removes an imported key. Machines it was put on keep it.
	DeleteSSHKey(ctx context.Context, id string) error
	// EnsureFirewall finds the firewall carrying labels, or creates it, and
	// sets its rules to exactly these.
	EnsureFirewall(ctx context.Context, name string, labels map[string]string, rules []Rule) (string, error)
	// SetFirewallRules replaces a firewall's rules.
	SetFirewallRules(ctx context.Context, id string, rules []Rule) error
	// DeleteFirewall removes a firewall that nothing is behind any more.
	DeleteFirewall(ctx context.Context, id string) error
	// CreateMachine orders a machine. It answers once the order is accepted,
	// usually before the machine is running.
	CreateMachine(ctx context.Context, spec MachineSpec) (Machine, error)
	// Machine reads one machine. A machine that is gone is ErrNotFound.
	Machine(ctx context.Context, id string) (Machine, error)
	// FindMachine finds the machine carrying every one of labels.
	FindMachine(ctx context.Context, labels map[string]string) (Machine, bool, error)
	// DeleteMachine deletes a machine and everything on its disk.
	DeleteMachine(ctx context.Context, id string) error
}

// Opener opens a provider of a kind with a token. The panel's own is Open; a
// test passes one that points at a fake.
type Opener func(kind, token string) (Provider, error)

// Open is the Opener the panel uses: the provider's real API, through
// internal/netguard.
func Open(kind, token string) (Provider, error) {
	switch kind {
	case KindHetzner:
		return NewHetzner(token), nil
	}
	return nil, fmt.Errorf("there is no cloud provider called %q", kind)
}

// Catalogue is what a provider sells, as the create form offers it.
type Catalogue struct {
	Locations   []Location   `json:"locations"`
	ServerTypes []ServerType `json:"server_types"`
	Images      []Image      `json:"images"`
}

// Location is a data centre, or a region.
type Location struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	City        string `json:"city"`
	Country     string `json:"country"`
}

// Architectures, spelled the way Kubernetes and the preflight check spell them.
const (
	ArchAMD64 = "amd64"
	ArchARM64 = "arm64"
)

// ServerType is a size of machine.
type ServerType struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Cores       int     `json:"cores"`
	MemoryGB    float64 `json:"memory_gb"`
	DiskGB      int     `json:"disk_gb"`
	// Arch is amd64 or arm64. k3s and the panel's own images are built for
	// both; see installer/install.sh and the preflight's architecture check.
	Arch string `json:"arch"`
	// CPUType is shared or dedicated.
	CPUType string `json:"cpu_type"`
	// Locations are where it can be ordered now.
	Locations []string `json:"locations"`
	Prices    []Price  `json:"prices"`
}

// Price is what a type costs in one location, VAT included, as the provider
// writes it: a decimal string, so nothing is lost to a float.
type Price struct {
	Location string `json:"location"`
	Monthly  string `json:"monthly"`
	Hourly   string `json:"hourly"`
	Currency string `json:"currency"`
}

// AvailableIn reports whether the type can be ordered in a location.
func (t ServerType) AvailableIn(location string) bool {
	return slices.Contains(t.Locations, location)
}

// Image is an operating system to install.
type Image struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Arch        string `json:"arch"`
}

// SSHKey is a public key the provider holds.
type SSHKey struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Rule lets one kind of traffic in. Every rule is inbound: a provider's
// firewall with no outbound rules lets everything out, which a node needs to
// download k3s and pull images.
type Rule struct {
	Protocol string `json:"protocol"`
	// Port is one port, or a range written "from-to".
	Port string `json:"port"`
	// Sources are CIDRs. 0.0.0.0/0 and ::/0 together are anywhere.
	Sources     []string `json:"sources"`
	Description string   `json:"description"`
}

// Anywhere is every address, in both families.
var Anywhere = []string{"0.0.0.0/0", "::/0"}

// MachineSpec is a machine to order.
type MachineSpec struct {
	Name     string
	Location string
	Type     string
	Image    string
	// UserData is the cloud-config the machine starts with. See UserData.
	UserData string
	// SSHKeys are provider key names or ids, put on root.
	SSHKeys []string
	// Firewalls are provider firewall ids, applied from the first boot.
	Firewalls []string
	Labels    map[string]string
}

// Machine is a machine as the provider reports it.
type Machine struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Status string            `json:"status"`
	IPv4   string            `json:"ipv4"`
	IPv6   string            `json:"ipv6"`
	Labels map[string]string `json:"labels"`
}

// Running reports whether the machine is up with an address to reach it on.
func (m Machine) Running() bool { return m.Status == "running" && m.IPv4 != "" }
