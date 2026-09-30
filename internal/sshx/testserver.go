package sshx

import (
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

// TestServer is a real SSH server used by tests.
//
// Testing the provisioning flow against a recording SSH server rather than a
// mocked client means the things that actually break are covered: command
// construction, quoting, exit code handling and the order of steps. It lives in
// the package rather than a _test file so the provision package can use it too.
type TestServer struct {
	listener net.Listener
	config   *ssh.ServerConfig

	mu sync.Mutex
	// Commands records every command the server was asked to run, in order.
	commands []string
	// responses map a substring of a command to the reply it gets. The first
	// matching entry wins, so more specific patterns must be registered first.
	responses []responseRule
	// AuthAttempts records the authentication methods that were tried.
	authAttempts []string
	// installedKeys holds the keys that were added through an authorized_keys
	// command, so the server behaves like a real one: a key it was just given
	// works on the next connection.
	installedKeys []string
	closed        bool
}

// installedKeyPattern matches the key in the script that installs it.
// Single-quoted, because that is what the panel generates: see
// internal/shellsafe. A pattern matching the double-quoted form would keep
// passing while the real script had changed shape.
var installedKeyPattern = regexp.MustCompile(`KEY='(ssh-[^']+)'`)

type responseRule struct {
	match    string
	stdout   string
	stderr   string
	exitCode int
}

// TestServerOptions configure the server.
type TestServerOptions struct {
	// Password, when set, is the only password accepted.
	Password string
	// AuthorizedKey, when set, is the only public key accepted.
	AuthorizedKey string
	// RejectAllAuth makes every attempt fail, for testing the error path.
	RejectAllAuth bool
}

// NewTestServer starts a server on a random local port.
func NewTestServer(opts TestServerOptions) (*TestServer, error) {
	hostKey, err := GenerateKeyPair("test-host")
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey([]byte(hostKey.PrivateKey))
	if err != nil {
		return nil, fmt.Errorf("parse the test host key: %w", err)
	}

	server := &TestServer{}
	config := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			server.recordAuth("password")
			if opts.RejectAllAuth || opts.Password == "" || string(password) != opts.Password {
				return nil, fmt.Errorf("password rejected")
			}
			return &ssh.Permissions{}, nil
		},
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			server.recordAuth("publickey")
			if opts.RejectAllAuth {
				return nil, fmt.Errorf("public key rejected")
			}
			offered := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
			if matchesAuthorizedKey(offered, opts.AuthorizedKey) {
				return &ssh.Permissions{}, nil
			}
			for _, installed := range server.InstalledKeys() {
				if matchesAuthorizedKey(offered, installed) {
					return &ssh.Permissions{}, nil
				}
			}
			return nil, fmt.Errorf("public key rejected")
		},
	}
	config.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	server.listener = listener
	server.config = config

	go server.accept()
	return server, nil
}

// Addr is the host and port the server is listening on.
func (s *TestServer) Addr() (string, int) {
	addr := s.listener.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port
}

// Respond registers a canned reply for commands containing match.
//
// Registering the same match again replaces the previous reply, which is how a
// test says "this command fails, and now it succeeds".
func (s *TestServer) Respond(match, stdout string, exitCode int) {
	s.RespondStderr(match, stdout, "", exitCode)
}

// RespondStderr registers a reply that also writes to stderr.
func (s *TestServer) RespondStderr(match, stdout, stderr string, exitCode int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rule := responseRule{match: match, stdout: stdout, stderr: stderr, exitCode: exitCode}
	for i := range s.responses {
		if s.responses[i].match == match {
			// Replace in place, so registration order still decides which of
			// two different patterns wins.
			s.responses[i] = rule
			return
		}
	}
	s.responses = append(s.responses, rule)
}

// Commands returns everything the server was asked to run.
func (s *TestServer) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...)
}

// Ran reports whether any command contained the given substring.
func (s *TestServer) Ran(substring string) bool {
	for _, c := range s.Commands() {
		if strings.Contains(c, substring) {
			return true
		}
	}
	return false
}

// AuthAttempts returns the authentication methods that were tried.
func (s *TestServer) AuthAttempts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.authAttempts...)
}

// Close stops the server.
func (s *TestServer) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return s.listener.Close()
}

// InstalledKeys returns the keys that were added through a command.
func (s *TestServer) InstalledKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.installedKeys...)
}

// matchesAuthorizedKey compares an offered key with an authorized_keys line,
// ignoring the trailing comment the way sshd does.
func matchesAuthorizedKey(offered, authorized string) bool {
	offeredFields := strings.Fields(offered)
	authorizedFields := strings.Fields(authorized)
	if len(offeredFields) < 2 || len(authorizedFields) < 2 {
		return false
	}
	return offeredFields[0] == authorizedFields[0] && offeredFields[1] == authorizedFields[1]
}

func (s *TestServer) recordAuth(method string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authAttempts = append(s.authAttempts, method)
}

func (s *TestServer) accept() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return
			}
			continue
		}
		go s.handle(conn)
	}
}

func (s *TestServer) handle(conn net.Conn) {
	defer conn.Close()
	sshConn, chans, reqs, err := ssh.NewServerConn(conn, s.config)
	if err != nil {
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "only sessions are supported")
			continue
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go s.handleSession(channel, requests)
	}
}

func (s *TestServer) handleSession(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()
	for req := range requests {
		switch req.Type {
		case "exec":
			command := parseExecPayload(req.Payload)
			_ = req.Reply(true, nil)
			s.runCommand(channel, command)
			return
		case "shell":
			_ = req.Reply(true, nil)
		default:
			_ = req.Reply(false, nil)
		}
	}
}

func (s *TestServer) runCommand(channel ssh.Channel, command string) {
	s.mu.Lock()
	s.commands = append(s.commands, command)
	// A command that installs a key makes that key work from now on, which is
	// what a real server does and what the provisioner's verification step
	// depends on.
	if match := installedKeyPattern.FindStringSubmatch(command); match != nil {
		s.installedKeys = append(s.installedKeys, match[1])
	}
	var rule *responseRule
	for i := range s.responses {
		if strings.Contains(command, s.responses[i].match) {
			rule = &s.responses[i]
			break
		}
	}
	s.mu.Unlock()

	stdout, stderr, exitCode := "", "", 0
	if rule != nil {
		stdout, stderr, exitCode = rule.stdout, rule.stderr, rule.exitCode
	}

	if stdout != "" {
		_, _ = io.WriteString(channel, stdout)
		if !strings.HasSuffix(stdout, "\n") {
			_, _ = io.WriteString(channel, "\n")
		}
	}
	if stderr != "" {
		_, _ = io.WriteString(channel.Stderr(), stderr+"\n")
	}
	_, _ = channel.SendRequest("exit-status", false, exitStatusPayload(exitCode))
	_ = channel.CloseWrite()
}

// parseExecPayload reads the command out of an exec request, which is a
// length-prefixed string.
func parseExecPayload(payload []byte) string {
	if len(payload) < 4 {
		return ""
	}
	length := int(payload[0])<<24 | int(payload[1])<<16 | int(payload[2])<<8 | int(payload[3])
	if length > len(payload)-4 {
		length = len(payload) - 4
	}
	return string(payload[4 : 4+length])
}

func exitStatusPayload(code int) []byte {
	return []byte{byte(code >> 24), byte(code >> 16), byte(code >> 8), byte(code)}
}
