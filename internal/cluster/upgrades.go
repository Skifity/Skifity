package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"skifity/internal/api"
	"skifity/internal/builder"
	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/settings"
	"skifity/internal/store"
)

// Upgrading what the installer installed: the components, and k3s.
//
// A component was installed once and never touched again, and nothing
// recorded which version that was. Now each records the version it was
// installed at, the panel says when the version it would install today is
// newer, and upgrading one applies that version's manifest over it — which is
// how each of these projects documents its own upgrade.

// UpgradeComponent is Rancher's system-upgrade-controller, which upgrades k3s.
const UpgradeComponent = "system-upgrade"

// The controller's release, and the manifests it ships.
const (
	upgradeControllerVersion = "v0.20.2"
	upgradeCRDURL            = "https://github.com/rancher/system-upgrade-controller/releases/download/" + upgradeControllerVersion + "/crd.yaml"
	upgradeControllerURL     = "https://github.com/rancher/system-upgrade-controller/releases/download/" + upgradeControllerVersion + "/system-upgrade-controller.yaml"
)

// k3sChannelsURL lists the latest k3s release of every minor version.
const k3sChannelsURL = "https://update.k3s.io/v1-release/channels"

var versionInURL = regexp.MustCompile(`v?(\d+\.\d+(?:\.\d+)?)`)

// ComponentVersion is the version of a component this panel installs now:
// from the manifest URL it would apply, or from the image it would run. Empty
// for one that has no version of its own, such as the firewall, which runs
// the panel's own image and moves with it.
func (c *Cluster) ComponentVersion(ctx context.Context, name string) string {
	switch name {
	case "registry":
		return imageTag(registryImage)
	case "buildkit":
		return builder.BuildKitVersion
	case "cloudflare-tunnel":
		return imageTag(TunnelImage)
	case UpgradeComponent:
		return strings.TrimPrefix(upgradeControllerVersion, "v")
	}
	url, err := c.manifestURL(ctx, name)
	if err != nil {
		return ""
	}
	// The last version-looking part of the address: a release directory
	// comes after the repository's name, which can have digits of its own.
	matches := versionInURL.FindAllStringSubmatch(url, -1)
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1][1]
}

func imageTag(image string) string {
	if i := strings.LastIndex(image, ":"); i >= 0 && !strings.Contains(image[i:], "/") {
		return image[i+1:]
	}
	return ""
}

// UpgradeInstalledComponent applies the version of an installed component this
// panel installs now over the one that is there. A failure leaves it marked
// installed — the version that was running is, as far as anybody knows, still
// running — with the reason beside it.
func (c *Cluster) UpgradeInstalledComponent(ctx context.Context, name string) error {
	def, ok := settings.LookupComponent(name)
	if !ok {
		return fmt.Errorf("%q is not a component Skifity installs", name)
	}
	c.installMu.Lock()
	defer c.installMu.Unlock()

	current, err := c.db.GetComponent(ctx, name)
	if err != nil {
		return err
	}
	if current.Status != "installed" {
		return errdoc.BadRequest(def.Title + " is not installed, so there is nothing to upgrade; install it instead.")
	}
	wanted := c.ComponentVersion(ctx, name)
	if wanted != "" && current.Version == wanted {
		return nil
	}
	if err := c.db.SetComponent(ctx, store.ClusterComponent{
		Name: name, Status: "upgrading", Version: current.Version, InstalledAt: current.InstalledAt,
	}); err != nil {
		return err
	}
	c.log.Info("upgrading cluster component", "component", name, "from", current.Version, "to", wanted)
	upgradeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
	defer cancel()
	if err := c.installComponent(upgradeCtx, name); err != nil {
		_ = c.db.SetComponent(ctx, store.ClusterComponent{
			Name: name, Status: "installed", Version: current.Version, InstalledAt: current.InstalledAt,
			Detail: fmt.Sprintf("The upgrade to %s did not finish: %s", wanted, err),
		})
		return fmt.Errorf("upgrade %s: %w", def.Title, err)
	}
	return c.db.SetComponent(ctx, store.ClusterComponent{
		Name: name, Status: "installed", Version: wanted, InstalledAt: time.Now(),
	})
}

// installUpgradeController installs the system-upgrade-controller: its
// resource definitions first, then the controller that reads them.
func (c *Cluster) installUpgradeController(ctx context.Context) error {
	for _, url := range []string{upgradeCRDURL, upgradeControllerURL} {
		c.log.Info("applying component manifest", "component", UpgradeComponent, "url", url)
		if err := c.client.ApplyManifestURL(ctx, url); err != nil {
			return err
		}
	}
	if err := c.client.WaitForDeployment(ctx, kube.UpgradeNamespace, "system-upgrade-controller", 5*time.Minute); err != nil {
		return fmt.Errorf("the upgrade controller was installed but did not start: %w", err)
	}
	return nil
}

// K3sUpgradeNodes is every node as an upgrade needs to know it.
func (c *Cluster) K3sUpgradeNodes(ctx context.Context) ([]kube.UpgradeNode, error) {
	summary, err := c.Summary(ctx)
	if err != nil {
		return nil, err
	}
	nodes := make([]kube.UpgradeNode, 0, len(summary.Nodes))
	for _, node := range summary.Nodes {
		controlPlane := false
		for _, role := range node.Roles {
			if role == "control-plane" || role == "master" {
				controlPlane = true
			}
		}
		nodes = append(nodes, kube.UpgradeNode{Name: node.Name, ControlPlane: controlPlane, Ready: node.Ready, Version: node.KubeletVer})
	}
	return nodes, nil
}

// StartK3sUpgrade installs the controller if it is not there and gives it
// the two Plans for a target version. The controller does the rest, one node
// at a time; the servers' versions on the Servers page are how it is followed.
func (c *Cluster) StartK3sUpgrade(ctx context.Context, target kube.K3sVersion) error {
	if err := c.InstallComponent(ctx, UpgradeComponent); err != nil {
		return err
	}
	for _, plan := range kube.BuildK3sUpgradePlans(target) {
		if err := c.client.Applier().Apply(ctx, plan); err != nil {
			return fmt.Errorf("give the upgrade controller its plan: %w", err)
		}
	}
	return nil
}

// K3sReleases is the latest k3s release of each minor version, newest first,
// from k3s's own channel server. The upgrade form offers them; a panel that
// cannot reach the internet can still be given a version by hand.
func (c *Cluster) K3sReleases(ctx context.Context) ([]api.K3sRelease, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k3sChannelsURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("read the k3s release channels: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("read the k3s release channels: %s", resp.Status)
	}
	return parseK3sChannels(io.LimitReader(resp.Body, 1<<20))
}

// parseK3sChannels keeps the channels named after a minor version, each
// with the release it points at.
func parseK3sChannels(body io.Reader) ([]api.K3sRelease, error) {
	var doc struct {
		Data []struct {
			ID     string `json:"id"`
			Latest string `json:"latest"`
		} `json:"data"`
	}
	if err := json.NewDecoder(body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("read the k3s release channels: %w", err)
	}
	stable := ""
	var out []api.K3sRelease
	versions := map[string]kube.K3sVersion{}
	for _, channel := range doc.Data {
		if channel.ID == "stable" {
			stable = channel.Latest
		}
		version, ok := kube.ParseK3sVersion(channel.Latest)
		if !ok || !strings.HasPrefix(channel.ID, "v") {
			continue
		}
		versions[channel.Latest] = version
		out = append(out, api.K3sRelease{Channel: channel.ID, Version: version.String()})
	}
	if len(out) == 0 {
		return nil, errors.New("the k3s release channels named no releases")
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := kube.ParseK3sVersion(out[i].Version)
		b, _ := kube.ParseK3sVersion(out[j].Version)
		return a.Compare(b) > 0
	})
	for i := range out {
		out[i].Stable = out[i].Version == stable
	}
	return out, nil
}
