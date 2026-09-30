package builder

import (
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
