package builder

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The clone script is a shell program, so it is tested by running it.
//
// Reading it was not enough. It built the credential into a variable and
// expanded it unquoted, on the stated belief that the variable held "the two
// -c words this script built itself". It held four, because the header's value
// is `Authorization: Basic <token>` and the shell splits on those two spaces.
// git was handed `Basic` where it expects a subcommand, so every build from a
// private repository failed — and nothing in the script's text looked wrong.
//
// runCloneScript runs the generated script with a stub git on PATH that records
// its arguments, and returns one line per invocation.
func runCloneScript(t *testing.T, spec JobSpec, env map[string]string) []string {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no POSIX shell here")
	}

	dir := t.TempDir()
	record := filepath.Join(dir, "git-calls")
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// A stub git: it writes what it was called with, one invocation per line,
	// with a marker between arguments so an argument containing a space is
	// still visible as one argument.
	stub := "#!/bin/sh\nprintf '%s\\n' \"$(printf '<%s>' \"$@\")\" >> " + record + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}

	script := cloneScript(spec)
	// The workspace the script cds into has to exist.
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Skipf("cannot create %s here: %v", workspace, err)
	}

	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the clone script failed: %v\n%s", err, out)
	}

	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("the script never called git: %v", err)
	}
	var calls []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line != "" {
			calls = append(calls, line)
		}
	}
	return calls
}

// findCall returns the invocation whose arguments contain want.
func findCall(calls []string, want string) string {
	for _, call := range calls {
		if strings.Contains(call, "<"+want+">") {
			return call
		}
	}
	return ""
}

func TestCloningAPrivateRepositoryAsksGitToFetch(t *testing.T) {
	calls := runCloneScript(t,
		JobSpec{CloneSecret: "app-git", RepoURL: "https://github.com/example/private.git"},
		map[string]string{
			"REPO_URL":  "https://github.com/example/private.git",
			"GIT_REF":   "main",
			"GIT_TOKEN": "not-a-real-token",
		})

	fetch := findCall(calls, "fetch")
	if fetch == "" {
		t.Fatalf("git was never asked to fetch. What it was asked:\n%s", strings.Join(calls, "\n"))
	}
	// The header is one argument, spaces and all.
	if !strings.Contains(fetch, "<http.https://github.com/.extraHeader=Authorization: Basic ") {
		t.Errorf("the credential header was not passed as one argument: %s", fetch)
	}
	// And nothing was split out of it into a word git would read as a command.
	if strings.Contains(fetch, "<Basic>") {
		t.Errorf("the header was split, so git would read a word of it as a subcommand: %s", fetch)
	}
	// -c takes exactly one argument, and fetch has to be the next word.
	if !strings.Contains(fetch, "><fetch><--depth>") {
		t.Errorf("fetch is not the subcommand: %s", fetch)
	}
}

func TestCloningAPublicRepositoryPassesNoCredentialAtAll(t *testing.T) {
	calls := runCloneScript(t,
		JobSpec{RepoURL: "https://github.com/example/public.git"},
		map[string]string{
			"REPO_URL": "https://github.com/example/public.git",
			"GIT_REF":  "main",
		})

	fetch := findCall(calls, "fetch")
	if fetch == "" {
		t.Fatalf("git was never asked to fetch. What it was asked:\n%s", strings.Join(calls, "\n"))
	}
	if strings.Contains(fetch, "extraHeader") || strings.Contains(fetch, "<-c>") {
		t.Errorf("a public clone carried configuration it does not need: %s", fetch)
	}
	if !strings.HasPrefix(fetch, "<fetch>") {
		t.Errorf("fetch is not the first word: %s", fetch)
	}
}

// Bitbucket takes a token only with a user name of its own, x-token-auth,
// where GitHub reads the token and ignores the name. The name comes from the
// clone Secret beside the token, and a Secret written before it had one still
// clones the way it always did.
func TestTheCloneUsesTheUserNameItsHostWants(t *testing.T) {
	fetchWith := func(env map[string]string) string {
		t.Helper()
		env["REPO_URL"] = "https://bitbucket.org/acme/private.git"
		env["GIT_REF"] = "main"
		env["GIT_TOKEN"] = "not-a-real-token"
		fetch := findCall(runCloneScript(t, JobSpec{CloneSecret: "app-git", RepoURL: env["REPO_URL"]}, env), "fetch")
		if fetch == "" {
			t.Fatal("git was never asked to fetch")
		}
		return fetch
	}
	basic := func(credential string) string {
		return "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(credential)) + ">"
	}

	if fetch := fetchWith(map[string]string{"GIT_USERNAME": "x-token-auth"}); !strings.Contains(fetch, basic("x-token-auth:not-a-real-token")) {
		t.Errorf("a Bitbucket clone did not present x-token-auth: %s", fetch)
	}
	if fetch := fetchWith(map[string]string{}); !strings.Contains(fetch, basic("x-access-token:not-a-real-token")) {
		t.Errorf("a clone Secret with no user name did not fall back to x-access-token: %s", fetch)
	}
}

func TestTheCloneUserNameComesFromTheSecretAndMayBeMissing(t *testing.T) {
	spec := baseJob()
	spec.CloneSecret = "bitbucket-token"
	job, err := BuildJob(spec)
	if err != nil {
		t.Fatalf("BuildJob: %v", err)
	}
	for _, env := range job.Spec.Template.Spec.InitContainers[0].Env {
		if env.Name != "GIT_USERNAME" {
			continue
		}
		ref := env.ValueFrom.SecretKeyRef
		if env.Value != "" || ref == nil || ref.Name != "bitbucket-token" || ref.Key != "username" ||
			ref.Optional == nil || !*ref.Optional {
			t.Fatalf("GIT_USERNAME is %+v; it is read from the clone Secret, and a Secret without it still starts the pod", env)
		}
		return
	}
	t.Fatal("the clone is not given a user name")
}

// The submodule pass has to carry the credential the same way, or a private
// repository with a private submodule fails at the second step instead of the
// first.
func TestASubmoduleUpdateCarriesTheSameCredential(t *testing.T) {
	calls := runCloneScript(t,
		JobSpec{CloneSecret: "app-git", RepoURL: "https://github.com/example/private.git"},
		map[string]string{
			"REPO_URL":  "https://github.com/example/private.git",
			"GIT_REF":   "main",
			"GIT_TOKEN": "not-a-real-token",
		})

	submodule := findCall(calls, "submodule")
	if submodule == "" {
		t.Fatalf("submodules were never updated. What git was asked:\n%s", strings.Join(calls, "\n"))
	}
	if !strings.Contains(submodule, "extraHeader") {
		t.Errorf("the submodule pass carried no credential: %s", submodule)
	}
	if strings.Contains(submodule, "<Basic>") {
		t.Errorf("the header was split on the submodule pass: %s", submodule)
	}
}
