package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"skifity/internal/crypto"
	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/plugins"
	"skifity/internal/store"
	"skifity/internal/version"
)

// Running a plugin.
//
// Installing one is four things in an order that matters: a token narrowed to
// exactly the permissions its manifest declared, a secret it will verify events
// with, a Secret object holding both plus its settings, and then the pod. The
// token first, because a pod that starts without one is a plugin whose first
// request fails for a reason nobody can see.
//
// Removing one is the same list backwards, and the token goes even when the
// cluster cannot be reached: a credential nobody can trace to anything is worse
// than a namespace left behind.

// PluginSecretName is the one Secret a plugin is given.
const PluginSecretName = "plugin"

// PluginTokenEnv and the rest are the names a plugin reads.
const (
	PluginTokenEnv  = "SKIFITY_TOKEN"
	PluginSecretEnv = "SKIFITY_SIGNING_SECRET"
	// PluginSettingPrefix is prepended to each declared setting, so that a
	// plugin's own key cannot collide with one of the names above.
	PluginSettingPrefix = "SKIFITY_SETTING_"
)

// InstallPlugin issues the plugin's credentials and starts it.
//
// The manifest is the one an administrator approved, passed in rather than
// re-fetched: a publisher who changes their manifest changes what the plugin
// may do, and that has to be an upgrade somebody approves rather than a fact
// that quietly becomes true between reading the screen and pressing the button.
func (c *Cluster) InstallPlugin(ctx context.Context, record store.Plugin, manifest plugins.Manifest, token string) error {
	if c.keyring == nil {
		return fmt.Errorf("this panel has no keyring, so a plugin's secrets cannot be sealed")
	}
	signing, err := c.keyring.Open(record.HMACSealed, pluginSealContext(record.ID))
	if err != nil {
		return fmt.Errorf("read the plugin's signing secret: %w", err)
	}

	values, err := c.pluginSettingValues(ctx, record.ID)
	if err != nil {
		return err
	}

	spec := kube.PluginSpec{
		ID:              manifest.ID,
		Name:            manifest.Name,
		Image:           manifest.Image,
		SecretName:      PluginSecretName,
		Port:            manifest.Runtime.Port,
		Health:          manifest.Runtime.Health,
		MemoryMB:        manifest.Runtime.MemoryMB,
		SystemNamespace: c.client.SystemNamespace(),
	}

	objects := kube.BuildPluginObjects(spec)
	// The Secret is applied after the namespace and before the pod needs it.
	// ApplyAll goes in order, and the namespace is first in the list.
	objects = append(objects, pluginSecret(manifest.ID, token, string(signing), values))

	if err := c.client.Applier().ApplyAll(ctx, objects...); err != nil {
		return err
	}
	namespace := kube.PluginNamespace(manifest.ID)
	if err := c.client.WaitForDeployment(ctx, namespace, kube.PluginDeploymentName, 3*time.Minute); err != nil {
		return errdoc.New("plugin.did_not_start", "The plugin was installed and did not start").
			WithCause("%s", err).
			WithImpact("Its credentials exist and its container is not running, so it receives nothing.").
			WithFix("Look at the plugin's own logs. A plugin image has to run as a non-root user; " +
				"an image that starts as root is refused by the namespace it runs in, and that is " +
				"the most common reason a plugin will not start.").
			WithStatus(http.StatusBadGateway)
	}
	return nil
}

// RemovePlugin takes a plugin's namespace away, and everything in it.
func (c *Cluster) RemovePlugin(ctx context.Context, id string) error {
	return c.client.DeleteNamespace(ctx, kube.PluginNamespace(id))
}

// StopPlugin scales a plugin to nothing without removing it, which is what
// switching it off means.
func (c *Cluster) StopPlugin(ctx context.Context, id string) error {
	return c.scalePlugin(ctx, id, 0)
}

// StartPlugin brings a switched-off plugin back.
func (c *Cluster) StartPlugin(ctx context.Context, id string) error {
	return c.scalePlugin(ctx, id, 1)
}

func (c *Cluster) scalePlugin(ctx context.Context, id string, replicas int32) error {
	namespace := kube.PluginNamespace(id)
	patch := fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas)
	_, err := c.client.Clientset().AppsV1().Deployments(namespace).Patch(
		ctx, kube.PluginDeploymentName, "application/strategic-merge-patch+json",
		[]byte(patch), metav1.PatchOptions{})
	if err != nil && !kube.IsNotFound(err) {
		return fmt.Errorf("scale the plugin %s: %w", id, err)
	}
	return nil
}

// PluginStatus reports whether a plugin's pod is running.
func (c *Cluster) PluginStatus(ctx context.Context, id string) (string, string) {
	namespace := kube.PluginNamespace(id)
	deployment, err := c.client.Clientset().AppsV1().
		Deployments(namespace).Get(ctx, kube.PluginDeploymentName, metav1.GetOptions{})
	if err != nil {
		if kube.IsNotFound(err) {
			return "failed", "The plugin's container is not in the cluster."
		}
		return "failed", err.Error()
	}
	if deployment.Spec.Replicas != nil && *deployment.Spec.Replicas == 0 {
		return "disabled", ""
	}
	if deployment.Status.ReadyReplicas > 0 {
		return "running", ""
	}
	return "installing", "Waiting for the plugin to start."
}

// pluginSettingValues reads a plugin's settings, opening the sealed ones.
func (c *Cluster) pluginSettingValues(ctx context.Context, id string) (map[string]string, error) {
	stored, err := c.db.ListPluginSettings(ctx, id)
	if err != nil {
		return nil, err
	}
	values := make(map[string]string, len(stored))
	for _, setting := range stored {
		if !setting.Encrypted {
			values[setting.Key] = setting.Value
			continue
		}
		plaintext, err := c.keyring.Open(setting.Value, pluginSettingContext(id, setting.Key))
		if err != nil {
			return nil, fmt.Errorf("read the plugin setting %q: %w", setting.Key, err)
		}
		values[setting.Key] = string(plaintext)
	}
	return values, nil
}

// pluginSecret is everything the plugin is handed, in one object.
func pluginSecret(id, token, signing string, values map[string]string) *corev1.Secret {
	data := map[string]string{
		PluginTokenEnv:  token,
		PluginSecretEnv: signing,
	}
	for key, value := range values {
		data[PluginSettingPrefix+envName(key)] = value
	}
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      PluginSecretName,
			Namespace: kube.PluginNamespace(id),
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": version.Binary,
			},
		},
		Type:       corev1.SecretTypeOpaque,
		StringData: data,
	}
}

// envName turns a setting key into the environment variable half of its name.
//
// The manifest already restricts a key to lowercase letters, digits and
// underscores, so this is an upper-casing and not a sanitiser — but it is
// written as one anyway, because the day somebody widens that pattern is the
// day this would otherwise produce a name no shell can export.
func envName(key string) string {
	out := make([]byte, 0, len(key))
	for i := 0; i < len(key); i++ {
		switch ch := key[i]; {
		case ch >= 'a' && ch <= 'z':
			out = append(out, ch-32)
		case ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9', ch == '_':
			out = append(out, ch)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

// pluginSealContext binds a plugin's signing secret to the plugin it belongs
// to, so a row copied to another plugin will not open.
func pluginSealContext(id string) string { return "plugin:" + id }

// pluginSettingContext does the same for one setting.
func pluginSettingContext(id, key string) string { return "plugin-setting:" + id + ":" + key }

// NewPluginSigningSecret makes the key a plugin verifies events with.
func (c *Cluster) NewPluginSigningSecret(id string) (string, error) {
	if c.keyring == nil {
		return "", fmt.Errorf("this panel has no keyring, so a plugin's signing secret cannot be sealed")
	}
	raw, err := crypto.RandomToken(32)
	if err != nil {
		return "", fmt.Errorf("generate the plugin's signing secret: %w", err)
	}
	return c.keyring.Seal([]byte(raw), pluginSealContext(id))
}

// RestartPlugin rolls a plugin so that new settings are read.
func (c *Cluster) RestartPlugin(ctx context.Context, id string) error {
	return c.client.RestartApp(ctx, kube.PluginNamespace(id), kube.PluginDeploymentName)
}

// PluginTargets is what the dispatcher asks: who wants this event, and how to
// reach them.
//
// Read from the database on every event rather than cached, because plugins are
// installed, switched off and removed while the panel runs, and a cached list
// is a plugin that keeps being sent events after somebody turned it off.
func (c *Cluster) PluginTargets(ctx context.Context, event, teamID string) ([]plugins.Target, error) {
	installed, err := c.db.ListPlugins(ctx)
	if err != nil {
		return nil, err
	}
	var targets []plugins.Target
	for _, record := range installed {
		if !record.Enabled || record.Status == "disabled" {
			continue
		}
		if !c.pluginMaySee(ctx, record, teamID) {
			continue
		}
		var manifest plugins.Manifest
		if err := json.Unmarshal([]byte(record.Manifest), &manifest); err != nil {
			c.log.Error("an installed plugin's manifest could not be read",
				"plugin", record.ID, "error", err)
			continue
		}
		subscription, wants := subscriptionFor(manifest, event)
		if !wants {
			continue
		}
		signing, err := c.keyring.Open(record.HMACSealed, pluginSealContext(record.ID))
		if err != nil {
			c.log.Error("a plugin's signing secret could not be read, so it was not sent the event",
				"plugin", record.ID, "error", err)
			continue
		}
		targets = append(targets, plugins.Target{
			ID:       manifest.ID,
			Name:     manifest.Name,
			URL:      kube.PluginServiceURL(manifest.ID, manifest.Runtime.Port),
			Secret:   string(signing),
			Blocking: subscription.Blocking,
			Timeout:  time.Duration(subscription.Timeout()) * time.Second,
		})
	}
	return targets, nil
}

// pluginMaySee reports whether a plugin should hear about something that
// happened in a team.
//
// A plugin is installed panel-wide and runs with a token belonging to whoever
// installed it, so it can already read what that person can read. The events it
// is sent have to draw the same line: an owner of one team must not install a
// plugin and have it told about another team's deploys — or, for a blocking
// hook, be able to stop them.
//
// An event with no team attached reaches nobody, and a plugin whose installer
// cannot be identified hears nothing. Both are recorded rather than silent,
// because a plugin that receives nothing looks exactly like a plugin that is
// broken.
func (c *Cluster) pluginMaySee(ctx context.Context, record store.Plugin, teamID string) bool {
	if teamID == "" {
		c.log.Warn("an event carried no team, so no plugin was sent it", "plugin", record.ID)
		return false
	}
	if record.InstalledBy == "" {
		c.log.Warn("a plugin has no record of who installed it, so it was not sent an event",
			"plugin", record.ID)
		return false
	}
	if _, err := c.db.GetMembership(ctx, teamID, record.InstalledBy); err != nil {
		return false
	}
	return true
}

func subscriptionFor(manifest plugins.Manifest, event string) (plugins.EventSubscription, bool) {
	for _, subscription := range manifest.Events {
		if subscription.Event == event {
			return subscription, true
		}
	}
	return plugins.EventSubscription{}, false
}
