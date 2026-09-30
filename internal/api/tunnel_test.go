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
