// Package settings is the catalogue of everything an operator configures at
// runtime through the panel.
//
// This is the list that makes the product's promise true: a user never edits
// code or a file on the server to configure Skifity. Adding a feature that needs
// configuration means adding it here, not adding an environment variable.
package settings

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"skifity/internal/pluginstore"
)

// Definition describes one setting.
type Definition struct {
	// Key is the stable identifier stored in the database.
	Key string
	// Label is the English name shown in the UI; the UI translates it by key.
	Label string
	// Group puts related settings on the same settings page.
	Group string
	// Help explains what the setting does and where to find the value.
	Help string
	// Placeholder is an example value.
	Placeholder string
	// Secret means the value is encrypted and never shown again.
	Secret bool
	// Kind tells the panel what control to draw. Without it every setting is a
	// text box, and a yes/no setting becomes a field where someone has to guess
	// whether "yes", "true" or "1" is the word this one wants.
	Kind Kind
	// Options are the allowed values when Kind is KindChoice.
	Options []string
	// Multiline asks for a text area rather than a single line. Used for the
	// values that genuinely are long, such as a private key.
	Multiline bool
	// Validate rejects values that would fail later in a confusing way. An
	// empty value always passes, because clearing a setting is allowed.
	Validate func(string) error
}

// Kind is the shape of a setting's value.
type Kind string

const (
	// KindText is the default: a single line of free text.
	KindText Kind = "text"
	// KindBool is a switch. Stored as "true" or "false".
	KindBool Kind = "bool"
	// KindNumber is a whole number.
	KindNumber Kind = "number"
	// KindChoice is one of Options.
	KindChoice Kind = "choice"
	// KindURL, KindEmail and KindDomain are text with a keyboard hint and a
	// matching input type, so a phone offers the right keys and the browser
	// can help.
	KindURL    Kind = "url"
	KindEmail  Kind = "email"
	KindDomain Kind = "domain"
)

// ResolvedKind returns the setting's kind, working it out from the other fields
// when it was not set explicitly. A secret is always a password field.
func (d Definition) ResolvedKind() Kind {
	if d.Kind != "" {
		return d.Kind
	}
	if len(d.Options) > 0 {
		return KindChoice
	}
	return KindText
}

// Pod network backends. Every server in a cluster must agree: a node that
// joins with a different one never exchanges a packet with the others, and
// nothing reports that the flags disagree.
const (
	// FlannelWireGuard encrypts traffic between servers. The default.
	FlannelWireGuard = "wireguard-native"
	// FlannelVXLAN works on a kernel without the WireGuard module, and does
	// not encrypt.
	FlannelVXLAN = "vxlan"
)

// IngressServiceAddress is what a Cloudflare tunnel's public hostname points at.
//
// k3s ships Traefik in kube-system under this name. It is the address of the
// thing that already routes every app by Host header, so one route here is
// every app, for ever, with nothing to update when an app is added. It lives
// here rather than beside the connector because the operator has to type it
// into Cloudflare, which means the help text and the code must not drift.
const IngressServiceAddress = "traefik.kube-system.svc.cluster.local:80"

// Groups, in the order the UI shows them.
const (
	GroupGeneral       = "general"
	GroupDomains       = "domains"
	GroupGit           = "git"
	GroupStorage       = "storage"
	GroupEmail         = "email"
	GroupNotifications = "notifications"
	GroupRegistry      = "registry"
	GroupCluster       = "cluster"
	GroupSignIn        = "signin"
	GroupPlugins       = "plugins"
)

// Keys used elsewhere in the panel. Referring to a constant rather than a string
// literal means a rename is a compile error rather than a silent misconfiguration.
const (
	KeyPanelURL              = "general.panel_url"
	KeyK3sVersion            = "cluster.k3s_version"
	KeyFlannelBackend        = "cluster.flannel_backend"
	KeyPreviewTTLDays        = "cluster.preview_ttl_days"
	KeyWildcardDomain        = "domains.wildcard"
	KeyClusterIP             = "domains.cluster_ip"
	KeyACMEEmail             = "domains.acme_email"
	KeyACMEServer            = "domains.acme_server"
	KeyCloudflareTunnelToken = "domains.cloudflare_tunnel_token"
	KeyTrustedProxies        = "domains.trusted_proxies"
	KeyGeoCountryURL         = "domains.geo_country_url"
	KeyGeoASNURL             = "domains.geo_asn_url"
	KeyGeoEnabled            = "domains.geo_enabled"
	KeyPluginStoreURL        = "plugins.store_url"
	KeyPluginStoreKey        = "plugins.store_public_key"
	KeyS3Endpoint            = "storage.s3_endpoint"
	KeyS3Region              = "storage.s3_region"
	KeyS3Bucket              = "storage.s3_bucket"
	KeyS3AccessKey           = "storage.s3_access_key"
	KeyS3SecretKey           = "storage.s3_secret_key"
	KeyS3PathStyle           = "storage.s3_path_style"
	KeySMTPHost              = "email.smtp_host"
	KeySMTPPort              = "email.smtp_port"
	KeySMTPUser              = "email.smtp_user"
	KeySMTPPassword          = "email.smtp_password"
	KeySMTPFrom              = "email.smtp_from"
	KeySMTPTLS               = "email.smtp_tls"
	KeyRegistryURL           = "registry.url"
	KeyRegistryUser          = "registry.username"
	KeyRegistryPassword      = "registry.password"
	KeySSOIssuer             = "signin.oidc_issuer"
	KeySSOClientID           = "signin.oidc_client_id"
	KeySSOClientSecret       = "signin.oidc_client_secret"
	KeySSOButtonLabel        = "signin.oidc_button_label"
	KeySSODomains            = "signin.oidc_allowed_domains"
	KeySSOAutoCreate         = "signin.oidc_auto_create"
	KeyBuilderDefault        = "general.default_builder"
	KeyTelemetryDisabled     = "general.telemetry_disabled"

	// Written by the panel rather than by a person: when the registry was last
	// swept, and how long finished records are kept. They are settings because
	// they have to survive a restart, and the first one has no form field for
	// the same reason a timestamp never does.
	KeyRegistrySweptAt   = "maintenance.registry_swept_at"
	KeyPrunedAt          = "maintenance.pruned_at"
	KeyDeploymentHistory = "maintenance.deployment_history"
	KeyAuditHistoryDays  = "maintenance.audit_history_days"
)

// Definitions is the whole catalogue, in display order.
var Definitions = []Definition{
	{
		Key: KeyPanelURL, Label: "Panel URL", Group: GroupGeneral,
		Help:        "The address people use to reach this panel. Used in links inside notifications and in webhook URLs.",
		Placeholder: "https://panel.example.com",
		Kind:        KindURL,
		Validate:    validateURL,
	},
	{
		Key: KeyBuilderDefault, Label: "Default builder", Group: GroupGeneral,
		Help:     "Which builder to use when a repository has no Dockerfile. Railpack produces smaller images; Nixpacks is older and more widely tested.",
		Options:  []string{"railpack", "nixpacks"},
		Validate: validateOneOf("railpack", "nixpacks"),
	},
	{
		Key: KeyTelemetryDisabled, Label: "Disable usage reporting", Group: GroupGeneral,
		Help: "Skifity sends nothing anywhere by default. This setting exists so that the absence of telemetry is visible rather than assumed.",
		Kind: KindBool, Validate: validateBool,
	},
	{
		Key: KeySSOIssuer, Label: "Single sign-on issuer", Group: GroupSignIn,
		Help: "The OpenID Connect issuer URL of your identity provider — Okta, Entra, Authentik, Keycloak, Zitadel, Google. " +
			"Skifity reads its configuration from /.well-known/openid-configuration under this address. Leave empty to sign in with a password only.",
		Placeholder: "https://login.example.com",
		Kind:        KindURL,
		Validate:    validateURL,
	},
	{
		Key: KeySSOClientID, Label: "Client ID", Group: GroupSignIn,
		Help: "From the application you registered with the provider. Its redirect URI is this panel's address followed by /api/auth/sso/callback.",
	},
	{
		Key: KeySSOClientSecret, Label: "Client secret", Group: GroupSignIn, Secret: true,
		Help: "Stored encrypted and never shown again.",
	},
	{
		Key: KeySSOButtonLabel, Label: "Sign-in button text", Group: GroupSignIn,
		Help:        "What the button on the sign-in page says. Leave empty for a generic label.",
		Placeholder: "Sign in with Okta",
	},
	{
		Key: KeySSODomains, Label: "Allowed email domains", Group: GroupSignIn,
		Help: "Comma-separated. Only people whose verified email is in one of these domains may sign in. " +
			"Leave empty to accept anybody the provider vouches for, which is only safe when the provider is yours.",
		Placeholder: "example.com, example.org",
	},
	{
		Key: KeySSOAutoCreate, Label: "Create an account on first sign-in", Group: GroupSignIn,
		Help: "With this off, somebody the provider knows and this panel does not is refused, " +
			"so accounts are created by invitation rather than by anyone with a company address.",
		Kind: KindBool, Validate: validateBool,
	},
	{
		Key: KeyK3sVersion, Label: "Kubernetes version", Group: GroupCluster,
		Help: "The k3s version a server added from here is installed with. Leave empty to follow the stable channel, which is what a new cluster gets. Pin it to keep a server you add next month on the same version as the ones you have.",
		// A k3s release, which is a Kubernetes version with a k3s suffix.
		Placeholder: "v1.34.1+k3s1",
		Validate:    validateK3sVersion,
	},
	{
		Key: KeyPreviewTTLDays, Label: "Remove preview environments after", Group: GroupCluster,
		Help:        "Days with no deployment before a pull request's preview environment is removed. A preview that is still being pushed to survives however long the pull request stays open. Set to 0 to keep them until their pull request closes, which is how previews are normally removed — but a webhook that never arrives then leaves one running forever.",
		Placeholder: "7",
		Kind:        KindNumber,
		Validate:    validateInt,
	},
	{
		Key: KeyFlannelBackend, Label: "How servers talk to each other", Group: GroupCluster,
		Help: "WireGuard encrypts traffic between your servers, which matters when they are " +
			"with different providers. It needs the wireguard kernel module on every server. " +
			"vxlan works everywhere and is not encrypted. Every server in a cluster has to use " +
			"the same one, so change this before adding servers, not after.",
		Kind:     KindChoice,
		Options:  []string{FlannelWireGuard, FlannelVXLAN},
		Validate: validateOneOf(FlannelWireGuard, FlannelVXLAN),
	},
	{
		Key: KeyDeploymentHistory, Label: "Deployment records to keep, per app", Group: GroupCluster,
		Help: "How many past deployments an app keeps, with their build logs. " +
			"Older ones are removed so the panel's database does not grow without end. " +
			"Only the ten most recent can be rolled back to, because that is how many " +
			"images the registry keeps and how many revisions Kubernetes itself remembers.",
		Placeholder: "50",
		Kind:        KindNumber,
		Validate:    validateInt,
	},
	{
		Key: KeyAuditHistoryDays, Label: "Keep the activity log for", Group: GroupCluster,
		Help: "Days. The activity log is kept far longer than anything else here, " +
			"because a log that forgets is most of the way to not having one. " +
			"Shorten it only if you have a reason to.",
		Placeholder: "365",
		Kind:        KindNumber,
		Validate:    validateInt,
	},
	{
		Key: KeyWildcardDomain, Label: "Wildcard domain", Group: GroupDomains,
		Help:        "A domain with a wildcard DNS record pointing at this cluster. Every app gets a free subdomain under it. Leave empty to use sslip.io addresses instead.",
		Placeholder: "apps.example.com",
		Kind:        KindDomain,
		Validate:    validateDomain,
	},
	{
		Key: KeyClusterIP, Label: "Cluster public IP", Group: GroupDomains,
		Help:        "The address your domains should point at. Detected automatically; override it if your cluster sits behind a load balancer.",
		Placeholder: "203.0.113.10",
		Validate:    validateIPOrHost,
	},
	{
		Key: KeyACMEEmail, Label: "Let's Encrypt email", Group: GroupDomains,
		Help:        "Where Let's Encrypt sends certificate expiry warnings. Required before HTTPS certificates can be issued.",
		Placeholder: "you@example.com",
		Kind:        KindEmail,
		Validate:    validateEmail,
	},
	{
		Key: KeyACMEServer, Label: "ACME directory URL", Group: GroupDomains,
		Help:        "Leave empty for Let's Encrypt production. Point it at the staging directory while you are testing, so you do not hit the rate limit.",
		Placeholder: "https://acme-staging-v02.api.letsencrypt.org/directory",
		Kind:        KindURL,
		Validate:    validateURL,
	},
	{
		Key: KeyCloudflareTunnelToken, Label: "Cloudflare tunnel token", Group: GroupDomains, Secret: true,
		Help: "Lets your apps answer on the internet without a public IP, a port open, or a fixed address for DNS to point at. " +
			"Create a tunnel in the Cloudflare dashboard under Zero Trust, Networks, Tunnels; choose the Cloudflared connector and copy the token it shows. " +
			"Then add one public hostname on that tunnel — a wildcard such as *.apps.example.com covers every app at once — with the service set to " +
			"HTTP and the address " + IngressServiceAddress + ". Install Cloudflare tunnel under Components once the token is saved.",
		Placeholder: "eyJhIjoi...",
		Validate:    validateCloudflareTunnelToken,
	},
	{
		Key: KeyTrustedProxies, Label: "Trusted proxies", Group: GroupDomains,
		Help: "Address ranges in front of this cluster that may say who a visitor is. " +
			"The pod and service networks are trusted already; add a load balancer's range here if you have one. " +
			"Get this wrong in the other direction and a visitor can choose the address your firewall rules judge them by, " +
			"so add only ranges you control.",
		Placeholder: "203.0.113.0/24, 198.51.100.9",
		Validate:    validateCIDRList,
	},
	{
		Key: KeyGeoEnabled, Label: "Look up a visitor's country and network", Group: GroupDomains,
		Help: "On by default. The firewall downloads DB-IP's free monthly databases, which need no account, so that a rule " +
			"can name a country or a network. Turn it off on a cluster with no way out to the internet, or if you would rather " +
			"nothing was downloaded — a rule about a country cannot then be saved, which is better than one that is quietly skipped.",
		Kind: KindBool, Validate: validateBool,
	},
	{
		Key: KeyGeoCountryURL, Label: "Country database", Group: GroupDomains,
		Help: "Where the firewall's country data comes from, in MaxMind's .mmdb format. " +
			"Leave empty for DB-IP's free monthly file, which needs no account. A %s in the address is replaced with the month. " +
			"Point it at your own copy, or at a MaxMind download, if you would rather.",
		Placeholder: "https://example.com/GeoLite2-Country.mmdb",
		Validate:    validateGeoURL,
	},
	{
		Key: KeyGeoASNURL, Label: "Network database", Group: GroupDomains,
		Help:        "The same, for the network a visitor's address belongs to, which is what an AS number rule tests.",
		Placeholder: "https://example.com/GeoLite2-ASN.mmdb",
		Validate:    validateGeoURL,
	},
	{
		Key: KeyPluginStoreURL, Label: "Plugin store", Group: GroupPlugins,
		Help: "Where the panel looks for plugins. Leave empty for the Skifity store. " +
			"Point it at your own index to run a store of your own, or clear the public key below to browse " +
			"one that is not signed — a plugin can always be installed from a manifest address or by pasting one, " +
			"which is what a cluster with no way out to the internet uses.",
		Placeholder: "https://plugins.skifity.com/index.json",
		Kind:        KindURL,
		Validate:    validateURL,
	},
	{
		Key: KeyPluginStoreKey, Label: "Plugin store signing key", Group: GroupPlugins,
		Help: "The store's Ed25519 public key, base64. With one set, an index that is not signed by it is refused. " +
			"With none, the catalogue is still readable and every entry is shown as vouched for by nobody. " +
			"Generate a pair with: skifity admin plugin-key",
		Placeholder: "kMvJ...",
		Validate:    validatePluginStoreKey,
	},
	// A GitHub App needs five more settings than these, and had them: an App
	// ID, a slug, a client id, a client secret and a private key. Nothing ever
	// read one of them. There is no JWT signed with that key and no
	// installation token exchanged for it, so an operator could paste a private
	// key and have nothing happen — and the client id's help text promised
	// signing in with GitHub and a list of repositories to pick from, neither
	// of which exists. They are gone until the code behind them exists.
	//
	// Connecting GitHub with a personal access token is the path that works,
	// and it is the one the panel offers.
	{
		Key: KeyS3Endpoint, Label: "S3 endpoint", Group: GroupStorage,
		Help:        "Any S3-compatible service works: AWS, Backblaze B2, Cloudflare R2, Wasabi, or a MinIO server you run yourself.",
		Placeholder: "https://s3.eu-central-1.amazonaws.com",
		Kind:        KindURL, Validate: validateURL,
	},
	{Key: KeyS3Region, Label: "S3 region", Group: GroupStorage, Placeholder: "eu-central-1",
		Help: "Some providers ignore this; AWS does not."},
	{Key: KeyS3Bucket, Label: "S3 bucket", Group: GroupStorage, Placeholder: "skifity-backups",
		Help: "The bucket backups are written to. Skifity does not create it for you."},
	{Key: KeyS3AccessKey, Label: "S3 access key", Group: GroupStorage, Secret: true,
		Help: "Use a key that can only write to this bucket."},
	{Key: KeyS3SecretKey, Label: "S3 secret key", Group: GroupStorage, Secret: true,
		Help: "Stored encrypted and never shown again."},
	{Key: KeyS3PathStyle, Label: "Use path-style URLs", Group: GroupStorage, Kind: KindBool, Validate: validateBool,
		Help: "Turn this on for MinIO and most self-hosted S3 services."},
	{Key: KeySMTPHost, Label: "SMTP host", Group: GroupEmail, Placeholder: "smtp.example.com",
		Help: "Needed for email notifications and for password reset emails."},
	{Key: KeySMTPPort, Label: "SMTP port", Group: GroupEmail, Placeholder: "587",
		Help: "587 for STARTTLS, which is what almost everything uses now. 465 is implicit TLS; 25 is unencrypted and usually blocked by hosting providers.",
		Kind: KindNumber, Validate: validatePort},
	{Key: KeySMTPUser, Label: "SMTP username", Group: GroupEmail,
		Help: "Leave empty if your mail server accepts mail from this cluster without signing in."},
	{Key: KeySMTPPassword, Label: "SMTP password", Group: GroupEmail, Secret: true,
		Help: "Stored encrypted. Most providers want an app password here rather than your account password."},
	{Key: KeySMTPFrom, Label: "Send email from", Group: GroupEmail, Placeholder: "skifity@example.com",
		Help: "The address notifications appear to come from. It usually has to be one your mail server is allowed to send as, or the mail is rejected.",
		Kind: KindEmail, Validate: validateEmail},
	{Key: KeySMTPTLS, Label: "Use STARTTLS", Group: GroupEmail, Kind: KindBool, Validate: validateBool,
		Help: "Leave on unless your mail server only accepts plain connections."},
	{Key: KeyRegistryURL, Label: "External registry", Group: GroupRegistry,
		Help:        "Where built images are pushed. Leave empty to use the registry inside the cluster, which is the simplest option.",
		Placeholder: "registry.example.com/skifity",
	},
	{Key: KeyRegistryUser, Label: "Registry username", Group: GroupRegistry,
		Help: "Needed only for an external registry. The registry inside the cluster needs no credentials."},
	{Key: KeyRegistryPassword, Label: "Registry password", Group: GroupRegistry, Secret: true,
		Help: "A token with permission to push. Stored encrypted and never shown again."},
}

var index = func() map[string]Definition {
	m := make(map[string]Definition, len(Definitions))
	for i, d := range Definitions {
		if d.Validate == nil {
			// Filling this in once here means every caller can call
			// Validate without a nil check.
			Definitions[i].Validate = noValidation
			d.Validate = noValidation
		}
		m[d.Key] = d
	}
	return m
}()

// Lookup finds a setting definition by key.
func Lookup(key string) (Definition, bool) {
	d, ok := index[key]
	return d, ok
}

// noValidation accepts anything, used by free-text settings.
func noValidation(string) error { return nil }

// Context returns the encryption context for a setting's value, so a sealed
// setting cannot be moved to a different key.
func Context(key string) string { return "setting:" + key }

// Component describes an optional cluster add-on.
type Component struct {
	Name        string
	Title       string
	Description string
	// Optional components are installed on first use rather than at install time.
	Optional bool
	// Beta marks a component that is pre-1.0 upstream.
	Beta bool
	// MemoryMB is roughly what it costs to run, so the UI can warn honestly.
	MemoryMB int
	// External marks a component the panel cannot install.
	//
	// It is still listed, because an operator deciding what to run wants to
	// know it exists and what it costs. An Install button that always answers
	// "use Helm instead" is worse than no button: it looks like a failure.
	External bool
	// Docs is where the panel points instead of that button.
	Docs string
}

// Components is what the panel can install into the cluster.
var Components = []Component{
	{Name: "cert-manager", Title: "HTTPS certificates",
		Description: "Issues and renews Let's Encrypt certificates for your domains.", MemoryMB: 120},
	{Name: "registry", Title: "Image registry", Optional: true,
		Description: "Stores the images Skifity builds, inside the cluster.", MemoryMB: 60},
	{Name: "buildkit", Title: "Builder", Optional: true,
		Description: "Builds your apps into images, without needing Docker on the host.", MemoryMB: 200},
	{Name: "cloudnative-pg", Title: "PostgreSQL", Optional: true,
		Description: "Runs and looks after PostgreSQL databases, including backups and failover.", MemoryMB: 150},
	{Name: "keda", Title: "Scale to zero", Optional: true, Beta: true,
		Description: "Stops idle apps and starts them again on the first request. The HTTP add-on is beta upstream.", MemoryMB: 180},
	{Name: "longhorn", Title: "Cross-node storage", Optional: true,
		Description: "Replicates volumes between servers so an app with storage survives a node failure. Uses a noticeable amount of memory on every node.", MemoryMB: 700},
	{Name: "firewall", Title: "Firewall", Optional: true,
		Description: "Rules on who may reach an app: by address, by country, by network, by path or by header, combined with and and or. Needs Traefik, which k3s installs by default.", MemoryMB: 128},
	{Name: "cloudflare-tunnel", Title: "Cloudflare tunnel", Optional: true,
		Description: "Puts your apps on the internet with no public IP and no port open, through Cloudflare. Needs a tunnel token in Settings, under Domains and HTTPS.", MemoryMB: 64},
	{Name: "monitoring", Title: "Full monitoring", Optional: true, External: true,
		Description: "Prometheus and Grafana, installed with Helm rather than by the panel. Skifity shows CPU and memory for every server and every instance without it.",
		Docs:        "/docs/troubleshooting#full-monitoring", MemoryMB: 900},
}

var componentIndex = func() map[string]Component {
	m := make(map[string]Component, len(Components))
	for _, c := range Components {
		m[c.Name] = c
	}
	return m
}()

// LookupComponent finds a component definition by name.
func LookupComponent(name string) (Component, bool) {
	c, ok := componentIndex[name]
	return c, ok
}

// --- validators ---
//
// Every validator treats an empty value as valid, because clearing a setting is
// how an integration is disconnected.

// k3sVersionPattern is a k3s release tag: a Kubernetes version and a k3s build.
var k3sVersionPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+\+k3s\d+$`)

// validateK3sVersion refuses anything the k3s installer would not recognise.
//
// A wrong version here is not a validation error later: it is a server that
// downloads nothing and fails halfway through being added, which looks like a
// network problem.
func validateK3sVersion(value string) error {
	if value == "" {
		return nil
	}
	if !k3sVersionPattern.MatchString(value) {
		return fmt.Errorf("that is not a k3s release; they look like v1.34.1+k3s1")
	}
	return nil
}

// validateCloudflareTunnelToken refuses what people paste instead of the token.
//
// The token is base64 of a small JSON object with three keys: the account, the
// tunnel and its secret. Two things get pasted in its place often enough to be
// worth naming — the whole `cloudflared service install <token>` command line
// that the dashboard shows above it, and the tunnel's UUID from the URL. Both
// are refused here, in a text box, rather than by a connector that starts,
// fails to authenticate, and restarts for ever.
func validateCloudflareTunnelToken(value string) error {
	if value == "" {
		return nil
	}
	if strings.ContainsAny(value, " \t\n") {
		return errors.New("paste the token only, not the whole cloudflared command it is shown inside")
	}
	if tunnelIDPattern.MatchString(value) {
		return errors.New("that is the tunnel's ID, not its token; the token is the long string under \"Install and run a connector\"")
	}
	decoded, err := base64.StdEncoding.WithPadding(base64.NoPadding).DecodeString(strings.TrimRight(value, "="))
	if err != nil {
		return errors.New("that is not a Cloudflare tunnel token; they are one long line of letters, digits and + or /")
	}
	var claims struct {
		Account string `json:"a"`
		Tunnel  string `json:"t"`
		Secret  string `json:"s"`
	}
	if err := json.Unmarshal(decoded, &claims); err != nil || claims.Account == "" || claims.Tunnel == "" || claims.Secret == "" {
		return errors.New("that token is not one Cloudflare issued; copy it again from Zero Trust, Networks, Tunnels")
	}
	return nil
}

// tunnelIDPattern is a UUID, which is what the dashboard's own URL contains.
var tunnelIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// validateCIDRList refuses a trusted-proxy list that would not do what it says.
//
// This setting decides whose word is taken for a visitor's address, so a typo
// here is a rule judging the wrong person. The reader that runs on every
// request drops what it cannot parse rather than failing a site; this is the
// half that tells the operator, while they are looking at the box.
func validateCIDRList(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	for _, field := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\t'
	}) {
		if _, err := netip.ParsePrefix(field); err == nil {
			continue
		}
		if _, err := netip.ParseAddr(field); err == nil {
			continue
		}
		return fmt.Errorf("%q is not an address or an address range; they look like 203.0.113.0/24", field)
	}
	return nil
}

// validateGeoURL accepts a URL that may carry a %s for the month.
//
// url.Parse is happy with a %s and net/url's own escaping is not, so the
// placeholder is taken out before the address is checked rather than being
// reported as an invalid escape.
func validateGeoURL(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	if err := validateURL(strings.ReplaceAll(value, "%s", "2026-01")); err != nil {
		return err
	}
	if !strings.HasSuffix(value, ".mmdb") && !strings.HasSuffix(value, ".mmdb.gz") {
		return errors.New("that is not a database file; it should end in .mmdb or .mmdb.gz")
	}
	return nil
}

// validatePluginStoreKey refuses a key that could never verify anything.
//
// The alternative is finding out at the moment somebody opens the store, which
// is both the wrong place and the wrong time: the message there would be "the
// index is not signed by the key this panel trusts", which reads as an attack
// rather than as a typo.
func validatePluginStoreKey(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	if _, err := pluginstore.ParsePublicKey(value); err != nil {
		return err
	}
	return nil
}

func validateURL(value string) error {
	if value == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("enter a full URL, starting with https://")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("the URL must start with http:// or https://")
	}
	return nil
}

func validateDomain(value string) error {
	if value == "" {
		return nil
	}
	value = strings.TrimPrefix(value, "*.")
	if strings.Contains(value, "/") || strings.Contains(value, " ") {
		return errors.New("enter just the domain, without a scheme or a path")
	}
	if !strings.Contains(value, ".") {
		return errors.New("that does not look like a domain name")
	}
	return nil
}

func validateIPOrHost(value string) error {
	if value == "" {
		return nil
	}
	if strings.ContainsAny(value, " /:") {
		return errors.New("enter just an IP address or hostname")
	}
	return nil
}

func validateEmail(value string) error {
	if value == "" {
		return nil
	}
	at := strings.LastIndex(value, "@")
	if at <= 0 || at == len(value)-1 || !strings.Contains(value[at+1:], ".") {
		return errors.New("that does not look like an email address")
	}
	return nil
}

func validateInt(value string) error {
	if value == "" {
		return nil
	}
	if _, err := strconv.Atoi(value); err != nil {
		return errors.New("enter a number")
	}
	return nil
}

func validatePort(value string) error {
	if value == "" {
		return nil
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("enter a port between 1 and 65535")
	}
	return nil
}

func validateBool(value string) error {
	switch value {
	case "", "true", "false":
		return nil
	}
	return errors.New("this setting is on or off")
}

func validateOneOf(allowed ...string) func(string) error {
	return func(value string) error {
		if value == "" {
			return nil
		}
		for _, a := range allowed {
			if value == a {
				return nil
			}
		}
		return fmt.Errorf("must be one of: %s", strings.Join(allowed, ", "))
	}
}
