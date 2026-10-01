package cloud_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"skifity/internal/cloud"
	"skifity/internal/cloud/cloudtest"
	"skifity/internal/errdoc"
	"skifity/internal/netguard"
	"skifity/internal/sshx"
)

// These run the Hetzner client against cloudtest's fake, which answers in the
// shapes https://docs.hetzner.cloud/reference/cloud documents. Nothing here
// reaches Hetzner.

func open(t *testing.T, fake *cloudtest.Hetzner, token string) cloud.Provider {
	t.Helper()
	return cloud.NewHetznerAt(token, fake.URL, fake.Client())
}

func codeOf(err error) string {
	var problem *errdoc.Problem
	if errors.As(err, &problem) {
		return problem.Code
	}
	return ""
}

// A token is asked whether it can write, without anything being created.
func TestAReadOnlyTokenIsFoundBeforeItIsSaved(t *testing.T) {
	fake := cloudtest.New(t)

	if err := open(t, fake, cloudtest.Token).Check(t.Context()); err != nil {
		t.Fatalf("a read-write token was refused: %v", err)
	}
	if keys := fake.Keys(); len(keys) != 0 {
		t.Fatalf("checking a token created %d SSH keys", len(keys))
	}

	err := open(t, fake, cloudtest.ReadOnlyToken).Check(t.Context())
	if code := codeOf(err); code != "cloud.token_read_only" {
		t.Errorf("a read-only token answered %q (%v), want cloud.token_read_only", code, err)
	}
	err = open(t, fake, "not-a-token-hetzner-knows").Check(t.Context())
	if code := codeOf(err); code != "cloud.token_invalid" {
		t.Errorf("an unknown token answered %q (%v), want cloud.token_invalid", code, err)
	}

	// The token goes in the Authorization header and nowhere else.
	for _, request := range fake.Requests() {
		if strings.Contains(request.Query, "token") || strings.Contains(request.Path, cloudtest.Token) {
			t.Errorf("the token reached the address: %s %s?%s", request.Method, request.Path, request.Query)
		}
	}
}

// Every page is read, and what cannot be ordered is left out.
func TestTheCatalogueIsWhatCanBeOrdered(t *testing.T) {
	fake := cloudtest.New(t)
	catalogue, err := open(t, fake, cloudtest.Token).Catalogue(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(catalogue.Locations) != 3 {
		t.Errorf("read %d locations over the pages, want 3: %+v", len(catalogue.Locations), catalogue.Locations)
	}

	types := map[string]cloud.ServerType{}
	for _, serverType := range catalogue.ServerTypes {
		types[serverType.Name] = serverType
	}
	if _, ok := types["cx11"]; ok {
		t.Error("a deprecated server type is offered")
	}
	arm, ok := types["cax11"]
	if !ok || arm.Arch != cloud.ArchARM64 || !arm.AvailableIn("fsn1") || arm.AvailableIn("hel1") {
		t.Errorf("cax11 is %+v; want arm64, orderable in fsn1 only", arm)
	}
	x86 := types["cx22"]
	if x86.Arch != cloud.ArchAMD64 || len(x86.Prices) != 2 || x86.Prices[0].Monthly != "4.5100000000" || x86.Prices[0].Currency != "EUR" {
		t.Errorf("cx22 is %+v; want amd64 with a monthly price in each location", x86)
	}

	var images []string
	for _, image := range catalogue.Images {
		images = append(images, image.Name+"/"+image.Arch)
	}
	want := "ubuntu-24.04/amd64 ubuntu-24.04/arm64 debian-12/amd64 debian-12/arm64"
	if strings.Join(images, " ") != want {
		t.Errorf("images offered: %v, want %s", images, want)
	}
}

// A key, a firewall and a machine: each found again rather than made twice.
func TestTheMachineLifecycle(t *testing.T) {
	fake := cloudtest.New(t)
	provider := open(t, fake, cloudtest.Token)
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
	firewall, err := provider.EnsureFirewall(ctx, "skifity-web-1", labels, rules)
	if err != nil {
		t.Fatal(err)
	}
	rules = append(rules, cloud.Rule{Protocol: "tcp", Port: "6443", Sources: []string{"198.51.100.7/32"}, Description: "API"},
		cloud.Rule{Protocol: "udp", Port: "8472", Sources: nil, Description: "nobody to let in"})
	if again, err := provider.EnsureFirewall(ctx, "skifity-web-1", labels, rules); err != nil || again != firewall {
		t.Fatalf("the firewall was not found again: %s, %v", again, err)
	}
	firewalls := fake.Firewalls()
	if len(firewalls) != 1 || len(firewalls[0].Rules) != 2 {
		t.Fatalf("firewalls are %+v; want one, with the two rules that let somebody in", firewalls)
	}

	machine, err := provider.CreateMachine(ctx, cloud.MachineSpec{
		Name: "web-1", Location: "fsn1", Type: "cx22", Image: "ubuntu-24.04",
		SSHKeys: []string{first.Name}, Firewalls: []string{firewall}, Labels: labels, UserData: "#cloud-config\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if machine.Running() {
		t.Error("a machine just ordered reads as running")
	}
	found, ok, err := provider.FindMachine(ctx, map[string]string{"skifity.com/server-id": "srv_1"})
	if err != nil || !ok || found.ID != machine.ID {
		t.Fatalf("the machine was not found by its label: %+v %v %v", found, ok, err)
	}
	if _, ok, _ := provider.FindMachine(ctx, map[string]string{"skifity.com/server-id": "srv_2"}); ok {
		t.Error("another server's label found this machine")
	}
	created := fake.Servers()[0]
	if len(created.SSHKeys) != 1 || len(created.Firewalls) != 1 {
		t.Errorf("the machine was ordered without its key or its firewall: %+v", created)
	}

	if err := provider.DeleteMachine(ctx, machine.ID); err != nil {
		t.Fatal(err)
	}
	if err := provider.DeleteMachine(ctx, machine.ID); err != nil {
		t.Errorf("deleting a machine that is gone failed: %v", err)
	}
	if _, err := provider.Machine(ctx, machine.ID); !errors.Is(err, cloud.ErrNotFound) {
		t.Errorf("reading a deleted machine answered %v, want ErrNotFound", err)
	}
	if err := provider.DeleteFirewall(ctx, firewall); err != nil {
		t.Errorf("deleting the firewall: %v", err)
	}
	if err := provider.DeleteSSHKey(ctx, first.ID); err != nil {
		t.Errorf("deleting the key: %v", err)
	}
}

// The address the panel talks to is not something anybody can set, and the
// client every caller gets would not reach the panel's own machine if it were.
func TestTheGuardedClientRefusesTheFakesAddress(t *testing.T) {
	fake := cloudtest.New(t)
	provider := cloud.NewHetznerAt(cloudtest.Token, fake.URL, nil)
	err := provider.Check(t.Context())
	var blocked *netguard.Blocked
	if !errors.As(err, &blocked) {
		t.Fatalf("a loopback address was dialled through the panel's own client: %v", err)
	}
	if requests := fake.Requests(); len(requests) != 0 {
		t.Errorf("%d requests reached a loopback address", len(requests))
	}
	if code := codeOf(err); code != "cloud.unreachable" {
		t.Errorf("the refusal is %q, want cloud.unreachable", code)
	}
}

// The first boot's configuration: cloud-config, the host key the panel
// generated, and nothing else secret.
func TestTheUserDataCarriesTheHostKeyAndNothingElse(t *testing.T) {
	hostKey, err := sshx.GenerateKeyPair("web-1")
	if err != nil {
		t.Fatal(err)
	}
	userData, err := cloud.UserData(cloud.HostKey{Private: hostKey.PrivateKey, Public: hostKey.PublicKey})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(userData, "#cloud-config\n") {
		t.Fatalf("cloud-init only reads cloud-config that says so on its first line:\n%s", userData)
	}
	if len(userData) > 32<<10 {
		t.Errorf("the user data is %d bytes; Hetzner takes 32 KiB", len(userData))
	}

	var config map[string]any
	if err := yaml.Unmarshal([]byte(userData), &config); err != nil {
		t.Fatalf("the user data is not YAML: %v\n%s", err, userData)
	}
	allowed := map[string]bool{"ssh_deletekeys": true, "ssh_genkeytypes": true, "ssh_keys": true, "ssh_pwauth": true, "disable_root": true}
	for key := range config {
		if !allowed[key] {
			t.Errorf("the user data sets %s, and it should install nothing beyond the host key", key)
		}
	}
	if config["ssh_deletekeys"] != true || config["ssh_pwauth"] != false || config["disable_root"] != false {
		t.Errorf("the SSH settings are %v", config)
	}
	if generated, _ := config["ssh_genkeytypes"].([]any); len(generated) != 0 {
		t.Errorf("cloud-init is asked to generate host keys of its own: %v", generated)
	}
	keys, _ := config["ssh_keys"].(map[string]any)
	if len(keys) != 2 {
		t.Fatalf("ssh_keys is %v; want the ed25519 pair only", keys)
	}
	private, _ := keys["ed25519_private"].(string)
	public, _ := keys["ed25519_public"].(string)
	if strings.TrimSpace(private) != strings.TrimSpace(hostKey.PrivateKey) || public != hostKey.PublicKey {
		t.Fatal("the host key in the user data is not the one generated")
	}
	derived, err := sshx.PublicKeyOf(private, "")
	if err != nil || !strings.HasPrefix(hostKey.PublicKey, derived) {
		t.Fatalf("the private key in the user data does not match its public half: %v", err)
	}
	if strings.Count(userData, "PRIVATE KEY-----") != 2 {
		t.Errorf("the user data holds more than the one private key")
	}

	if _, err := cloud.UserData(cloud.HostKey{Private: "-----BEGIN RSA PRIVATE KEY-----", Public: "ssh-rsa AAAA"}); err == nil {
		t.Error("a key other than ed25519 was accepted")
	}
}

// A refusal is Hetzner's own words in the panel's shape.
func TestAProviderRefusalIsAProblem(t *testing.T) {
	fake := cloudtest.New(t)
	provider := open(t, fake, cloudtest.Token)
	_, err := provider.CreateMachine(t.Context(), cloud.MachineSpec{Name: "web-1"})
	var refused *cloud.APIError
	if !errors.As(err, &refused) || refused.Status != http.StatusUnprocessableEntity || refused.Code != "invalid_input" {
		t.Fatalf("an invalid order answered %v", err)
	}
	if code := codeOf(err); code != "cloud.request_failed" {
		t.Errorf("the problem is %q, want cloud.request_failed", code)
	}
}
