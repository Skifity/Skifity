package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"skifity/internal/cli"
	"skifity/internal/errdoc"
	"skifity/internal/events"
	"skifity/internal/runsafe"
	"skifity/internal/store"
)

// tunnelDatabases answers credentials that point at a listener of the test's.
type tunnelDatabases struct {
	fakeDatabases
	address string
}

func (f *tunnelDatabases) Credentials(context.Context, string) (DatabaseCredentials, error) {
	host, port, _ := net.SplitHostPort(f.address)
	n, _ := strconv.Atoi(port)
	return DatabaseCredentials{Engine: "postgres", Host: host, Port: n}, nil
}

// echoServer stands in for a database: it says back whatever it is sent.
func echoServer(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		defer runsafe.Recover(nil, "the test's echo server", nil)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer runsafe.Recover(nil, "an echo", nil)
				defer func() { _ = conn.Close() }()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return listener
}

func TestATunnelCarriesADatabasesOwnProtocol(t *testing.T) {
	// The API's half and the CLI's half, against each other: the request the
	// CLI makes is the one this handler has to answer.
	if TunnelProtocol != cli.TunnelProtocol {
		t.Fatalf("the panel opens %q and the CLI asks for %q", TunnelProtocol, cli.TunnelProtocol)
	}

	h := newHarness(t)
	acme := h.newTenant("acme")
	database := h.database(acme, "orders")
	databases := &tunnelDatabases{address: echoServer(t).Addr().String()}
	h.withDatabases(databases)
	open := func(as tenant) (io.ReadWriteCloser, error) {
		return cli.NewClient(cli.Config{PanelURL: h.server.URL, Token: as.token}).OpenTunnel(t.Context(), database.ID)
	}

	conn, err := open(acme)
	if err != nil {
		t.Fatalf("the tunnel was not opened: %v", err)
	}
	// Both ways, twice, so it is a connection and not one answer.
	for _, line := range []string{"SELECT 1;\n", "SELECT 2;\n"} {
		if _, err := io.WriteString(conn, line); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(line))
		if _, err := io.ReadFull(conn, got); err != nil || string(got) != line {
			t.Fatalf("sent %q and read back %q (%v)", line, got, err)
		}
	}
	_ = conn.Close()

	entries, err := h.db.ListAudit(t.Context(), acme.team.ID, "database.tunnel_opened", database.ID, 10)
	if err != nil || len(entries) != 1 {
		t.Fatalf("a tunnel is the same access as the password and was not audited: %d entries, %v", len(entries), err)
	}

	// A member can link a database to an app, but not read its password, so
	// not this either; the route walk covers viewers and other teams.
	member := h.newMember(acme, "dev", store.RoleMember)
	if _, err := open(member); !isCode(err, "auth.forbidden") {
		t.Fatalf("a member opened a tunnel: %v", err)
	}

	// Asked for without the upgrade, it is refused before anything is dialled.
	if status, _ := h.do(acme, http.MethodPost, "/api/databases/"+database.ID+"/tunnel", nil); status != http.StatusBadRequest {
		t.Fatalf("a plain POST answered %d", status)
	}

	// Nothing listening: the CLI is told why, not handed a dead connection.
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	databases.address = closed.Addr().String()
	_ = closed.Close()
	if _, err := open(acme); !isCode(err, "database.tunnel_unreachable") {
		t.Fatalf("a database that does not answer: %v", err)
	}
}

func TestATunnelEndsWhenItsOpenerMayNoLonger(t *testing.T) {
	// Removing somebody from the team ends the tunnels they already had,
	// rather than whenever they next hang up.
	was := tunnelRecheck
	tunnelRecheck = 20 * time.Millisecond
	t.Cleanup(func() { tunnelRecheck = was })

	h := newHarness(t)
	acme := h.newTenant("acme")
	database := h.database(acme, "orders")
	h.withDatabases(&tunnelDatabases{address: echoServer(t).Addr().String()})
	ops := h.newMember(acme, "ops", store.RoleAdmin)
	conn, err := cli.NewClient(cli.Config{PanelURL: h.server.URL, Token: ops.token}).OpenTunnel(t.Context(), database.ID)
	if err != nil {
		t.Fatalf("an admin's tunnel was not opened: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := io.WriteString(conn, "SELECT 1;\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(conn, make([]byte, len("SELECT 1;\n"))); err != nil {
		t.Fatalf("the tunnel did not carry anything while allowed: %v", err)
	}

	if err := h.db.RemoveMember(t.Context(), acme.team.ID, ops.user.ID); err != nil {
		t.Fatal(err)
	}
	ended := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, conn)
		ended <- err
	}()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the tunnel of somebody no longer in the team stayed open")
	}
}

func TestATunnelPassesOnTheEndOfOneDirectionAlone(t *testing.T) {
	// A database that answers once its client has said everything: the way
	// `nc -N` or a file piped in ends. Closing both ways at the first end of
	// stream loses that answer.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		defer runsafe.Recover(nil, "the test's counting server", nil)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		n, _ := io.Copy(io.Discard, conn)
		_, _ = io.WriteString(conn, "read "+strconv.FormatInt(n, 10)+" bytes")
	}()

	h := newHarness(t)
	acme := h.newTenant("acme")
	database := h.database(acme, "orders")
	h.withDatabases(&tunnelDatabases{address: listener.Addr().String()})
	conn, err := cli.NewClient(cli.Config{PanelURL: h.server.URL, Token: acme.token}).OpenTunnel(t.Context(), database.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := io.WriteString(conn, "COPY orders FROM STDIN;"); err != nil {
		t.Fatal(err)
	}
	half, ok := conn.(interface{ CloseWrite() error })
	if !ok {
		t.Fatalf("the CLI's end of a tunnel (%T) cannot be half-closed", conn)
	}
	if err := half.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	answer, err := io.ReadAll(conn)
	if err != nil || string(answer) != "read 23 bytes" {
		t.Fatalf("after saying everything, the client read %q (%v)", answer, err)
	}
}

func TestATunnelNeedsTheDatabasesManager(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	database := h.database(acme, "orders")
	h.server.Config.Handler = New(Options{
		DB: h.db, Keyring: h.keyring, Auth: h.auth,
		Hub: events.NewHub(16), Logger: slog.New(slog.DiscardHandler),
	})
	_, err := cli.NewClient(cli.Config{PanelURL: h.server.URL, Token: acme.token}).OpenTunnel(t.Context(), database.ID)
	if err == nil || !strings.Contains(err.Error(), "Managed databases") {
		t.Fatalf("a panel with no cluster opened a tunnel: %v", err)
	}
}

func isCode(err error, code string) bool {
	var problem *errdoc.Problem
	return errors.As(err, &problem) && problem.Code == code
}
