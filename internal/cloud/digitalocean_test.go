package cloud_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"skifity/internal/cloud"
	"skifity/internal/cloud/cloudtest"
	"skifity/internal/netguard"
	"skifity/internal/sshx"
)

// These run the DigitalOcean client against cloudtest's fake, which answers
// in the shapes https://docs.digitalocean.com/reference/api/digitalocean/
// documents. Nothing here reaches DigitalOcean.

func openDO(t *testing.T, fake *cloudtest.DigitalOcean, token string) cloud.Provider {
	t.Helper()
	return cloud.NewDigitalOceanAt(token, fake.URL, fake.Client())
}

// A token is asked whether it can write, without anything being created.
func TestADigitalOceanTokenThatCannotWriteIsFoundBeforeItIsSaved(t *testing.T) {
	fake := cloudtest.NewDigitalOcean(t)

	if err := openDO(t, fake, cloudtest.Token).Check(t.Context()); err != nil {
		t.Fatalf("a token that can write was refused: %v", err)
	}
	if keys := fake.Keys(); len(keys) != 0 {
		t.Fatalf("checking a token created %d SSH keys", len(keys))
	}
	if code := codeOf(openDO(t, fake, cloudtest.ReadOnlyToken).Check(t.Context())); code != "cloud.token_read_only" {
		t.Errorf("a read-only token answered %q, want cloud.token_read_only", code)
	}
	if code := codeOf(openDO(t, fake, "not-a-token-digitalocean-knows").Check(t.Context())); code != "cloud.token_invalid" {
		t.Errorf("an unknown token answered %q, want cloud.token_invalid", code)
	}
	for _, request := range fake.Requests() {
		if strings.Contains(request.Query, cloudtest.Token) || strings.Contains(request.Path, cloudtest.Token) {
			t.Errorf("the token reached the address: %s %s?%s", request.Method, request.Path, request.Query)
		}
	}
}

// Every page is read, what cannot be ordered is left out, and the images are
// the panel's names for DigitalOcean's.
func TestTheDigitalOceanCatalogueIsWhatCanBeOrdered(t *testing.T) {
	fake := cloudtest.NewDigitalOcean(t)
	catalogue, err := openDO(t, fake, cloudtest.Token).Catalogue(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	var regions []string
	for _, location := range catalogue.Locations {
		regions = append(regions, location.Name)
	}
	if strings.Join(regions, " ") != "ams3 fra1 nyc1" {
		t.Errorf("regions offered: %v; want the three available ones, read over two pages", regions)
	}
	if amsterdam := catalogue.Locations[0]; amsterdam.City != "Amsterdam" || amsterdam.Country != "NL" {
		t.Errorf("ams3 is %+v; want Amsterdam, NL", amsterdam)
	}

	sizes := map[string]cloud.ServerType{}
	for _, size := range catalogue.ServerTypes {
		sizes[size.Name] = size
	}
	for _, gone := range []string{"s-1vcpu-512mb-10gb", "gpu-h100x1-80gb"} {
		if _, ok := sizes[gone]; ok {
			t.Errorf("%s is offered, and cannot be ordered or is not a node", gone)
		}
	}
	basic := sizes["s-1vcpu-1gb"]
	if basic.Arch != cloud.ArchAMD64 || basic.CPUType != "shared" || basic.MemoryGB != 1 ||
		!basic.AvailableIn("fra1") || basic.AvailableIn("nyc1") {
		t.Errorf("s-1vcpu-1gb is %+v; want shared amd64 with 1 GB, in ams3 and fra1", basic)
	}
	if len(basic.Prices) != 2 || basic.Prices[0].Monthly != "6.00" || basic.Prices[0].Currency != "USD" {
		t.Errorf("s-1vcpu-1gb costs %+v; want 6.00 USD a month in each region", basic.Prices)
	}
	if sizes["g-2vcpu-8gb"].CPUType != "dedicated" {
		t.Errorf("a General Purpose droplet is %q, want dedicated", sizes["g-2vcpu-8gb"].CPUType)
	}

	var images []string
	for _, image := range catalogue.Images {
		images = append(images, image.Name+"/"+image.Arch)
	}
	if strings.Join(images, " ") != "ubuntu-24.04/amd64 debian-12/amd64" {
		t.Errorf("images offered: %v; want the supported two, in the panel's names", images)
	}
}

// A key, a firewall and a droplet: each found again rather than made twice,
// and the droplet behind its firewall from its first boot.
func TestADigitalOceanMachinesLifecycle(t *testing.T) {
	fake := cloudtest.NewDigitalOcean(t)
	provider := openDO(t, fake, cloudtest.Token)
	ctx := t.Context()
	labels := map[string]string{"skifity.com/managed": "true", "skifity.com/server-id": "srv_1"}

	pair, err := sshx.GenerateKeyPair("panel")
	if err != nil {
		t.Fatal(err)
	}
	first, err := provider.ImportSSHKey(ctx, "skifity-srv_1", pair.PublicKey, labels)
	if err != nil {
		t.Fatal(err)
	}
	again, err := provider.ImportSSHKey(ctx, "skifity-srv_1", pair.PublicKey, labels)
	if err != nil || again.ID != first.ID {
		t.Fatalf("importing the same key again gave %+v, %v; want %+v", again, err, first)
	}

	rules := []cloud.Rule{{Protocol: "tcp", Port: "22", Sources: cloud.Anywhere, Description: "SSH"}}
	firewall, err := provider.EnsureFirewall(ctx, "skifity-web_1-abc123", labels, rules)
	if err != nil {
		t.Fatal(err)
	}
	rules = append(rules, cloud.Rule{Protocol: "tcp", Port: "6443", Sources: []string{"198.51.100.7/32"}},
		cloud.Rule{Protocol: "udp", Port: "8472", Sources: nil, Description: "nobody to let in"},
		cloud.Rule{Protocol: "icmp", Sources: cloud.Anywhere})
	if again, err := provider.EnsureFirewall(ctx, "skifity-web_1-abc123", labels, rules); err != nil || again != firewall {
		t.Fatalf("the firewall was not found again: %s, %v", again, err)
	}
	firewalls := fake.Firewalls()
	if len(firewalls) != 1 || len(firewalls[0].InboundRules) != 3 {
		t.Fatalf("firewalls are %+v; want one, with the three rules that let somebody in", firewalls)
	}
	if firewalls[0].Name != "skifity-web-1-abc123" {
		t.Errorf("the firewall is called %q; DigitalOcean takes letters, digits, dashes and dots", firewalls[0].Name)
	}
	// A DigitalOcean firewall with no outbound rules lets nothing out, and a
	// node has to download k3s.
	if len(firewalls[0].OutboundRules) == 0 {
		t.Fatal("the firewall lets nothing out")
	}
	if icmp := firewalls[0].InboundRules[2]; icmp["ports"] != nil {
		t.Errorf("an ICMP rule names ports: %v", icmp)
	}

	machine, err := provider.CreateMachine(ctx, cloud.MachineSpec{
		Name: "web-1", Location: "fra1", Type: "s-1vcpu-1gb", Image: "ubuntu-24.04",
		SSHKeys: []string{first.Name}, Firewalls: []string{firewall}, Labels: labels, UserData: "#cloud-config\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if machine.Running() {
		t.Error("a droplet just ordered reads as running")
	}
	droplet := fake.Droplets()[0]
	if droplet.Image != "ubuntu-24-04-x64" || len(droplet.SSHKeys) != 1 || !droplet.IPv6 {
		t.Errorf("the droplet was ordered as %+v", droplet)
	}
	if !slices.Contains(droplet.Tags, firewalls[0].Tags[0]) {
		t.Errorf("the droplet's tags %v do not put it behind its firewall %v", droplet.Tags, firewalls[0].Tags)
	}
	for _, tag := range firewalls[0].Tags {
		if strings.Contains(tag, "managed") {
			t.Errorf("the firewall reaches every managed droplet through %q", tag)
		}
	}

	// The first read finds it booting; the fake brings it up on the second.
	if _, err := provider.Machine(ctx, machine.ID); err != nil {
		t.Fatal(err)
	}
	running, err := provider.Machine(ctx, machine.ID)
	if err != nil || !running.Running() || running.IPv4 != "198.51.100.60" {
		t.Fatalf("the droplet did not come up with its public address: %+v, %v", running, err)
	}
	if running.Labels["skifity.com/server-id"] != "srv_1" || running.Labels["skifity.com/managed"] != "true" {
		t.Errorf("the labels did not come back from the tags: %v", running.Labels)
	}

	found, ok, err := provider.FindMachine(ctx, map[string]string{"skifity.com/server-id": "srv_1"})
	if err != nil || !ok || found.ID != machine.ID {
		t.Fatalf("the droplet was not found by its label: %+v %v %v", found, ok, err)
	}
	if _, ok, _ := provider.FindMachine(ctx, map[string]string{"skifity.com/server-id": "srv_2"}); ok {
		t.Error("another server's label found this droplet")
	}

	if err := provider.DeleteMachine(ctx, machine.ID); err != nil {
		t.Fatal(err)
	}
	if err := provider.DeleteMachine(ctx, machine.ID); err != nil {
		t.Errorf("deleting a droplet that is gone failed: %v", err)
	}
	if _, err := provider.Machine(ctx, machine.ID); !errors.Is(err, cloud.ErrNotFound) {
		t.Errorf("reading a deleted droplet answered %v, want ErrNotFound", err)
	}
	if err := provider.DeleteFirewall(ctx, firewall); err != nil {
		t.Errorf("deleting the firewall: %v", err)
	}
	if err := provider.DeleteSSHKey(ctx, first.ID); err != nil {
		t.Errorf("deleting the key: %v", err)
	}
}

// DigitalOcean's refusals become the panel's problems.
func TestADigitalOceanRefusalIsAProblem(t *testing.T) {
	fake := cloudtest.NewDigitalOcean(t)
	provider := openDO(t, fake, cloudtest.Token)
	ctx := t.Context()

	_, err := provider.CreateMachine(ctx, cloud.MachineSpec{Name: "web-1", Location: "fra1", Type: "g-2vcpu-8gb", Image: "debian-12"})
	if code := codeOf(err); code != "cloud.unavailable" {
		t.Errorf("a size not sold in the region answered %q (%v), want cloud.unavailable", code, err)
	}
	fake.DropletLimit = 0
	if _, err := provider.CreateMachine(ctx, cloud.MachineSpec{Name: "web-1", Location: "fra1", Type: "s-1vcpu-1gb", Image: "debian-12"}); err != nil {
		t.Fatal(err)
	}
	fake.DropletLimit = 1
	_, err = provider.CreateMachine(ctx, cloud.MachineSpec{Name: "web-2", Location: "fra1", Type: "s-1vcpu-1gb", Image: "debian-12"})
	if code := codeOf(err); code != "cloud.limit_reached" {
		t.Errorf("an account at its droplet limit answered %q (%v), want cloud.limit_reached", code, err)
	}
	if _, err := provider.CreateMachine(ctx, cloud.MachineSpec{Name: "web-3", Location: "fra1", Type: "s-1vcpu-1gb", Image: "centos-7"}); err == nil {
		t.Error("an image the panel does not install was ordered")
	}
	// A label that is not the panel's own cannot be written as a tag and
	// read back as itself, so it is refused rather than mangled.
	if _, err := provider.CreateMachine(ctx, cloud.MachineSpec{Name: "web-4", Location: "fra1", Type: "s-1vcpu-1gb",
		Image: "debian-12", Labels: map[string]string{"example.com/team": "a b"}}); err == nil {
		t.Error("a label that cannot be a tag was written as one")
	}
}

// The address the panel talks to is not something anybody can set, and the
// client every caller gets would not reach the panel's own machine if it were.
func TestTheGuardedClientRefusesTheDigitalOceanFakesAddress(t *testing.T) {
	fake := cloudtest.NewDigitalOcean(t)
	err := cloud.NewDigitalOceanAt(cloudtest.Token, fake.URL, nil).Check(t.Context())
	var blocked *netguard.Blocked
	if !errors.As(err, &blocked) {
		t.Fatalf("a loopback address was dialled through the panel's own client: %v", err)
	}
	if requests := fake.Requests(); len(requests) != 0 {
		t.Errorf("%d requests reached a loopback address", len(requests))
	}
}

// Both providers are offered, under their own names.
func TestBothProvidersAreOffered(t *testing.T) {
	for _, kind := range []string{cloud.KindHetzner, cloud.KindDigitalOcean} {
		if !cloud.Supported(kind) {
			t.Errorf("%s is not offered", kind)
		}
		if _, err := cloud.Open(kind, "a-token"); err != nil {
			t.Errorf("%s cannot be opened: %v", kind, err)
		}
	}
	if cloud.Title(cloud.KindDigitalOcean) != "DigitalOcean" {
		t.Errorf("DigitalOcean is called %q", cloud.Title(cloud.KindDigitalOcean))
	}
}
