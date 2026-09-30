package api

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

func contextWithTimeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}

// queryInt reads an integer query parameter with a default.
func queryInt(r *http.Request, name string, fallback int) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}

// queryBool reads a boolean query parameter.
func queryBool(r *http.Request, name string) bool {
	switch r.URL.Query().Get(name) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// contextType names context.Context for the optional-interface assertions in
// upgrade.go, where the inline interface literal would otherwise need the
// import spelled out in an awkward place.
type contextType = context.Context
