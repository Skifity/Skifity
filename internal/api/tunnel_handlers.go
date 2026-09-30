package api

import (
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/runsafe"
	"skifity/internal/store"
)

// A tunnel to a database, for `skifity db connect`.
//
// A managed database has no address outside the cluster, on purpose, and that
// left a person with a laptop, a SQL client and no way to use them: Coolify,
// Fly and Sealos all offer the same thing. The panel already reaches every
// database; this lends that reach to one connection at a time. The CLI listens
// on a local port and, for each connection a client makes to it, asks for a
// tunnel here: an HTTP upgrade, after which the connection carries the
// database's own protocol both ways until either end closes it.
//
// A POST, so a read-only token cannot open one — a database connection writes
// — and an admin's, audited, because it is the same access as reading the
// password, which is what it is used with.

// TunnelProtocol is the Upgrade token a tunnel is asked for with.
const TunnelProtocol = "skifity-db-tunnel"

// tunnelDialTimeout bounds reaching the database.
const tunnelDialTimeout = 10 * time.Second

func (s *Server) handleDatabaseTunnel(w http.ResponseWriter, r *http.Request) {
	record, _, err := s.authorizeDatabase(r, chi.URLParam(r, "databaseID"), store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), TunnelProtocol) {
		writeError(w, r, errdoc.BadRequest("A tunnel is opened by `skifity db connect`, which asks for it with an Upgrade header."))
		return
	}
	if s.databases == nil {
		writeError(w, r, errdoc.NotConfigured("Managed databases", "the panel's cluster connection"))
		return
	}
	credentials, err := s.databases.Credentials(r.Context(), record.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	dialCtx, cancel := context.WithTimeout(r.Context(), tunnelDialTimeout)
	defer cancel()
	upstream, err := (&net.Dialer{}).DialContext(dialCtx, "tcp",
		net.JoinHostPort(credentials.Host, strconv.Itoa(credentials.Port)))
	if err != nil {
		writeError(w, r, errdoc.TunnelUnreachable(record.Name))
		return
	}

	teamID, _ := s.db.TeamIDForDatabase(r.Context(), record.ID)
	s.audit(r, teamID, "database.tunnel_opened", "database", record.ID, record.Name)

	conn, buffered, err := http.NewResponseController(w).Hijack()
	if err != nil {
		_ = upstream.Close()
		writeError(w, r, err)
		return
	}
	// The server's deadlines were for a request; this is a connection that
	// lasts as long as somebody's session does.
	_ = conn.SetDeadline(time.Time{})
	if _, err := buffered.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: " + TunnelProtocol +
		"\r\nConnection: Upgrade\r\n\r\n"); err != nil || buffered.Flush() != nil {
		_ = conn.Close()
		_ = upstream.Close()
		return
	}
	pipe(s, conn, buffered.Reader, upstream)
}

// pipe copies both ways until either side is done, then closes both, so
// neither goroutine outlives the other waiting on a connection nobody uses.
func pipe(s *Server, client net.Conn, fromClient io.Reader, upstream net.Conn) {
	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			_ = client.Close()
			_ = upstream.Close()
		})
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer closeBoth()
		defer runsafe.Recover(s.log, "a database tunnel, towards the database", nil)
		_, _ = io.Copy(upstream, fromClient)
	}()
	go func() {
		defer wg.Done()
		defer closeBoth()
		defer runsafe.Recover(s.log, "a database tunnel, towards the client", nil)
		_, _ = io.Copy(client, upstream)
	}()
	wg.Wait()
}
