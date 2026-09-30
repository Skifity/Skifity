package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// write puts a configuration file in a temporary directory and returns its path.
func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "skifity.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// TestEveryFileKeyIsTheOneTheStructNames: the file's keys are the environment
// variable names lower-cased, and the struct's toml tags are what somebody
// reads to find out what to write. A tag that names a key the parser does not
// look for is a setting that is accepted, ignored, and never reported.
func TestEveryFileKeyIsTheOneTheStructNames(t *testing.T) {
	// A value per field that is visibly not the default.
	values := map[string]string{
		"string":        "changed",
		"bool":          "true",
		"int":           "7",
		"time.Duration": "42m",
	}

	structType := reflect.TypeOf(Config{})
	for i := range structType.NumField() {
		field := structType.Field(i)
		tag := field.Tag.Get("toml")
		if tag == "" {
			t.Errorf("%s has no toml tag, so nobody can tell what to write for it", field.Name)
			continue
		}
		value, ok := values[field.Type.String()]
		if !ok {
			t.Fatalf("%s is a %s, which this test does not know how to set", field.Name, field.Type)
		}

		cfg := Default()
		if err := cfg.applyFile(write(t, tag+" = \""+value+"\"\n")); err != nil {
			t.Fatalf("%s: %v", tag, err)
		}
		before := reflect.ValueOf(Default()).Field(i)
		after := reflect.ValueOf(cfg).Field(i)
		if after.Equal(before) {
			t.Errorf("%s = %q in a file changed nothing; the parser does not look for %q",
				tag, value, tag)
		}
	}
}

// The file and the environment have to mean the same thing, because the
// documentation says one is the other lower-cased, and somebody moving a
// setting from a container's environment into a file expects it to survive.
func TestTheFileAndTheEnvironmentAgree(t *testing.T) {
	env := map[string]string{
		"SKIFITY_LISTEN":              "127.0.0.1:9000",
		"SKIFITY_LOG_LEVEL":           "debug",
		"SKIFITY_SESSION_TTL":         "1h",
		"SKIFITY_TRUSTED_PROXY_COUNT": "3",
		"SKIFITY_DEV_MODE":            "yes",
		"SKIFITY_POD_NETWORK":         "vxlan",
		"SKIFITY_CLI_DIR":             "/opt/cli",
	}

	var file strings.Builder
	for key, value := range env {
		file.WriteString(strings.ToLower(strings.TrimPrefix(key, EnvPrefix)) + " = \"" + value + "\"\n")
	}

	fromFile := Default()
	if err := fromFile.applyFile(write(t, file.String())); err != nil {
		t.Fatalf("applyFile: %v", err)
	}
	fromEnv := Default()
	fromEnv.applyEnv(func(key string) string { return env[key] })

	if fromFile != fromEnv {
		t.Errorf("the same settings read differently:\nfile: %+v\nenv:  %+v", fromFile, fromEnv)
	}
	if fromFile == Default() {
		t.Fatal("neither source changed anything, so this test proves nothing")
	}
}

func TestValuesAreReadAsWhatTheyAre(t *testing.T) {
	cfg := Default()
	if err := cfg.applyFile(write(t, `
# A comment, and a blank line follow.

listen = "127.0.0.1:9000"
session_ttl = "1h"
shutdown_grace = "5s"
trusted_proxy_count = "3"
dev_mode = "yes"
log_level = "debug"   # trailing comments are stripped
`)); err != nil {
		t.Fatalf("applyFile: %v", err)
	}
	if cfg.Listen != "127.0.0.1:9000" {
		t.Errorf("listen = %q", cfg.Listen)
	}
	if cfg.SessionTTL != time.Hour {
		t.Errorf("session_ttl = %s", cfg.SessionTTL)
	}
	if cfg.ShutdownGrace != 5*time.Second {
		t.Errorf("shutdown_grace = %s", cfg.ShutdownGrace)
	}
	if cfg.TrustedProxyCount != 3 {
		t.Errorf("trusted_proxy_count = %d", cfg.TrustedProxyCount)
	}
	if !cfg.DevMode {
		t.Error("dev_mode = yes was not read as true")
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("log_level = %q, so the trailing comment was kept", cfg.LogLevel)
	}
}

// A `#` inside a quoted value is part of the value. Cutting at the first one
// silently shortens a path or a URL, and what fails afterwards is the thing
// that used it, a long way from here.
func TestAHashInsideAValueIsPartOfIt(t *testing.T) {
	cfg := Default()
	if err := cfg.applyFile(write(t, "database_path = \"/var/lib/skifity #2/panel.db\"\n")); err != nil {
		t.Fatalf("applyFile: %v", err)
	}
	if cfg.DatabasePath != "/var/lib/skifity #2/panel.db" {
		t.Errorf("database_path = %q; the value was cut at the hash", cfg.DatabasePath)
	}
}

func TestAFileThatIsNotConfigurationIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"a section":         "[server]\nlisten = \":8080\"\n",
		"no equals sign":    "listen\n",
		"an unclosed quote": "listen = \":8080\n",
	} {
		cfg := Default()
		if err := cfg.applyFile(write(t, body)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// A file that is not there is not an error: every setting has a default, so an
// absent configuration file is a valid configuration.
func TestAMissingFileIsFine(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nothing-here.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != Default().Listen {
		t.Error("a missing file changed the defaults")
	}
}

func TestValidateRefusesWhatWouldFailLater(t *testing.T) {
	cases := map[string]func(*Config){
		"a listen address that is not host:port": func(c *Config) { c.Listen = "8080" },
		"no database path":                       func(c *Config) { c.DatabasePath = "" },
		"no master key path":                     func(c *Config) { c.MasterKeyPath = "" },
		"an unknown log level":                   func(c *Config) { c.LogLevel = "chatty" },
		"an unknown log format":                  func(c *Config) { c.LogFormat = "xml" },
		"a public URL with no scheme":            func(c *Config) { c.PublicURL = "panel.example.test" },
		"a session shorter than a minute":        func(c *Config) { c.SessionTTL = time.Second },
		"a pod network nobody runs":              func(c *Config) { c.PodNetwork = "wiregaurd" },
	}
	for name, break_ := range cases {
		cfg := Default()
		break_(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s was accepted", name)
		} else if strings.TrimSpace(err.Error()) == "" {
			t.Errorf("%s was refused with an empty message", name)
		}
	}
	defaults := Default()
	if err := defaults.Validate(); err != nil {
		t.Errorf("the defaults do not pass validation: %v", err)
	}
}

// TestEverySettingIsDocumented: the configuration page is a table somebody
// copies from, and a setting that is not in it does not exist as far as anyone
// running Skifity is concerned. The list is derived from the struct rather than
// typed out here, so adding a field to Config is what fails this.
func TestEverySettingIsDocumented(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "..", "docs", "configuration.md"))
	if err != nil {
		t.Fatalf("read configuration.md: %v", err)
	}
	body := string(page)

	structType := reflect.TypeOf(Config{})
	for i := range structType.NumField() {
		tag := structType.Field(i).Tag.Get("toml")
		if tag == "" {
			continue // already reported by the test above
		}
		variable := EnvPrefix + strings.ToUpper(tag)
		if !strings.Contains(body, variable) {
			t.Errorf("%s is read and is not in docs/configuration.md", variable)
		}
	}
}

// TestSecureCookiesFollowsTheAddressNotTheBuild.
//
// A Secure cookie is not stored by a browser over plain http, and the default
// install is plain http: with no domain, the panel answers on an sslip.io
// address over HTTP on purpose (ADR-0015). Deciding this from DevMode marked
// those cookies Secure and made signing in impossible, with nothing in the
// panel able to say why.
func TestSecureCookiesFollowsTheAddressNotTheBuild(t *testing.T) {
	for _, tc := range []struct {
		name    string
		config  Config
		want    bool
		because string
	}{
		{"https", Config{PublicURL: "https://panel.example.com"}, true,
			"a panel on https must not hand its session cookie to http"},
		{"plain http", Config{PublicURL: "http://203-0-113-10.sslip.io"}, false,
			"a Secure cookie over http is never stored, so nobody can sign in"},
		{"dev mode", Config{DevMode: true, PublicURL: "https://panel.example.com"}, false,
			"the dev server is http on localhost"},
		{"address unknown", Config{}, true,
			"unknown means secure: hard to sign in is recoverable, a cookie in the clear is not"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.config.SecureCookies(); got != tc.want {
				t.Fatalf("SecureCookies() = %v, want %v: %s", got, tc.want, tc.because)
			}
		})
	}
}
