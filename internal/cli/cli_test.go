package cli

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseInterspersed(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		wantApp    string
		wantSecret bool
		wantPos    []string
	}{
		{"flags first", []string{"--app", "web", "KEY=value"}, "web", false, []string{"KEY=value"}},
		{"flags last", []string{"KEY=value", "--app", "web"}, "web", false, []string{"KEY=value"}},
		{"flags between", []string{"A=1", "--app", "web", "B=2"}, "web", false, []string{"A=1", "B=2"}},
		{"boolean flag", []string{"KEY=value", "--secret"}, "", true, []string{"KEY=value"}},
		{"no flags", []string{"A=1", "B=2"}, "", false, []string{"A=1", "B=2"}},
		{"no positionals", []string{"--app", "web"}, "web", false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flags := flag.NewFlagSet("test", flag.ContinueOnError)
			flags.SetOutput(os.Stderr)
			app := flags.String("app", "", "")
			secret := flags.Bool("secret", false, "")

			positional, err := parseInterspersed(flags, tc.args)
			if err != nil {
				t.Fatalf("parseInterspersed: %v", err)
			}
			if *app != tc.wantApp {
				t.Errorf("app is %q, want %q", *app, tc.wantApp)
			}
			if *secret != tc.wantSecret {
				t.Errorf("secret is %v, want %v", *secret, tc.wantSecret)
			}
			if strings.Join(positional, ",") != strings.Join(tc.wantPos, ",") {
				t.Errorf("positional is %v, want %v", positional, tc.wantPos)
			}
		})
	}
}

func TestParseProjectFile(t *testing.T) {
	file, err := parseProjectFile(`
# a comment
name = "shop"
app = "app_123"
environment = "env_456"
`)
	if err != nil {
		t.Fatalf("parseProjectFile: %v", err)
	}
	if file.App != "app_123" || file.Name != "shop" || file.Environment != "env_456" {
		t.Fatalf("parsed as %+v", file)
	}

	// An unknown key is an error rather than being ignored: a typo in a
	// setting the user believes they changed is worse than a clear failure.
	if _, err := parseProjectFile(`aap = "app_123"`); err == nil {
		t.Fatal("an unknown setting was accepted")
	}
	if _, err := parseProjectFile(`app`); err == nil {
		t.Fatal("a line with no value was accepted")
	}
}

func TestProjectFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	path, err := SaveProjectFile(ProjectFile{App: "app_1", Name: "Shop", Environment: "env_1"})
	if err != nil {
		t.Fatalf("SaveProjectFile: %v", err)
	}
	// It is committed to a repository, so it must contain nothing secret.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if strings.Contains(string(data), "token") {
		t.Fatalf("the project file contains a credential:\n%s", data)
	}

	// It must be found from a subdirectory, the way git finds its root.
	sub := filepath.Join(dir, "packages", "web")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Chdir(sub); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	found, _, err := LoadProjectFile()
	if err != nil {
		t.Fatalf("LoadProjectFile from a subdirectory: %v", err)
	}
	if found.App != "app_1" {
		t.Fatalf("found %+v", found)
	}
}

func TestNormaliseRemote(t *testing.T) {
	cases := map[string]string{
		"git@github.com:acme/shop.git":      "https://github.com/acme/shop",
		"https://github.com/acme/shop.git":  "https://github.com/acme/shop",
		"https://github.com/acme/shop":      "https://github.com/acme/shop",
		"git@gitlab.example.com:team/x.git": "https://gitlab.example.com/team/x",
	}
	for input, want := range cases {
		if got := normaliseRemote(input); got != want {
			t.Errorf("normaliseRemote(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestConfigIsPrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	t.Setenv("SKIFITY_CONFIG", path)

	if err := SaveConfig(Config{PanelURL: "https://panel.example.test", Token: "skf_secret"}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	// A token readable by every user on the machine is not a stored token.
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Fatalf("the config file is mode %#o", mode)
	}

	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.Token != "skf_secret" {
		t.Fatalf("the token did not survive: %q", loaded.Token)
	}
}

func TestLoadConfigWithoutOneExplainsItself(t *testing.T) {
	t.Setenv("SKIFITY_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	_, err := LoadConfig()
	if err == nil {
		t.Fatal("a missing config was accepted")
	}
	if !strings.Contains(err.Error(), "login") {
		t.Fatalf("the error does not tell the user what to do: %v", err)
	}
}

func TestQuery(t *testing.T) {
	if got := Query("tail", "100", "follow", ""); got != "?tail=100" {
		t.Fatalf("Query dropped or kept the wrong values: %q", got)
	}
	if got := Query("a", ""); got != "" {
		t.Fatalf("Query with nothing set = %q, want empty", got)
	}
}

func TestEnvSubcommand(t *testing.T) {
	cases := []struct {
		args     []string
		wantSub  string
		wantVals []string
	}{
		{nil, "list", nil},
		{[]string{"list"}, "list", nil},
		{[]string{"ls"}, "list", nil},
		{[]string{"set", "A=1", "B=2"}, "set", []string{"A=1", "B=2"}},
		{[]string{"unset", "A"}, "unset", []string{"A"}},
		{[]string{"rm", "A"}, "unset", []string{"A"}},
		// A bare KEY=value is what someone typing it means.
		{[]string{"A=1"}, "set", []string{"A=1"}},
		{[]string{"nonsense"}, "nonsense", nil},
	}
	for _, tc := range cases {
		sub, values := envSubcommand(tc.args)
		if sub != tc.wantSub {
			t.Errorf("envSubcommand(%v) sub = %q, want %q", tc.args, sub, tc.wantSub)
		}
		if strings.Join(values, ",") != strings.Join(tc.wantVals, ",") {
			t.Errorf("envSubcommand(%v) values = %v, want %v", tc.args, values, tc.wantVals)
		}
	}
}

// The CLI has to work where nobody ever ran `skifity login`: a CI job, a
// container, an AI assistant's sandbox. SKIFITY_URL and SKIFITY_TOKEN are how,
// and they have to win over a stored configuration so that setting them is not
// silently ignored on a machine that happens to have one.
func TestLoadConfigFromEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	t.Setenv("SKIFITY_CONFIG", path)

	t.Run("no configuration at all", func(t *testing.T) {
		t.Setenv("SKIFITY_URL", "")
		t.Setenv("SKIFITY_TOKEN", "")
		if _, err := LoadConfig(); err == nil {
			t.Fatal("expected an error when there is no configuration and no environment")
		} else if !strings.Contains(err.Error(), "SKIFITY_URL") {
			t.Errorf("the error should mention the environment variables, got %q", err)
		}
	})

	t.Run("environment only", func(t *testing.T) {
		t.Setenv("SKIFITY_URL", "https://panel.example.test")
		t.Setenv("SKIFITY_TOKEN", "skf_not_a_real_token")
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.PanelURL != "https://panel.example.test" || cfg.Token != "skf_not_a_real_token" {
			t.Errorf("the environment was not used: %+v", cfg)
		}
	})

	t.Run("environment beats the file", func(t *testing.T) {
		if err := os.WriteFile(path,
			[]byte(`{"panel_url":"https://stored.example.test","token":"stored-token","team_id":"team_stored"}`),
			0o600); err != nil {
			t.Fatalf("write the stored config: %v", err)
		}
		t.Setenv("SKIFITY_URL", "https://override.example.test")
		t.Setenv("SKIFITY_TOKEN", "override-token")

		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.PanelURL != "https://override.example.test" {
			t.Errorf("the environment should win, got %q", cfg.PanelURL)
		}
		if cfg.Token != "override-token" {
			t.Errorf("the environment should win, got the stored token")
		}
		// Anything the environment does not set still comes from the file.
		if cfg.TeamID != "team_stored" {
			t.Errorf("the stored team should survive, got %q", cfg.TeamID)
		}
	})
}

// TestEveryCommandTakesJSON: llms.txt tells an AI assistant "every command takes
// --json", and this package's own help text says the same. An assistant reads
// that literally and passes the flag; a command that does not define it exits
// with "flag provided but not defined", which reads as the tool being broken
// rather than as the document being wrong.
//
// `open`, `logout`, `admin reset-password` and `admin backup-db` did not have
// it. The flag is checked by reading the source, because a command's flags are
// defined inside the function that runs it and there is no registry to ask.
func TestEveryCommandTakesJSON(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	var source strings.Builder
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		source.Write(body)
	}

	// cmdAdmin only prints help and dispatches; its subcommands are checked.
	exempt := map[string]bool{"cmdAdmin": true}

	checked := 0
	for _, function := range strings.Split(source.String(), "\nfunc ") {
		name, _, found := strings.Cut(function, "(")
		if !found || (!strings.HasPrefix(name, "cmd") && !strings.HasPrefix(name, "admin")) {
			continue
		}
		if exempt[name] {
			continue
		}
		checked++
		if !strings.Contains(function, `flags.Bool("json"`) {
			t.Errorf("%s does not define --json, and every command is documented as taking it", name)
		}
	}
	if checked < 10 {
		t.Fatalf("only %d commands were found; this test is not reading the package", checked)
	}
}

// The namespace and the slug in an export come from the panel. A panel that
// answered with a namespace that climbs out of the directory would have the
// CLI write a file wherever it liked, as whoever ran the export.
func TestAnExportNeverWritesOutsideTheDirectoryItWasGiven(t *testing.T) {
	base := t.TempDir()

	// One ".." is absorbed by the "manifests" element and lands back inside, so
	// these are the ones that actually climb out.
	for _, escape := range []string{
		"../../elsewhere",
		"../../../etc/cron.d",
		"a/../../../elsewhere",
	} {
		if path, err := underneath(base, "manifests", escape); err == nil {
			t.Errorf("underneath(%q) allowed %s, which is outside %s", escape, path, base)
		}
	}
	// And the same check with no element in front of it, because the file name
	// goes through it too.
	if path, err := underneath(base, "../escaped.yaml"); err == nil {
		t.Errorf("underneath allowed %s, which is outside %s", path, base)
	}

	// An absolute name is not an escape: Join puts it under the base, which is
	// a strange file name and not a file anywhere else.
	for _, name := range []string{"acme-production", "/etc"} {
		path, err := underneath(base, "manifests", name)
		if err != nil {
			t.Fatalf("underneath(%q) was refused: %v", name, err)
		}
		if !strings.HasPrefix(path, base) {
			t.Fatalf("%s is not inside %s", path, base)
		}
	}
}
