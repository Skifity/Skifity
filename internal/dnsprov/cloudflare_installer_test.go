package dnsprov

import (
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestTheInstallerKnowsTheSameCloudflareAddressesAsThePanel: the installer says
// "your domain is behind Cloudflare's proxy" instead of "your record is wrong"
// from its own copy of the list, because it runs before any panel exists. The
// two lists have to be one list.
func TestTheInstallerKnowsTheSameCloudflareAddressesAsThePanel(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "installer", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^CLOUDFLARE_RANGES="([^"]*)"`).FindSubmatch(script)
	if match == nil {
		t.Fatal("install.sh has no CLOUDFLARE_RANGES")
	}
	installer := map[string]bool{}
	for _, cidr := range strings.Fields(string(match[1])) {
		installer[cidr] = true
	}

	panel := map[string]bool{}
	for _, prefix := range cloudflareRanges {
		if prefix.Addr().Is4() {
			panel[prefix.String()] = true
		}
	}
	for cidr := range panel {
		if !installer[cidr] {
			t.Errorf("the panel knows Cloudflare's %s and install.sh does not", cidr)
		}
	}
	for cidr := range installer {
		if _, err := netip.ParsePrefix(cidr); err != nil {
			t.Errorf("install.sh lists %q, which is not a network", cidr)
		}
		if !panel[cidr] {
			t.Errorf("install.sh knows Cloudflare's %s and the panel does not", cidr)
		}
	}
}
