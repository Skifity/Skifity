package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"skifity/internal/errdoc"
	"skifity/internal/runsafe"
)

// fakeTunnelPanel opens a tunnel that echoes, or refuses one while refuse is
// set, the way the panel does: a problem, not a closed connection.
func fakeTunnelPanel(t *testing.T, refuse *atomic.Bool) *httptest.Server {
	t.Helper()
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/databases/db_1/tunnel" || r.Header.Get("Upgrade") != TunnelProtocol ||
			r.Header.Get("Authorization") != "Bearer skf_fake" {
			http.Error(w, "not what a tunnel asks for", http.StatusBadRequest)
			return
		}
		if refuse.Load() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, `{"error":{"code":"database.tunnel_unreachable",`+
				`"title":"The panel could not reach this database","fix":"Check the database is running."}}`)
			return
		}
		conn, buffered, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = buffered.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: " + TunnelProtocol +
			"\r\nConnection: Upgrade\r\n\r\n")
		_ = buffered.Flush()
		_, _ = io.Copy(conn, buffered)
	}))
	t.Cleanup(panel.Close)
	return panel
}

func TestATunnelRelaysEachLocalConnection(t *testing.T) {
	var refuse atomic.Bool
	panel := fakeTunnelPanel(t, &refuse)
	client := NewClient(Config{PanelURL: panel.URL, Token: "skf_fake"})

	listener, err := listenLocally(t.Context(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if host, _, _ := net.SplitHostPort(listener.Addr().String()); host != "127.0.0.1" {
		t.Fatalf("a tunnel listens on %s, where the rest of the network can use it", host)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		defer runsafe.Recover(nil, "the tunnel under test", nil)
		done <- serveTunnel(ctx, client, "db_1", listener, &out)
	}()

	first := dialLocal(t, listener)
	for _, line := range []string{"SELECT 1;\n", "SELECT 2;\n"} {
		if _, err := io.WriteString(first, line); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(line))
		if _, err := io.ReadFull(first, got); err != nil || string(got) != line {
			t.Fatalf("sent %q and read back %q (%v)", line, got, err)
		}
	}

	// A refused tunnel closes that connection and says why; the next one
	// still gets its chance.
	refuse.Store(true)
	second := dialLocal(t, listener)
	if n, _ := second.Read(make([]byte, 1)); n != 0 {
		t.Fatal("a refused tunnel carried data")
	}

	// Stopping the command ends it even with a connection still open: the
	// first one was never closed.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stopping returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Ctrl-C did not end the tunnel while a client was connected")
	}
	if !strings.Contains(out.String(), "could not be tunnelled: The panel could not reach this database. Check the database is running.") {
		t.Fatalf("the refusal was reported as %q", out.String())
	}
}

func TestARelayPassesOnTheEndOfOneDirectionAlone(t *testing.T) {
	// A database that answers once its client has said everything, the way
	// `nc -N` or a file piped into a client ends.
	database, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	go func() {
		defer runsafe.Recover(nil, "the test's counting database", nil)
		conn, err := database.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		n, _ := io.Copy(io.Discard, conn)
		_, _ = io.WriteString(conn, fmt.Sprintf("read %d bytes", n))
	}()
	remote, err := net.Dial("tcp", database.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	listener, err := listenLocally(t.Context(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		defer runsafe.Recover(nil, "the relay under test", nil)
		local, err := listener.Accept()
		if err != nil {
			return
		}
		relay(local, remote)
	}()

	client := dialLocal(t, listener)
	if _, err := io.WriteString(client, "COPY orders FROM STDIN;"); err != nil {
		t.Fatal(err)
	}
	if err := client.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	answer, err := io.ReadAll(client)
	if err != nil || string(answer) != "read 23 bytes" {
		t.Fatalf("after saying everything, the client read %q (%v)", answer, err)
	}
}

// failingListener hands out one real connection and then fails, the way
// accept does when the process runs out of file descriptors.
type failingListener struct {
	net.Listener
	accepted atomic.Int32
}

func (l *failingListener) Accept() (net.Conn, error) {
	if l.accepted.Add(1) == 1 {
		return l.Listener.Accept()
	}
	return nil, errors.New("accept: too many open files")
}

func TestAFailedAcceptEndsTheTunnelsRatherThanWaitingForThem(t *testing.T) {
	var refuse atomic.Bool
	panel := fakeTunnelPanel(t, &refuse)
	client := NewClient(Config{PanelURL: panel.URL, Token: "skf_fake"})
	real, err := listenLocally(t.Context(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	listener := &failingListener{Listener: real}

	// The first connection is open, and never closed by its client, when
	// the second accept fails.
	first := make(chan net.Conn, 1)
	go func() {
		defer runsafe.Recover(nil, "the test's client", nil)
		conn, err := net.Dial("tcp", real.Addr().String())
		if err == nil {
			first <- conn
		}
	}()
	done := make(chan error, 1)
	go func() {
		defer runsafe.Recover(nil, "the tunnel under test", nil)
		done <- serveTunnel(t.Context(), client, "db_1", listener, io.Discard)
	}()
	conn := <-first
	t.Cleanup(func() { _ = conn.Close() })

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "too many open files") {
			t.Fatalf("a failed accept returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a failed accept waited for a client that never hangs up")
	}
	if _, err := net.DialTimeout("tcp", real.Addr().String(), time.Second); err == nil {
		t.Fatal("the port is still listening after the tunnel ended")
	}
}

func dialLocal(t *testing.T, listener net.Listener) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	return conn
}

func TestAPortAskedForIsThatPortOrAnError(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()
	port := taken.Addr().(*net.TCPAddr).Port

	var problem *errdoc.Problem
	if _, err := listenLocally(t.Context(), port, 0); !errors.As(err, &problem) || problem.Code != "cli.port_in_use" {
		t.Fatalf("a taken port asked for: %v", err)
	}
	// The database's own port is only a preference.
	listener, err := listenLocally(t.Context(), 0, port)
	if err != nil {
		t.Fatalf("a taken default port was not stepped around: %v", err)
	}
	_ = listener.Close()
}

func TestTheLocalURLPointsAtTheTunnel(t *testing.T) {
	cases := map[string]string{
		"postgres://app:s3cret@orders.team-prod.svc.cluster.local:5432/orders?sslmode=disable": "postgres://app:s3cret@127.0.0.1:6543/orders?sslmode=disable",
		"redis://:s3cret@cache.team-prod.svc:6379/0":                                           "redis://:s3cret@127.0.0.1:6543/0",
		"": "",
	}
	for connection, want := range cases {
		if got := localURL(connection, "127.0.0.1:6543"); got != want {
			t.Errorf("localURL(%q) = %q, want %q", connection, got, want)
		}
	}
}
