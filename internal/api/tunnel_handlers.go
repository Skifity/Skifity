package api

import (
	"bytes"
	"context"
	"errors"
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

// An open tunnel is asked again, every tunnelRecheck, whether whoever opened
// it still may: a token revoked, a member removed or an admin made a member
// ends their tunnels within that, rather than whenever they next hang up.
// None lasts beyond tunnelMaxLife; `db connect` opens a new one for the next
// connection a client makes. Variables, so a test need not wait a minute.
//
// A recheck that cannot be answered — the panel's database busy or briefly
// unreadable — is not a no, and closing on it cut a working tunnel in the
// middle of a migration or a dump. tunnelPatience of them in a row is.
var (
	tunnelRecheck  = time.Minute
	tunnelMaxLife  = 12 * time.Hour
	tunnelPatience = 3
)

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
	// Recorded once the tunnel is open, not when it was asked for.
	teamID, _ := s.db.TeamIDForDatabase(r.Context(), record.ID)
	s.audit(r, teamID, "database.tunnel_opened", "database", record.ID, record.Name)

	// Not the request's context: the server cancels that when it reads the
	// client's end of stream, and a client that half-closes has not left.
	open, end := context.WithTimeout(context.WithoutCancel(r.Context()), tunnelMaxLife)
	defer end()
	every, patience := tunnelRecheck, tunnelPatience
	go func() {
		defer runsafe.Recover(s.log, "rechecking a database tunnel", nil)
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		unanswered := 0
		for {
			select {
			case <-open.Done():
				return
			case <-ticker.C:
				allowed, err := s.stillAllowed(r, record.ID)
				switch {
				case err != nil:
					unanswered++
					if unanswered < patience {
						s.log.Warn("could not recheck a database tunnel; it stays open for now", "database", record.ID, "error", err)
						continue
					}
					s.log.Warn("closed a database tunnel that could not be rechecked", "database", record.ID, "error", err)
				case allowed:
					unanswered = 0
					continue
				default:
					s.log.Info("closed a database tunnel whose access was withdrawn", "database", record.ID)
				}
				end()
				return
			}
		}
	}()
	// What the client sent behind the request is already in the server's
	// buffer; the rest is read from the connection itself, for the same
	// reason.
	fromClient := io.Reader(conn)
	if n := buffered.Reader.Buffered(); n > 0 {
		early, _ := buffered.Peek(n)
		fromClient = io.MultiReader(bytes.NewReader(bytes.Clone(early)), conn)
	}
	pipe(open, s, conn, fromClient, upstream)
}

// stillAllowed signs the tunnel's request in again, with the credential it
// was opened with, and asks whether that may still open this database's
// tunnel. An error means it could not be asked; false means no.
//
// A cookie session is looked at without being extended: the tunnel asking
// every minute is not the person doing anything.
func (s *Server) stillAllowed(r *http.Request, databaseID string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx, err := s.identifyAs(ctx, r, s.auth.CheckSession)
	if err != nil {
		if refused(err) {
			return false, nil
		}
		return false, err
	}
	if _, ok := UserFrom(ctx); !ok {
		return false, nil
	}
	if _, _, err := s.authorizeDatabase(r.WithContext(ctx), databaseID, store.RoleAdmin); err != nil {
		if refused(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// refused reports whether an error is an answer — not signed in, not
// allowed, not there — rather than a failure to find one out.
func refused(err error) bool {
	var problem *errdoc.Problem
	if !errors.As(err, &problem) {
		return false
	}
	switch problem.Status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return true
	}
	return false
}

// pipe copies both ways until both are done, or ctx ends. The end of one
// direction is passed on as the end of that direction alone: a client that
// has sent all it will still hears the database's answer. An error either
// way, or ctx ending, closes both.
func pipe(ctx context.Context, s *Server, client net.Conn, fromClient io.Reader, upstream net.Conn) {
	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			_ = client.Close()
			_ = upstream.Close()
		})
	}
	defer closeBoth()
	defer context.AfterFunc(ctx, closeBoth)()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer runsafe.Recover(s.log, "a database tunnel, towards the database", func(error) { closeBoth() })
		copyHalf(upstream, fromClient, closeBoth)
	}()
	go func() {
		defer wg.Done()
		defer runsafe.Recover(s.log, "a database tunnel, towards the client", func(error) { closeBoth() })
		copyHalf(client, upstream, closeBoth)
	}()
	wg.Wait()
}

// copyHalf copies until from ends and then closes only the writing half of
// to. When to has no half to close, or the copy failed, it closes both.
func copyHalf(to io.Writer, from io.Reader, closeBoth func()) {
	if _, err := io.Copy(to, from); err == nil {
		if half, ok := to.(interface{ CloseWrite() error }); ok && half.CloseWrite() == nil {
			return
		}
	}
	closeBoth()
}
