package api

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"skifity/internal/config"
	"skifity/internal/events"
)

// TestEveryRouteLlmsTxtPromisesIsRouted: llms.txt is the page an AI assistant
// is pointed at, and it prints a table of API routes. An assistant reads that
// table and calls what is in it. A route that moved, or was never there, does
// not read as a mistake in a document — it reads as the product being broken,
// and the assistant will keep trying.
//
// So the table is checked against the router itself rather than against a list
// somebody keeps by hand.
func TestEveryRouteLlmsTxtPromisesIsRouted(t *testing.T) {
	promised := routesFromLlmsTxt(t)
	if len(promised) < 20 {
		t.Fatalf("only %d routes were read from llms.txt; this test is not reading the table", len(promised))
	}
	registered := registeredRoutes(t)

	for _, route := range promised {
		if !registered[route] {
			t.Errorf("llms.txt documents %s %s, and nothing answers it", route.method, route.path)
		}
	}
}

type route struct{ method, path string }

// routesFromLlmsTxt reads the API table out of the document.
var routeLine = regexp.MustCompile(`^(GET|POST|PUT|DELETE|PATCH)\s+(/\S+)`)

func routesFromLlmsTxt(t *testing.T) []route {
	t.Helper()

	body, err := os.ReadFile(filepath.Join("..", "..", "llms.txt"))
	if err != nil {
		t.Fatalf("read llms.txt: %v", err)
	}

	var out []route
	for _, line := range strings.Split(string(body), "\n") {
		match := routeLine.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		// A query string is not part of the route.
		path, _, _ := strings.Cut(match[2], "?")
		out = append(out, route{method: match[1], path: normalise(path)})
	}
	return out
}

// registeredRoutes asks the router what it actually serves, with chi's own
// {placeholder} spelling normalised to the one the document uses.
func registeredRoutes(t *testing.T) map[route]bool {
	t.Helper()

	server := New(Options{
		Config: config.Config{},
		Hub:    events.NewHub(4),
		Logger: slog.New(slog.DiscardHandler),
	})

	out := map[route]bool{}
	err := chi.Walk(server.router, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		out[route{method: method, path: normalise(pattern)}] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walk the router: %v", err)
	}
	if len(out) < 40 {
		t.Fatalf("the router reported %d routes, which cannot be right", len(out))
	}
	return out
}

// normalise makes a chi pattern comparable with what llms.txt writes: chi names
// its parameters after the handler's variables ({appID}), the document names
// them after the thing ({app}), and only the shape matters.
var parameter = regexp.MustCompile(`\{[^}]+\}`)

func normalise(pattern string) string {
	pattern = parameter.ReplaceAllString(pattern, "{}")
	pattern = strings.TrimSuffix(pattern, "/")
	if pattern == "" {
		return "/"
	}
	return pattern
}
