package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"skifity/internal/cli"
	"skifity/internal/errdoc"
	"skifity/internal/mcpserver"
)

// The panel's own MCP endpoint.
//
// `skifity mcp` runs on the person's computer and talks to the panel over the
// API. That needs the binary installed wherever the assistant runs, which a
// hosted assistant, a phone or a teammate's editor does not have. This is the
// same server, served by the panel at /api/mcp over streamable HTTP, and
// authenticated the way every other API client is: an API token in the
// Authorization header.
//
// The tools are not reimplemented here. Each one makes the same API requests
// it makes from the CLI, with the caller's own token, through the panel's own
// router — so a viewer's token gets a viewer's answers, a token limited to one
// project reaches that project, and a read-only token reads. Nothing about
// the MCP endpoint is a second way in.

// mcpPanelURL is the base the in-process client builds its requests on. The
// requests never leave the process, so the host is only a name.
const mcpPanelURL = "http://panel.internal"

func (s *Server) mcpHandler() http.Handler {
	return mcpserver.HTTPHandler(func(r *http.Request) *mcpserver.Server {
		token, ok := apiTokenFrom(r.Context())
		if !ok {
			return nil
		}
		raw := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		return mcpserver.NewRemote(cli.Config{PanelURL: mcpPanelURL, Token: raw, TeamID: token.TeamID},
			inProcess{handler: s.router, from: r})
	})
}

// handleMCP serves the endpoint to API tokens only. A browser session has a
// cookie, and a cookie is what a page on another site can make a browser send;
// an assistant has no cookie to send and no reason to use one.
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	if _, ok := apiTokenFrom(r.Context()); !ok {
		writeError(w, r, errdoc.New("mcp.token_required", "The MCP endpoint takes an API token").
			WithCause("This request was signed in with a browser session rather than an API token.").
			WithImpact("Nothing was done.").
			WithFix("Create a token under Account, then give it to the assistant as an Authorization: Bearer header.").
			WithDocs("/docs/cli#ai-assistants").
			WithStatus(http.StatusUnauthorized))
		return
	}
	s.mcp.ServeHTTP(w, r)
}

// inProcess is an http.RoundTripper that answers from a handler in this
// process instead of the network.
//
// The response is streamed through a pipe rather than collected first, so a
// request that streams — a log, an event stream — behaves as it would over a
// connection, and one that is abandoned stops the handler writing.
type inProcess struct {
	handler http.Handler
	// from is the MCP request the calls are made for. The address and the
	// client it names are what the audit log records for them.
	from *http.Request
}

func (t inProcess) RoundTrip(req *http.Request) (*http.Response, error) {
	// A fresh context that ends when the caller's does, and carries nothing
	// else of it. The caller's context is the MCP request's, which holds that
	// request's route, and its authenticated user: a request made with it
	// would be routed as /api/mcp, and would already be signed in before its
	// own token was read.
	ctx, cancel := context.WithCancel(context.Background())
	stop := context.AfterFunc(req.Context(), cancel)
	inner := req.Clone(ctx)
	inner.RemoteAddr = t.from.RemoteAddr
	inner.RequestURI = req.URL.RequestURI()
	inner.Host = req.URL.Host
	for _, header := range []string{"X-Forwarded-For", "X-Real-Ip", "X-Forwarded-Proto"} {
		if value := t.from.Header.Get(header); value != "" {
			inner.Header.Set(header, value)
		}
	}
	if agent := t.from.UserAgent(); agent != "" {
		inner.Header.Set("User-Agent", agent+" (MCP)")
	}

	reader, writer := io.Pipe()
	w := &pipeResponse{header: http.Header{}, body: writer, ready: make(chan struct{})}
	go func() {
		defer cancel()
		defer stop()
		defer func() {
			if p := recover(); p != nil {
				w.start(http.StatusInternalServerError)
				_ = writer.CloseWithError(fmt.Errorf("the panel failed while answering: %v", p))
				return
			}
			w.start(http.StatusOK)
			_ = writer.Close()
		}()
		t.handler.ServeHTTP(w, inner)
	}()

	select {
	case <-w.ready:
	case <-req.Context().Done():
		_ = reader.CloseWithError(req.Context().Err())
		return nil, req.Context().Err()
	}
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", w.status, http.StatusText(w.status)),
		StatusCode:    w.status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        w.sent,
		Body:          reader,
		ContentLength: -1,
		Request:       req,
	}, nil
}

// pipeResponse is the handler's side of inProcess.
type pipeResponse struct {
	header http.Header
	body   *io.PipeWriter

	once   sync.Once
	ready  chan struct{}
	status int
	// sent is the header as it was when the status was written, which is
	// what the other side reads while the handler goes on.
	sent http.Header
}

func (p *pipeResponse) Header() http.Header { return p.header }

func (p *pipeResponse) WriteHeader(status int) { p.start(status) }

func (p *pipeResponse) Write(b []byte) (int, error) {
	p.start(http.StatusOK)
	return p.body.Write(b)
}

// Flush is a no-op: every write already goes straight to the reader.
func (p *pipeResponse) Flush() {}

func (p *pipeResponse) start(status int) {
	p.once.Do(func() {
		p.status = status
		p.sent = p.header.Clone()
		close(p.ready)
	})
}
