package kube

import (
	"context"
	"errors"
	"testing"

	"skifity/internal/netguard"
)

// TestAManifestIsNotDownloadedFromAnywhereThePanelShouldNotReach: the URL a
// component's manifest comes from is a setting, and what comes back is applied
// to the cluster as Kubernetes objects. Of the three settings that become a
// request the panel's own process makes, this is the one that matters most —
// and it was the one not going through internal/netguard.
func TestAManifestIsNotDownloadedFromAnywhereThePanelShouldNotReach(t *testing.T) {
	client := &Client{}
	for _, address := range []string{
		"http://169.254.169.254/latest/meta-data/iam/security-credentials/",
		"https://169.254.169.254/computeMetadata/v1/instance/service-accounts/",
		"http://127.0.0.1:8080/api/settings",
		"http://localhost:6443/api/v1/secrets",
	} {
		err := client.ApplyManifestURL(context.Background(), address)
		if err == nil {
			t.Errorf("%s was downloaded", address)
			continue
		}
		var blocked *netguard.Blocked
		if !errors.As(err, &blocked) {
			t.Errorf("%s failed with %v, which is not netguard refusing it", address, err)
		}
	}

	// A scheme that is not http is refused before anything is dialled, with a
	// sentence rather than whatever net/http says about an unsupported scheme.
	for _, address := range []string{"file:///etc/shadow", "gopher://example.test/", "://not a url"} {
		if err := client.ApplyManifestURL(context.Background(), address); err == nil {
			t.Errorf("%s was accepted as a manifest address", address)
		}
	}
}
