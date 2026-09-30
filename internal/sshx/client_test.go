package sshx

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDialWithPassword(t *testing.T) {
	server, err := NewTestServer(TestServerOptions{Password: "hunter2"})
	if err != nil {
		t.Fatalf("NewTestServer: %v", err)
	}
	defer server.Close()
	host, port := server.Addr()

	client, err := Dial(t.Context(), Config{
		Host: host, Port: port,
		Credentials: Credentials{User: "root", Password: "hunter2"},
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	// The fingerprint must come back so it can be stored for next time.
	if !strings.HasPrefix(client.HostKey, "SHA256:") {
		t.Fatalf("host key fingerprint is %q, want an OpenSSH SHA256 fingerprint", client.HostKey)
	}
}

func TestDialWithKey(t *testing.T) {
	key, err := GenerateKeyPair("skifity@panel")
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	server, err := NewTestServer(TestServerOptions{AuthorizedKey: key.PublicKey})
	if err != nil {
		t.Fatalf("NewTestServer: %v", err)
	}
	defer server.Close()
	host, port := server.Addr()

	client, err := Dial(t.Context(), Config{
		Host: host, Port: port,
		Credentials: Credentials{User: "root", PrivateKey: key.PrivateKey},
	})
	if err != nil {
		t.Fatalf("Dial with a generated key: %v", err)
	}
	defer client.Close()
}

func TestDialRejectsWrongCredentials(t *testing.T) {
	server, err := NewTestServer(TestServerOptions{Password: "correct"})
	if err != nil {
		t.Fatalf("NewTestServer: %v", err)
	}
	defer server.Close()
	host, port := server.Addr()

	_, err = Dial(t.Context(), Config{
		Host: host, Port: port,
		Credentials: Credentials{User: "root", Password: "wrong"},
	})
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("Dial with a wrong password gave %v, want ErrAuthFailed", err)
	}
}

func TestDialWithNoCredentials(t *testing.T) {
	_, err := Dial(t.Context(), Config{Host: "127.0.0.1", Port: 22})
	if !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("Dial with nothing gave %v, want ErrNoCredentials", err)
	}
}

func TestDialUnreachable(t *testing.T) {
	// Port 1 on loopback has nothing listening.
	_, err := Dial(t.Context(), Config{
		Host: "127.0.0.1", Port: 1, Timeout: time.Second,
		Credentials: Credentials{User: "root", Password: "x"},
	})
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("Dial to a closed port gave %v, want ErrUnreachable", err)
	}
}

func TestDialRefusesChangedHostKey(t *testing.T) {
	server, err := NewTestServer(TestServerOptions{Password: "pw"})
	if err != nil {
		t.Fatalf("NewTestServer: %v", err)
	}
	defer server.Close()
	host, port := server.Addr()

	// A stored fingerprint from a different machine. The panel runs privileged
	// commands over this connection, so this must be refused, not warned about.
	_, err = Dial(t.Context(), Config{
		Host: host, Port: port,
		HostKey:     "SHA256:definitely-not-this-server",
		Credentials: Credentials{User: "root", Password: "pw"},
	})
	if !errors.Is(err, ErrHostKeyChanged) {
		t.Fatalf("Dial with a mismatched host key gave %v, want ErrHostKeyChanged", err)
	}
}

func TestDialAcceptsMatchingHostKey(t *testing.T) {
	server, err := NewTestServer(TestServerOptions{Password: "pw"})
	if err != nil {
		t.Fatalf("NewTestServer: %v", err)
	}
	defer server.Close()
	host, port := server.Addr()

	first, err := Dial(t.Context(), Config{
		Host: host, Port: port, Credentials: Credentials{User: "root", Password: "pw"},
	})
	if err != nil {
		t.Fatalf("first Dial: %v", err)
	}
	fingerprint := first.HostKey
	first.Close()

	second, err := Dial(t.Context(), Config{
		Host: host, Port: port, HostKey: fingerprint,
		Credentials: Credentials{User: "root", Password: "pw"},
	})
	if err != nil {
		t.Fatalf("reconnecting with the stored fingerprint failed: %v", err)
	}
	second.Close()
}

func TestRunCapturesExitCodeAndOutput(t *testing.T) {
	server, err := NewTestServer(TestServerOptions{Password: "pw"})
	if err != nil {
		t.Fatalf("NewTestServer: %v", err)
	}
	defer server.Close()
	server.Respond("uname", "Linux node-1 6.8.0", 0)
	server.RespondStderr("false", "", "it did not work", 3)
	host, port := server.Addr()

	client, err := Dial(t.Context(), Config{
		Host: host, Port: port, Credentials: Credentials{User: "root", Password: "pw"},
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	result, err := client.Run(t.Context(), "uname -a")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Output() != "Linux node-1 6.8.0" {
		t.Fatalf("stdout is %q", result.Output())
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code is %d, want 0", result.ExitCode)
	}

	// A non-zero exit must not be an error: half the panel's checks are
	// "does this command succeed".
	failed, err := client.Run(t.Context(), "false")
	if err != nil {
		t.Fatalf("a failing command returned an error: %v", err)
	}
	if failed.ExitCode != 3 {
		t.Fatalf("exit code is %d, want 3", failed.ExitCode)
	}
	if !strings.Contains(failed.Combined(), "it did not work") {
		t.Fatalf("stderr was lost: %q", failed.Combined())
	}
}

func TestRunStreamingDeliversLines(t *testing.T) {
	server, err := NewTestServer(TestServerOptions{Password: "pw"})
	if err != nil {
		t.Fatalf("NewTestServer: %v", err)
	}
	defer server.Close()
	server.Respond("install", "step one\nstep two\nstep three", 0)
	host, port := server.Addr()

	client, err := Dial(t.Context(), Config{
		Host: host, Port: port, Credentials: Credentials{User: "root", Password: "pw"},
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	var lines []string
	result, err := client.RunStreaming(t.Context(), "install.sh", func(stream, line string) {
		lines = append(lines, line)
	})
	if err != nil {
		t.Fatalf("RunStreaming: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code is %d", result.ExitCode)
	}
	if len(lines) != 3 {
		t.Fatalf("received %d lines (%v), want 3", len(lines), lines)
	}
	if lines[0] != "step one" || lines[2] != "step three" {
		t.Fatalf("lines are %v", lines)
	}
}

func TestWriteFileQuotesContentSafely(t *testing.T) {
	server, err := NewTestServer(TestServerOptions{Password: "pw"})
	if err != nil {
		t.Fatalf("NewTestServer: %v", err)
	}
	defer server.Close()
	host, port := server.Addr()

	client, err := Dial(t.Context(), Config{
		Host: host, Port: port, Credentials: Credentials{User: "root", Password: "pw"},
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	// Content with shell metacharacters must survive intact. An unquoted
	// heredoc would expand $HOME and run the backticks.
	content := "TOKEN=$HOME`whoami`\"quoted\"\nSECOND=line"
	if err := client.WriteFile(t.Context(), "/etc/skifity/config", content, "0600"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	commands := server.Commands()
	if len(commands) != 1 {
		t.Fatalf("got %d commands, want 1", len(commands))
	}
	command := commands[0]
	if !strings.Contains(command, "<<'SKIFITY_EOF") {
		t.Fatalf("the heredoc delimiter is not quoted, so the shell would expand the content:\n%s", command)
	}
	if !strings.Contains(command, content) {
		t.Fatalf("the content was mangled:\n%s", command)
	}
	if !strings.Contains(command, "chmod 0600") {
		t.Fatalf("the file mode was not applied:\n%s", command)
	}
	if !strings.Contains(command, "mkdir -p '/etc/skifity'") {
		t.Fatalf("the parent directory is not created:\n%s", command)
	}
}

func TestWriteFileRefusesDangerousPaths(t *testing.T) {
	client := &Client{}
	err := client.WriteFile(t.Context(), "/etc/'; rm -rf /; '", "x", "0600")
	if err == nil {
		t.Fatal("a path containing a quote was accepted, which would break out of the command")
	}
}

func TestGenerateKeyPair(t *testing.T) {
	pair, err := GenerateKeyPair("skifity@panel")
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	if !strings.HasPrefix(pair.PrivateKey, "-----BEGIN OPENSSH PRIVATE KEY-----") {
		t.Fatalf("the private key is not in OpenSSH format: %q", firstLine(pair.PrivateKey))
	}
	if !strings.HasPrefix(pair.PublicKey, "ssh-ed25519 ") {
		t.Fatalf("the public key is %q, want an ed25519 authorized_keys line", pair.PublicKey)
	}
	if !strings.HasSuffix(pair.PublicKey, "skifity@panel") {
		t.Fatalf("the comment was lost: %q", pair.PublicKey)
	}
	if !strings.HasPrefix(pair.Fingerprint, "SHA256:") {
		t.Fatalf("the fingerprint is %q", pair.Fingerprint)
	}

	// Two keys must never be the same.
	other, _ := GenerateKeyPair("skifity@panel")
	if other.PrivateKey == pair.PrivateKey {
		t.Fatal("two generated keys are identical")
	}

	// And the public key must be derivable from the private one.
	derived, err := PublicKeyOf(pair.PrivateKey, "")
	if err != nil {
		t.Fatalf("PublicKeyOf: %v", err)
	}
	if !strings.HasPrefix(pair.PublicKey, derived) {
		t.Fatalf("derived public key %q does not match %q", derived, pair.PublicKey)
	}
}

func TestLineReaderHandlesCarriageReturns(t *testing.T) {
	// curl and the k3s installer draw progress with \r; without splitting on it
	// a whole download arrives as one enormous line.
	r := newLineReader(strings.NewReader("downloading 10%\rdownloading 50%\rdone\nnext line\n"))
	var lines []string
	for {
		line, err := r.ReadLine()
		if line != "" {
			lines = append(lines, line)
		}
		if err != nil {
			break
		}
	}
	if len(lines) != 4 {
		t.Fatalf("got %d lines (%v), want 4", len(lines), lines)
	}
	if lines[2] != "done" || lines[3] != "next line" {
		t.Fatalf("lines are %v", lines)
	}
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}
