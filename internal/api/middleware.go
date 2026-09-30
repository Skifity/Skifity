package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"skifity/internal/auth"
	"skifity/internal/crypto"
	"skifity/internal/errdoc"
	"skifity/internal/store"
	"skifity/web"
)

type contextKey string

const (
	ctxUser      contextKey = "user"
	ctxSession   contextKey = "session"
	ctxAPIToken  contextKey = "api_token"
	ctxRequestID contextKey = "request_id"
	ctxClientIP  contextKey = "client_ip"
)

// UserFrom returns the authenticated user, if any.
func UserFrom(ctx context.Context) (store.User, bool) {
	u, ok := ctx.Value(ctxUser).(store.User)
	return u, ok
}

func sessionFrom(ctx context.Context) (store.Session, bool) {
	s, ok := ctx.Value(ctxSession).(store.Session)
	return s, ok
}

func apiTokenFrom(ctx context.Context) (store.APIToken, bool) {
	t, ok := ctx.Value(ctxAPIToken).(store.APIToken)
	return t, ok
}

func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxRequestID).(string)
	return id
}

func clientIPFrom(ctx context.Context) string {
	ip, _ := ctx.Value(ctxClientIP).(string)
	return ip
}

// requestContext assigns a request id and resolves the client address once, so
// every later layer agrees on both.
func (s *Server) requestContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" || len(id) > 64 {
			generated, err := crypto.RandomToken(16)
			if err != nil {
				generated = fmt.Sprintf("req-%d", time.Now().UnixNano())
			}
			id = generated
		}
		ctx := context.WithValue(r.Context(), ctxRequestID, id)
		ctx = context.WithValue(ctx, ctxClientIP, s.clientIP(r))
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// clientIP resolves the caller's address, trusting exactly as many
// X-Forwarded-For entries as the operator says sit in front of the panel.
//
// Trusting the whole header lets anyone spoof their address and defeat the
// sign-in rate limit; trusting none breaks it behind the ingress we ship.
func (s *Server) clientIP(r *http.Request) string {
	direct, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		direct = r.RemoteAddr
	}
	if s.cfg.TrustedProxyCount <= 0 {
		return direct
	}
	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded == "" {
		return direct
	}
	parts := strings.Split(forwarded, ",")
	// The rightmost entries are the ones our own proxies appended. Step back
	// exactly TrustedProxyCount of them to find the address they saw.
	idx := len(parts) - s.cfg.TrustedProxyCount
	if idx < 0 {
		idx = 0
	}
	candidate := strings.TrimSpace(parts[idx])
	if net.ParseIP(candidate) == nil {
		return direct
	}
	return candidate
}

// recoverer turns a panic into a 500 rather than a dropped connection.
func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				// http.ErrAbortHandler is how a handler intentionally drops a
				// connection; re-panicking preserves that meaning.
				if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(rec)
				}
				s.log.Error("panic serving request",
					"panic", rec,
					"path", r.URL.Path,
					"method", r.Method,
					"request_id", requestIDFrom(r.Context()),
					"stack", string(debug.Stack()))
				writeError(w, r, errdoc.New("internal", "Something went wrong").
					WithCause("The panel hit an unexpected error while handling this request.").
					WithImpact("The action may not have completed.").
					WithFix("Try again. If it keeps happening, copy this error and open an issue.").
					With("request_id", requestIDFrom(r.Context())))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the status code for the access log.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer, which SSE
// needs for Flush.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// accessLog records one line per request.
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		level := slog.LevelInfo
		switch {
		case rec.status >= 500:
			level = slog.LevelError
		case rec.status >= 400:
			level = slog.LevelWarn
		case r.URL.Path == "/api/health" || r.URL.Path == "/api/ready":
			// Health checks run every few seconds; logging them at info level
			// drowns everything else.
			level = slog.LevelDebug
		}
		elapsed := time.Since(start)
		s.log.Log(r.Context(), level, "request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"bytes", rec.bytes,
			"duration_ms", elapsed.Milliseconds(),
			"ip", clientIPFrom(r.Context()),
			"request_id", requestIDFrom(r.Context()))

		// Labelled by the route pattern rather than the path. A label per app
		// id is how a metrics endpoint turns into a memory leak: every
		// deployment somebody ever looked at would keep a series forever.
		route := chi.RouteContext(r.Context()).RoutePattern()
		if route == "" {
			route = "other"
		}
		s.metrics.Inc("skifity_http_requests_total",
			"method", r.Method, "route", route, "status", strconv.Itoa(rec.status))
		s.metrics.Observe("skifity_http_request_duration_seconds", elapsed.Seconds(), "route", route)
	})
}

// securityHeaders sets the headers that make a browser defend the panel.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	// The panel serves its own assets and talks only to itself, so the policy
	// can be strict. 'unsafe-inline' is needed for styles because Tailwind's
	// runtime theme variables are set on the document element.
	// The one inline script in index.html sets the theme before the first
	// paint. Its hash is read out of the file that ships, so the policy and the
	// script cannot drift: without it the policy refused to run the script, and
	// every dark-mode user saw a white flash on every load — in production
	// only, because the policy is not set in dev mode.
	script := append([]string{"'self'"}, web.InlineScriptHashes()...)
	csp := strings.Join([]string{
		"default-src 'self'",
		"script-src " + strings.Join(script, " "),
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob:",
		"font-src 'self' data:",
		"connect-src 'self'",
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"object-src 'none'",
	}, "; ")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		// Nothing here is meant to be loaded by another site: not the panel's
		// own assets, and certainly not an API response. Without this a page
		// somewhere else can still pull them in with a plain <img> or <script>
		// tag, which is where a side-channel starts.
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		if !s.cfg.DevMode {
			// The dev server runs over plain http, where HSTS would lock the
			// developer out of localhost for a year.
			h.Set("Content-Security-Policy", csp)
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// authenticate resolves a session cookie or a bearer token. It does not reject
// anonymous requests; requireAuth does that, so public endpoints can still see
// who the caller is.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		if header := r.Header.Get("Authorization"); header != "" {
			token, ok := strings.CutPrefix(header, "Bearer ")
			if ok {
				user, apiToken, err := s.auth.AuthenticateToken(ctx, strings.TrimSpace(token))
				if err == nil {
					// A scope narrower than the owner's access is checked here
					// rather than per handler, so a read-only token cannot
					// reach a route that nobody thought to guard — starting
					// with the one that issues a token with no scopes at all.
					if !auth.TokenAllows(apiToken.Scopes, r.Method, r.URL.Path) {
						writeError(w, r, errdoc.New("auth.token_scope", "This token cannot make that request").
							WithCause("The token %s is limited to %s.", apiToken.Name, apiToken.Scopes).
							WithImpact("The request was refused. Nothing was changed.").
							WithFix("Use a token without a scope, or create one that can write, under Account.").
							WithStatus(http.StatusForbidden))
						return
					}
					ctx = context.WithValue(ctx, ctxUser, user)
					ctx = context.WithValue(ctx, ctxAPIToken, apiToken)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				if !errors.Is(err, store.ErrNotFound) {
					writeError(w, r, err)
					return
				}
			}
		}

		if value := s.auth.ReadCookie(r, auth.SessionCookieName); value != "" {
			user, session, err := s.auth.Authenticate(ctx, value)
			if err == nil {
				ctx = context.WithValue(ctx, ctxUser, user)
				ctx = context.WithValue(ctx, ctxSession, session)
			} else if !errors.Is(err, store.ErrNotFound) {
				writeError(w, r, err)
				return
			}
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireAuth rejects anonymous requests.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := UserFrom(r.Context()); !ok {
			writeError(w, r, errdoc.Unauthorized())
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireAdmin rejects anyone who is not an instance administrator. Used for
// panel-wide settings, not for team-scoped resources.
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := UserFrom(r.Context())
		if !ok {
			writeError(w, r, errdoc.Unauthorized())
			return
		}
		if !user.IsAdmin {
			writeError(w, r, errdoc.Forbidden("changing panel-wide settings"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// csrf checks that a state-changing request carries this session's own CSRF
// token in a header.
//
// Requests carrying a bearer token are exempt: a CSRF attack cannot set an
// Authorization header, and the CLI has no cookie to submit.
func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if _, ok := apiTokenFrom(r.Context()); ok {
			next.ServeHTTP(w, r)
			return
		}
		session, ok := sessionFrom(r.Context())
		if !ok {
			// No cookie session, so there is nothing for a cross-site request
			// to ride on.
			next.ServeHTTP(w, r)
			return
		}

		// Against the session, not against a second cookie. See HostPrefix in
		// internal/auth: on a panel that hosts applications, "a page on another
		// site" can be an app somebody deployed here, and a page on a sibling
		// subdomain can write cookies this panel would otherwise trust.
		if !s.auth.CheckCSRF(session, r.Header.Get(auth.CSRFHeaderName)) {
			writeError(w, r, errdoc.New("auth.csrf", "This request was blocked for safety").
				WithCause("The request did not carry a matching CSRF token.").
				WithImpact("Nothing was changed.").
				WithFix("Reload the page and try again. If you are calling the API directly, use an API token with an Authorization header instead of a cookie.").
				WithStatus(http.StatusForbidden))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireRecentAuth refuses an action unless the caller has proved who they are
// within the last few minutes, rather than merely holding a session cookie.
//
// It guards the three actions that turn a borrowed session into permanent
// access: turning two-factor off, reading the recovery codes, and minting an
// API token that outlives the session it was made from. Without it, a laptop
// left unlocked for a minute is an account somebody else keeps.
//
// A bearer token is refused outright rather than waved through. A token cannot
// re-authenticate — there is nobody at the keyboard — and letting one disable
// two-factor would make the token a way around the very thing it protects.
func (s *Server) requireRecentAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := apiTokenFrom(r.Context()); ok {
			writeError(w, r, errdoc.New("auth.needs_person", "This has to be done from the panel").
				WithCause("Changing two-factor authentication or creating a token has to be done by somebody who can prove who they are, and an API token cannot.").
				WithImpact("Nothing was changed.").
				WithFix("Sign in to the panel and do it there.").
				WithStatus(http.StatusForbidden))
			return
		}
		session, ok := sessionFrom(r.Context())
		if !ok {
			writeError(w, r, errdoc.Unauthorized())
			return
		}
		if !s.auth.RecentlyAuthenticated(session) {
			// The frontend routes on this code: it opens the dialog that asks
			// for the password, then repeats the request.
			writeError(w, r, errdoc.New("auth.reauth_required", "Confirm it is you").
				WithCause("This action needs your password again, because holding a signed-in session is not the same as being at the keyboard.").
				WithImpact("Nothing was changed.").
				WithFix("Enter your password to continue.").
				WithStatus(http.StatusForbidden))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// noCache stops a proxy or browser caching an API response.
func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, max-age=0")
		next.ServeHTTP(w, r)
	})
}
