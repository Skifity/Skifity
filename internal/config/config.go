// Package config loads panel configuration from a file and the environment.
//
// Precedence, lowest to highest: built-in defaults, the config file, environment
// variables. Everything an operator can change at runtime lives in the database
// and is edited in the panel; this package only covers what must be known before
// the database can be opened.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"skifity/internal/settings"
	"skifity/internal/version"
)

// EnvPrefix is the prefix of every environment variable this package reads.
const EnvPrefix = "SKIFITY_"

// Config is the panel's startup configuration.
type Config struct {
	// Listen is the address the HTTP server binds to.
	Listen string `json:"listen" toml:"listen"`
	// DatabasePath is the SQLite file backing the panel.
	DatabasePath string `json:"database_path" toml:"database_path"`
	// MasterKeyPath is the file holding the master encryption key. It is kept
	// outside the database on purpose: see ADR-0006.
	MasterKeyPath string `json:"master_key_path" toml:"master_key_path"`
	// KubeconfigPath is empty when running inside the cluster, where the
	// ServiceAccount token is used instead.
	//
	// The file key is `kubeconfig`, not `kubeconfig_path`: a file's keys are
	// the environment variable names lower-cased, the variable is
	// SKIFITY_KUBECONFIG, and a tag naming a key the parser does not look for
	// is a setting that is accepted, ignored, and never reported.
	KubeconfigPath string `json:"kubeconfig_path" toml:"kubeconfig"`
	// Namespace is the namespace the panel itself runs in.
	Namespace string `json:"namespace" toml:"namespace"`
	// LogLevel is one of debug, info, warn, error.
	LogLevel string `json:"log_level" toml:"log_level"`
	// LogFormat is json or text.
	LogFormat string `json:"log_format" toml:"log_format"`
	// DevMode serves the frontend from a Vite dev server instead of the embedded
	// files and relaxes cookie security so http://localhost works.
	DevMode bool `json:"dev_mode" toml:"dev_mode"`
	// DevFrontendURL is the Vite dev server to proxy to when DevMode is on.
	DevFrontendURL string `json:"dev_frontend_url" toml:"dev_frontend_url"`
	// PublicURL is how users reach the panel. Used for OAuth callbacks, webhook
	// URLs and links in notifications. Empty means "derive from the request".
	PublicURL string `json:"public_url" toml:"public_url"`
	// TrustedProxyCount is how many reverse proxies sit in front of the panel.
	// Only that many entries are trusted from X-Forwarded-For.
	TrustedProxyCount int `json:"trusted_proxy_count" toml:"trusted_proxy_count"`
	// SessionTTL is how long a login lasts without activity.
	SessionTTL time.Duration `json:"session_ttl" toml:"session_ttl"`
	// ShutdownGrace is how long in-flight requests get during shutdown.
	ShutdownGrace time.Duration `json:"shutdown_grace" toml:"shutdown_grace"`
	// SetupTokenPath holds the one-time token the installer generates. The file
	// is deleted once setup completes.
	SetupTokenPath string `json:"setup_token_path" toml:"setup_token_path"`
	// ClusterTokenPath holds the k3s join token of the cluster the panel runs
	// in, copied there by the installer.
	//
	// A panel installed onto a server that already runs k3s cannot invent this:
	// a server added later has to join with the token that cluster was started
	// with, and the panel has no other way to learn it.
	ClusterTokenPath string `json:"cluster_token_path" toml:"cluster_token_path"`
	// PodNetwork is the flannel backend the cluster the panel runs in was
	// started with, passed in by the installer.
	//
	// Every node in a cluster has to use the same one, and the panel cannot
	// read the flags the first node was installed with. So the installer, which
	// chose it, says what it chose; the panel stores that the first time it
	// starts and every server it adds later is given the same backend. After
	// that the setting is the answer and this is ignored.
	PodNetwork string `json:"pod_network" toml:"pod_network"`
	// CLIDir holds the command line tool built for the platforms this panel
	// does not run on — macOS, Windows, the other architecture — as
	// skifity-<os>-<arch>[.exe].gz, which the image puts there. The panel
	// serves its own binary for its own platform, and these for the rest, so
	// the person deploying from a Mac gets a CLI that runs on it.
	CLIDir string `json:"cli_dir" toml:"cli_dir"`
}

// Default returns the configuration used when nothing overrides it.
func Default() Config {
	return Config{
		Listen:            ":8080",
		DatabasePath:      filepath.Join(version.DataDir, "panel.db"),
		MasterKeyPath:     filepath.Join(version.ConfigDir, "master.key"),
		Namespace:         "skifity-system",
		LogLevel:          "info",
		LogFormat:         "json",
		DevFrontendURL:    "http://127.0.0.1:5173",
		TrustedProxyCount: 1,
		SessionTTL:        7 * 24 * time.Hour,
		ShutdownGrace:     20 * time.Second,
		SetupTokenPath:    filepath.Join(version.ConfigDir, "setup-token"),
		ClusterTokenPath:  filepath.Join(version.ConfigDir, "cluster-token"),
		CLIDir:            "/usr/local/share/skifity/cli",
	}
}

// Load builds a Config from defaults, then the file at path when it is non-empty
// and exists, then the environment.
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		if err := cfg.applyFile(path); err != nil {
			return cfg, err
		}
	}
	cfg.applyEnv(os.Getenv)
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (c *Config) applyFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read config %s: %w", path, err)
	}
	kv, err := parseSimpleTOML(string(data))
	if err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	c.applyEnv(func(key string) string {
		return kv[strings.ToLower(strings.TrimPrefix(key, EnvPrefix))]
	})
	return nil
}

// applyEnv reads every setting through lookup, which lets the same mapping serve
// both the environment and the config file.
func (c *Config) applyEnv(lookup func(string) string) {
	str := func(key string, dst *string) {
		if v := lookup(EnvPrefix + key); v != "" {
			*dst = v
		}
	}
	str("LISTEN", &c.Listen)
	str("DATABASE_PATH", &c.DatabasePath)
	str("MASTER_KEY_PATH", &c.MasterKeyPath)
	str("KUBECONFIG", &c.KubeconfigPath)
	str("NAMESPACE", &c.Namespace)
	str("LOG_LEVEL", &c.LogLevel)
	str("LOG_FORMAT", &c.LogFormat)
	str("DEV_FRONTEND_URL", &c.DevFrontendURL)
	str("PUBLIC_URL", &c.PublicURL)
	str("SETUP_TOKEN_PATH", &c.SetupTokenPath)
	str("CLUSTER_TOKEN_PATH", &c.ClusterTokenPath)
	str("POD_NETWORK", &c.PodNetwork)
	str("CLI_DIR", &c.CLIDir)

	if v := lookup(EnvPrefix + "DEV_MODE"); v != "" {
		c.DevMode = truthy(v)
	}
	if v := lookup(EnvPrefix + "TRUSTED_PROXY_COUNT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			c.TrustedProxyCount = n
		}
	}
	if v := lookup(EnvPrefix + "SESSION_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			c.SessionTTL = d
		}
	}
	if v := lookup(EnvPrefix + "SHUTDOWN_GRACE"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			c.ShutdownGrace = d
		}
	}
}

// Validate reports configuration that would fail later in a confusing way.
func (c *Config) Validate() error {
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		return fmt.Errorf("listen %q is not host:port: %w", c.Listen, err)
	}
	if c.DatabasePath == "" {
		return errors.New("database_path must not be empty")
	}
	if c.MasterKeyPath == "" {
		return errors.New("master_key_path must not be empty")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log_level %q must be one of debug, info, warn, error", c.LogLevel)
	}
	switch c.LogFormat {
	case "json", "text":
	default:
		return fmt.Errorf("log_format %q must be json or text", c.LogFormat)
	}
	if c.PublicURL != "" && !strings.HasPrefix(c.PublicURL, "http://") && !strings.HasPrefix(c.PublicURL, "https://") {
		return fmt.Errorf("public_url %q must start with http:// or https://", c.PublicURL)
	}
	switch c.PodNetwork {
	case "", settings.FlannelWireGuard, settings.FlannelVXLAN:
	default:
		return fmt.Errorf("pod_network %q must be %s or %s", c.PodNetwork, settings.FlannelWireGuard, settings.FlannelVXLAN)
	}
	if c.SessionTTL < time.Minute {
		return fmt.Errorf("session_ttl %s is shorter than a minute", c.SessionTTL)
	}
	return nil
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// parseSimpleTOML understands the flat `key = "value"` subset we need, plus
// `#` comments. Pulling in a TOML dependency to read a dozen keys is not worth
// it.
func parseSimpleTOML(src string) (map[string]string, error) {
	out := map[string]string{}
	for i, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			return nil, fmt.Errorf("line %d: sections are not supported in the panel config file", i+1)
		}
		key, rest, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected key = value", i+1)
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			return nil, fmt.Errorf("line %d: expected key = value", i+1)
		}
		value, err := parseValue(strings.TrimSpace(rest))
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		out[key] = value
	}
	return out, nil
}

// parseValue reads the right-hand side of one line.
//
// A quoted value ends at its closing quote and everything inside it is the
// value, `#` included: this used to cut at the first " #" wherever it was, so a
// path or a URL with a hash in it was silently shortened and what failed was
// whatever used it, a long way from here. A value whose quote is never closed
// is a mistake rather than a value, because the alternative is reading the rest
// of the line — including the comment somebody thought they were writing.
func parseValue(rest string) (string, error) {
	if rest == "" {
		return "", nil
	}
	if quote := rest[0]; quote == '"' || quote == '\'' {
		end := strings.IndexByte(rest[1:], quote)
		if end < 0 {
			return "", fmt.Errorf("the value opens with %c and never closes it", quote)
		}
		value := rest[1 : end+1]
		if after := strings.TrimSpace(rest[end+2:]); after != "" && !strings.HasPrefix(after, "#") {
			return "", fmt.Errorf("there is %q after the closing quote", after)
		}
		return value, nil
	}
	// Unquoted: a comment can only start at whitespace, so a `#` in the middle
	// of a bare word stays part of it.
	if idx := strings.Index(rest, " #"); idx >= 0 {
		rest = rest[:idx]
	}
	if idx := strings.Index(rest, "\t#"); idx >= 0 {
		rest = rest[:idx]
	}
	return strings.TrimSpace(rest), nil
}

// SecureCookies reports whether the panel's cookies may be marked Secure, and
// therefore whether they can carry the __Host- prefix.
//
// It is decided from the address people actually reach the panel on, not from
// whether this is a development build. A Secure cookie is not stored by a
// browser over plain http, and the default install is plain http: with no
// domain, apps and the panel answer on an sslip.io address over HTTP on
// purpose (ADR-0015). Marking the session cookie Secure there does not make
// anything safer — it makes signing in silently impossible, and the panel
// would have had no idea why.
//
// Unknown means secure. An operator running the binary by hand without telling
// it its own address gets the safe answer and a warning at startup, which is
// the right way round: a panel that is hard to sign in to is recoverable, and
// a session cookie sent in the clear is not.
func (c Config) SecureCookies() bool {
	if c.DevMode {
		return false
	}
	return !strings.HasPrefix(c.PublicURL, "http://")
}
