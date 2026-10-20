package netguard

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWhatIsRefusedAndWhatIsNot(t *testing.T) {
	cases := []struct {
		ip      string
		allowed bool
		why     string
	}{
		// The one that matters. Every cloud provider serves this address, and
		// what it serves is the machine's own credentials.
		{"169.254.169.254", false, "the cloud metadata service"},
		{"169.254.1.1", false, "link-local"},
		{"fe80::1", false, "link-local, v6"},
		{"127.0.0.1", false, "the panel itself"},
		{"::1", false, "the panel itself, v6"},
		{"0.0.0.0", false, "the unspecified address"},
		{"224.0.0.1", false, "multicast"},
		// The metadata services that are not on the link-local range.
		{"fd00:ec2::254", false, "AWS's metadata service over IPv6"},
		{"100.100.100.200", false, "Alibaba Cloud's metadata service"},
		{"192.0.0.192", false, "Oracle Cloud's metadata service"},

		// Allowed on purpose. A self-hosted Gitea on a private network and a
		// webhook receiver on the same LAN are what this product is for;
		// refusing them to look careful would break the ordinary case.
		{"10.0.0.5", true, "a private Git server"},
		{"192.168.1.10", true, "a webhook receiver on the LAN"},
		{"172.16.4.4", true, "a private address"},
		{"fd00::1", true, "a unique-local address"},
		{"140.82.121.4", true, "github.com"},
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if ip == nil {
			t.Fatalf("%q is not an address", c.ip)
		}
		if got := Allowed(ip); got != c.allowed {
			t.Errorf("Allowed(%s) = %v, want %v — %s", c.ip, got, c.allowed, c.why)
		}
	}

	// An address that could not be parsed is refused rather than guessed at.
	if Allowed(nil) {
		t.Error("an unparseable address was allowed")
	}
}

func TestTheClientRefusesToReachTheMetadataService(t *testing.T) {
	// The check runs on the address being dialled, so this holds however the
	// name got there: a setting, a redirect, or a name that resolves to it.
	client := Client(5 * time.Second)

	resp, err := client.Get("http://169.254.169.254/latest/meta-data/")
	if err == nil {
		resp.Body.Close()
		t.Fatal("the panel connected to the cloud metadata service")
	}
	var blocked *Blocked
	if !errors.As(err, &blocked) {
		t.Fatalf("the request failed for the wrong reason: %v", err)
	}
	if !strings.Contains(blocked.Error(), "credentials") {
		t.Errorf("the refusal does not say why it matters: %v", blocked)
	}
}

func TestTheClientStillReachesAnOrdinaryServer(t *testing.T) {
	// A guard that refuses everything is a guard nobody keeps. httptest listens
	// on loopback, which is refused on purpose, so this checks the allowed path
	// through the dialler rather than through a server.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if resp, err := Client(5 * time.Second).Get(server.URL); err == nil {
		resp.Body.Close()
		t.Fatal("loopback was reachable, so the guard is not on the client at all")
	}

	// And the rule that decides it is the one above: a public address passes.
	if !Allowed(net.ParseIP("140.82.121.4")) {
		t.Error("a public address is refused, which would break every hosted provider")
	}
}

func TestARefusalNamesTheAddress(t *testing.T) {
	// An operator who meant to reach a private service needs to see what was
	// refused, not a timeout.
	for _, ip := range []string{"169.254.169.254", "127.0.0.1", "0.0.0.0"} {
		err := (&Blocked{IP: net.ParseIP(ip)}).Error()
		if !strings.Contains(err, ip) {
			t.Errorf("the refusal for %s does not name it: %s", ip, err)
		}
	}
}

// The shape of an address, separately from where it points.
func TestAddressAcceptsAddressesAndNothingElse(t *testing.T) {
	for _, ok := range []string{
		"https://catalogue.example.com/index.yaml",
		"https://git.example.com:8443/api/v4/projects?per_page=100&page=2",
		"http://127.0.0.1:43211/manifest.json",
		"https://[2001:db8::1]/plugin.yaml",
		"https://example.com",
	} {
		if !Address.MatchString(ok) {
			t.Errorf("%s was refused", ok)
		}
	}
	for _, bad := range []string{
		"file:///etc/passwd",
		"https://user:pass@example.com/",
		"https://example.com/a b",
		"https://example.com/#fragment",
		"gopher://example.com/",
		"https:///nohost",
		"https://-example.com/",
		"https://example.com\n.evil/",
	} {
		if Address.MatchString(bad) {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// A team admin sets the address of a webhook, a Git host, a secret manager, a
// registry and a template catalogue. Every environment's namespace lets the
// panel in, so an address inside the cluster would be the panel reaching, on
// one team's behalf, another team's Services that nothing else may reach.
func TestATeamsAddressMayNotBeInsideTheCluster(t *testing.T) {
	cases := []struct {
		ip      string
		allowed bool
		why     string
	}{
		{"10.42.0.17", false, "a pod"},
		{"10.42.255.254", false, "a pod on another server"},
		{"10.43.0.1", false, "the Kubernetes API's Service"},
		{"10.43.200.9", false, "another team's Service"},
		{"169.254.169.254", false, "still the metadata service"},
		{"127.0.0.1", false, "still the panel itself"},

		{"10.0.0.5", true, "a private Git server on the LAN"},
		{"10.41.255.255", true, "just below the pod network"},
		{"10.44.0.1", true, "just above the Service network"},
		{"192.168.1.10", true, "a webhook receiver on the LAN"},
		{"140.82.121.4", true, "github.com"},
	}
	for _, c := range cases {
		if got := TenantAllowed(net.ParseIP(c.ip)); got != c.allowed {
			t.Errorf("TenantAllowed(%s) = %v, want %v — %s", c.ip, got, c.allowed, c.why)
		}
	}

	// The administrator's own settings are not held to it: the panel's own
	// components live in the cluster.
	if !Allowed(net.ParseIP("10.43.0.1")) {
		t.Error("an address inside the cluster is refused for the administrator too")
	}
}

func TestTheTenantClientRefusesAServiceInsideTheCluster(t *testing.T) {
	resp, err := TenantClient(5 * time.Second).Get("http://10.43.0.1:443/api")
	if err == nil {
		resp.Body.Close()
		t.Fatal("the panel connected to an address inside the cluster for a team")
	}
	var blocked *Blocked
	if !errors.As(err, &blocked) {
		t.Fatalf("the request failed for the wrong reason: %v", err)
	}
	if !strings.Contains(blocked.Error(), "inside this cluster") {
		t.Errorf("the refusal does not say why: %v", blocked)
	}

	// The dialler a mail server is reached through holds to the same rule.
	if _, err := TenantDialer(time.Second).Dial("tcp", "10.42.3.4:25"); !errors.As(err, &blocked) {
		t.Errorf("the tenant dialler reached a pod: %v", err)
	}
}
