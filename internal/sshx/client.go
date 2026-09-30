// Package sshx is the panel's SSH client: connecting to a bare VPS, verifying
// its identity, running commands and copying files.
package sshx

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"skifity/internal/runsafe"
)

// Credentials are what the user typed into the Add Server form.
type Credentials struct {
	User string
	// Password is used once, to install the panel's own key, and is never stored.
	Password string
	// PrivateKey is a PEM private key, either supplied by the user or generated
	// by the panel.
	PrivateKey string
	// Passphrase decrypts PrivateKey when it is encrypted.
	Passphrase string
}

// Config describes a connection.
type Config struct {
	Host string
	Port int
	Credentials
	// HostKey is the fingerprint recorded when the server was added. Empty means
	// this is the first connection and the key should be learned.
	HostKey string
	// Timeout bounds the TCP and handshake phases.
	Timeout time.Duration
}

// Errors this package returns. Callers distinguish them to produce the right
// explanation: unreachable, wrong credentials and changed identity need very
// different messages.
var (
	ErrUnreachable    = errors.New("could not reach the server over SSH")
	ErrAuthFailed     = errors.New("the server refused those credentials")
	ErrHostKeyChanged = errors.New("the server's SSH host key has changed")
	ErrNoCredentials  = errors.New("no password or private key was supplied")
)

// Client is a live SSH connection.
type Client struct {
	conn *ssh.Client
	// HostKey is the fingerprint the server presented, to store on first use.
	HostKey string
	host    string
	port    int
}

// Dial opens a connection.
//
// On the first connection the host key is learned and returned; on later ones it
// must match, and a mismatch is refused rather than warned about, because the
// panel runs privileged commands over this connection.
func Dial(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Port == 0 {
		cfg.Port = 22
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.User == "" {
		cfg.User = "root"
	}

	auth, err := authMethods(cfg.Credentials)
	if err != nil {
		return nil, err
	}

	var presented string
	clientConfig := &ssh.ClientConfig{
		User:    cfg.User,
		Auth:    auth,
		Timeout: cfg.Timeout,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			presented = Fingerprint(key)
			if cfg.HostKey == "" {
				// Trust on first use. The fingerprint is stored and shown, so a
				// later change is detectable.
				return nil
			}
			if cfg.HostKey != presented {
				return fmt.Errorf("%w: expected %s, got %s", ErrHostKeyChanged, cfg.HostKey, presented)
			}
			return nil
		},
		// Modern algorithms only. Leaving the defaults would accept ssh-rsa
		// with SHA-1, which is deprecated.
		HostKeyAlgorithms: []string{
			ssh.KeyAlgoED25519,
			ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521,
			ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSASHA512,
		},
	}

	address := net.JoinHostPort(cfg.Host, fmt.Sprint(cfg.Port))
	dialer := net.Dialer{Timeout: cfg.Timeout}
	rawConn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrUnreachable, address, err)
	}

	// The handshake has its own deadline: a host that accepts TCP but never
	// completes the handshake would otherwise hang until the context expires.
	_ = rawConn.SetDeadline(time.Now().Add(cfg.Timeout))
	sshConn, chans, reqs, err := ssh.NewClientConn(rawConn, address, clientConfig)
	if err != nil {
		rawConn.Close()
		switch {
		case errors.Is(err, ErrHostKeyChanged) || strings.Contains(err.Error(), "host key"):
			return nil, fmt.Errorf("%w: %s presented %s", ErrHostKeyChanged, cfg.Host, presented)
		case strings.Contains(err.Error(), "unable to authenticate"),
			strings.Contains(err.Error(), "no supported methods"),
			strings.Contains(err.Error(), "handshake failed"):
			return nil, fmt.Errorf("%w: %w", ErrAuthFailed, err)
		default:
			return nil, fmt.Errorf("%w: %w", ErrUnreachable, err)
		}
	}
	_ = rawConn.SetDeadline(time.Time{})

	return &Client{
		conn:    ssh.NewClient(sshConn, chans, reqs),
		HostKey: presented,
		host:    cfg.Host,
		port:    cfg.Port,
	}, nil
}

func authMethods(creds Credentials) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod
	if creds.PrivateKey != "" {
		var signer ssh.Signer
		var err error
		if creds.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(creds.PrivateKey), []byte(creds.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(creds.PrivateKey))
		}
		if err != nil {
			if strings.Contains(err.Error(), "passphrase") {
				return nil, fmt.Errorf("this private key is protected by a passphrase; enter it as well: %w", err)
			}
			return nil, fmt.Errorf("this does not look like a valid private key: %w", err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if creds.Password != "" {
		methods = append(methods, ssh.Password(creds.Password))
		// Some servers ask for the password through keyboard-interactive
		// instead of the password method, which otherwise fails confusingly.
		methods = append(methods, ssh.KeyboardInteractive(
			func(name, instruction string, questions []string, echos []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range questions {
					answers[i] = creds.Password
				}
				return answers, nil
			}))
	}
	if len(methods) == 0 {
		return nil, ErrNoCredentials
	}
	return methods, nil
}

// Close ends the connection.
func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// Address is the host:port this client is connected to.
func (c *Client) Address() string { return net.JoinHostPort(c.host, fmt.Sprint(c.port)) }

// Result is the outcome of a command.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Output returns stdout trimmed, which is what almost every caller wants.
func (r Result) Output() string { return strings.TrimSpace(r.Stdout) }

// Combined returns stdout and stderr together, for error messages.
func (r Result) Combined() string {
	if r.Stderr == "" {
		return strings.TrimSpace(r.Stdout)
	}
	return strings.TrimSpace(r.Stdout + "\n" + r.Stderr)
}

// Run executes a command and waits for it.
//
// A non-zero exit is not an error: many of the panel's checks are "does this
// command succeed", and treating that as a failure would mean every caller
// unwrapping an ExitError.
func (c *Client) Run(ctx context.Context, command string) (Result, error) {
	session, err := c.conn.NewSession()
	if err != nil {
		return Result{}, fmt.Errorf("open an SSH session: %w", err)
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	done := make(chan error, 1)
	go func() {
		// A panic here would take the panel down, and simply recovering would
		// leave whoever is waiting on this channel with nothing to wait for.
		// So it becomes the command's failure, which is what a caller can act
		// on. The channel is local and never closed, so this send is safe.
		defer func() {
			if rec := recover(); rec != nil {
				done <- fmt.Errorf("running the command panicked: %v", rec)
			}
		}()
		done <- session.Run(command)
	}()

	select {
	case <-ctx.Done():
		// Signalling the remote process is best effort; closing the session is
		// what actually releases the local resources.
		_ = session.Signal(ssh.SIGTERM)
		return Result{Stdout: stdout.String(), Stderr: stderr.String()}, ctx.Err()
	case err := <-done:
		result := Result{Stdout: stdout.String(), Stderr: stderr.String()}
		var exitErr *ssh.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitStatus()
			return result, nil
		}
		if err != nil {
			return result, fmt.Errorf("run %q: %w", firstWord(command), err)
		}
		return result, nil
	}
}

// RunStreaming executes a command and calls onLine for each line of output as it
// arrives, which is what makes install progress visible rather than a long wait
// followed by a wall of text.
func (c *Client) RunStreaming(ctx context.Context, command string, onLine func(stream, line string)) (Result, error) {
	session, err := c.conn.NewSession()
	if err != nil {
		return Result{}, fmt.Errorf("open an SSH session: %w", err)
	}
	defer session.Close()

	stdout, err := session.StdoutPipe()
	if err != nil {
		return Result{}, fmt.Errorf("attach to stdout: %w", err)
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return Result{}, fmt.Errorf("attach to stderr: %w", err)
	}

	if err := session.Start(command); err != nil {
		return Result{}, fmt.Errorf("start %q: %w", firstWord(command), err)
	}

	var (
		mu     sync.Mutex
		outBuf strings.Builder
		errBuf strings.Builder
	)
	lines := make(chan streamLine, 256)
	quit := make(chan struct{})
	defer close(quit)

	var readers sync.WaitGroup
	readers.Add(2)
	// readers.Done is deferred before the recover, so a panic while reading a
	// stream still releases the wait: the command finishes reporting what it
	// managed to read rather than hanging or ending the process.
	go func() {
		defer readers.Done()
		defer runsafe.Recover(slog.Default(), "reading the command's output", nil)
		readStream("stdout", stdout, &mu, &outBuf, lines, quit)
	}()
	go func() {
		defer readers.Done()
		defer runsafe.Recover(slog.Default(), "reading the command's errors", nil)
		readStream("stderr", stderr, &mu, &errBuf, lines, quit)
	}()

	readersDone := make(chan struct{})
	go func() {
		defer runsafe.Recover(slog.Default(), "waiting for the command's output", nil)
		readers.Wait()
		close(readersDone)
	}()

	waitErr := make(chan error, 1)
	go func() {
		// As above: a panic becomes the command's failure rather than a wait
		// that never ends.
		defer func() {
			if rec := recover(); rec != nil {
				waitErr <- fmt.Errorf("waiting for the command panicked: %v", rec)
			}
		}()
		waitErr <- session.Wait()
	}()

	// The command is finished only when the process has exited *and* both
	// streams have reached EOF. Returning on the exit status alone loses
	// whatever output was still in flight, which is exactly the output that
	// explains a failure.
	var runErr error
	exited, drained := false, false
	for !exited || !drained {
		select {
		case <-ctx.Done():
			_ = session.Signal(ssh.SIGTERM)
			mu.Lock()
			result := Result{Stdout: outBuf.String(), Stderr: errBuf.String()}
			mu.Unlock()
			return result, ctx.Err()
		case line := <-lines:
			if onLine != nil {
				onLine(line.stream, line.text)
			}
		case err := <-waitErr:
			runErr = err
			exited = true
		case <-readersDone:
			readersDone = nil // a closed channel is always ready; stop selecting on it
			drained = true
		}
	}

	// Anything the readers queued before finishing.
	for {
		select {
		case line := <-lines:
			if onLine != nil {
				onLine(line.stream, line.text)
			}
			continue
		default:
		}
		break
	}

	mu.Lock()
	result := Result{Stdout: outBuf.String(), Stderr: errBuf.String()}
	mu.Unlock()

	var exitErr *ssh.ExitError
	if errors.As(runErr, &exitErr) {
		result.ExitCode = exitErr.ExitStatus()
		return result, nil
	}
	if runErr != nil {
		return result, fmt.Errorf("run %q: %w", firstWord(command), runErr)
	}
	return result, nil
}

// streamLine is one line of output with the stream it came from.
type streamLine struct {
	stream string
	text   string
}

// readStream reads a pipe to EOF, buffering everything and forwarding each line.
//
// The send blocks rather than dropping, because a dropped line is usually the
// error message. quit releases it if the caller has given up.
func readStream(stream string, r io.Reader, mu *sync.Mutex, buf *strings.Builder, out chan<- streamLine, quit <-chan struct{}) {
	reader := newLineReader(r)
	for {
		line, err := reader.ReadLine()
		if line != "" {
			mu.Lock()
			buf.WriteString(line)
			buf.WriteByte('\n')
			mu.Unlock()
			select {
			case out <- streamLine{stream: stream, text: line}:
			case <-quit:
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// WriteFile writes content to a path on the server with the given mode.
//
// This uses a shell heredoc rather than SFTP: it needs no extra subsystem on the
// server, works on minimal images, and the files the panel writes are small
// configuration files, not large payloads.
func (c *Client) WriteFile(ctx context.Context, path, content string, mode string) error {
	if strings.Contains(path, "'") {
		return fmt.Errorf("refusing to write to a path containing a quote: %q", path)
	}
	// A quoted heredoc delimiter stops the shell expanding anything inside the
	// content, so a password or a $ in a config file survives intact.
	const delimiter = "SKIFITY_EOF_a7f3c9"
	if strings.Contains(content, delimiter) {
		return fmt.Errorf("refusing to write content containing the heredoc delimiter")
	}

	command := fmt.Sprintf("set -e; mkdir -p '%s'; cat > '%s' <<'%s'\n%s\n%s\nchmod %s '%s'",
		parentDir(path), path, delimiter, content, delimiter, mode, path)

	result, err := c.Run(ctx, command)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("write %s: %s", path, result.Combined())
	}
	return nil
}

// ReadFile reads a file from the server.
func (c *Client) ReadFile(ctx context.Context, path string) (string, error) {
	if strings.Contains(path, "'") {
		return "", fmt.Errorf("refusing to read a path containing a quote: %q", path)
	}
	result, err := c.Run(ctx, fmt.Sprintf("cat '%s'", path))
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("read %s: %s", path, result.Combined())
	}
	return result.Stdout, nil
}

// Fingerprint renders a host key the way OpenSSH does, so it can be compared
// with what `ssh-keyscan` shows.
func Fingerprint(key ssh.PublicKey) string {
	return ssh.FingerprintSHA256(key)
}

// FingerprintAuthorizedKey renders a public key in authorized_keys form.
func FingerprintAuthorizedKey(key ssh.PublicKey) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
}

// ParseHostKeyLine reads a fingerprint from a stored host key entry.
func ParseHostKeyLine(line string) (string, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", fmt.Errorf("that does not look like a host key line")
	}
	raw, err := base64.StdEncoding.DecodeString(fields[len(fields)-1])
	if err != nil {
		return "", fmt.Errorf("the host key is not valid base64: %w", err)
	}
	key, err := ssh.ParsePublicKey(raw)
	if err != nil {
		return "", fmt.Errorf("the host key could not be parsed: %w", err)
	}
	return Fingerprint(key), nil
}

func firstWord(command string) string {
	if idx := strings.IndexAny(command, " \t\n"); idx > 0 {
		return command[:idx]
	}
	return command
}

func parentDir(path string) string {
	idx := strings.LastIndexByte(path, '/')
	if idx <= 0 {
		return "."
	}
	return path[:idx]
}
