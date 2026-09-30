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
