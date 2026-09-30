// Package api is the panel's HTTP surface: the JSON API the browser, the CLI and
// the MCP server all use, plus the embedded frontend.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// maxBodyBytes caps request bodies. Nothing the API accepts is large, and an
// unbounded reader is a trivial way to exhaust memory on a 1 GB VPS.
const maxBodyBytes = 1 << 20 // 1 MiB

// writeJSON sends a JSON response.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// Responses are per-user and often contain secrets-adjacent data.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		// The status line is already sent, so there is nothing to tell the
		// client. Log it so a broken serialiser is not invisible.
		slog.Error("write json response", "error", err)
	}
}

// writeError turns any error into a Problem and sends it.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	problem := toProblem(err)
	if problem.Status >= 500 {
		slog.ErrorContext(r.Context(), "request failed",
			"code", problem.Code, "path", r.URL.Path, "method", r.Method, "error", err)
	} else {
		slog.DebugContext(r.Context(), "request rejected",
			"code", problem.Code, "path", r.URL.Path, "method", r.Method)
	}
	body, marshalErr := problem.JSON()
	if marshalErr != nil {
		http.Error(w, `{"error":{"code":"internal","title":"Something went wrong"}}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(problem.Status)
	_, _ = w.Write(body)
}

// toProblem maps the store's sentinel errors onto catalogued problems, so a
// repository can return ErrNotFound and the client still gets an explanation.
func toProblem(err error) *errdoc.Problem {
	var problem *errdoc.Problem
	if errors.As(err, &problem) {
		return problem
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return errdoc.NotFound("resource", "")
	case errors.Is(err, store.ErrConflict):
		return errdoc.Conflict(err.Error(), "Pick a different name and try again.")
	}
	return errdoc.From(err)
}

// decodeJSON reads and validates a JSON request body.
//
// Unknown fields are rejected: a typo in a field name should be an error, not a
// silently ignored setting the user believes they changed.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	contentType := r.Header.Get("Content-Type")
	if contentType != "" && !strings.HasPrefix(contentType, "application/json") {
		return errdoc.BadRequest(fmt.Sprintf("This endpoint expects application/json, not %q.", contentType))
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		var maxBytesErr *http.MaxBytesError
		switch {
		case errors.As(err, &syntaxErr):
			return errdoc.BadRequest(fmt.Sprintf("The request body is not valid JSON (at position %d).", syntaxErr.Offset))
		case errors.As(err, &typeErr):
			return errdoc.BadRequest(fmt.Sprintf("The field %q expects a %s.", typeErr.Field, typeErr.Type))
		case errors.As(err, &maxBytesErr):
			return errdoc.BadRequest("The request body is too large.")
		case errors.Is(err, io.EOF):
			return errdoc.BadRequest("The request body is empty.")
		case strings.HasPrefix(err.Error(), "json: unknown field "):
			field := strings.TrimPrefix(err.Error(), "json: unknown field ")
			return errdoc.BadRequest(fmt.Sprintf("The field %s is not something this endpoint accepts. Check the spelling.", field))
		default:
			return errdoc.BadRequest("The request body could not be read.")
		}
	}
	// A second JSON value in the same body usually means a client bug.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errdoc.BadRequest("The request body must contain exactly one JSON object.")
	}
	return nil
}

// listResponse is the shape every list endpoint returns, so a client never has
// to handle both a bare array and an object.
type listResponse[T any] struct {
	Items []T `json:"items"`
	Total int `json:"total"`
}

func writeList[T any](w http.ResponseWriter, items []T) {
	if items == nil {
		items = []T{}
	}
	writeJSON(w, http.StatusOK, listResponse[T]{Items: items, Total: len(items)})
}

// okResponse is returned by actions that have nothing else to say.
type okResponse struct {
	OK bool `json:"ok"`
}

func writeOK(w http.ResponseWriter) { writeJSON(w, http.StatusOK, okResponse{OK: true}) }
