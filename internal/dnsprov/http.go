package dnsprov

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// jsonAPI is the part of a request every JSON provider shares: the client,
// how the credentials go on, and how its error bodies read.
type jsonAPI struct {
	kind    string
	client  *http.Client
	sign    func(*http.Request)
	message func(body []byte) string
}

// maxAnswer bounds what is read of one answer. The longest is a page of
// zones, far below it.
const maxAnswer = 4 << 20

// do sends one request and reads the answer into out. A status outside 2xx
// is an *APIError carrying the provider's own words.
func (a jsonAPI) do(ctx context.Context, method, url string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	a.sign(req)

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("reach %s: %w", Title(a.kind), err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if err != nil {
		return fmt.Errorf("read %s's answer: %w", Title(a.kind), err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &APIError{Provider: a.kind, Status: resp.StatusCode, Message: clip(a.message(raw))}
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("read %s's answer: %w", Title(a.kind), err)
		}
	}
	return nil
}

// isStatus reports whether err is the provider answering with this status.
func isStatus(err error, status int) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == status
}

// clip keeps a provider's message short enough to show. The providers here
// never put a credential in one; a message is still not a place for a page
// of HTML from a proxy in the way.
func clip(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	if len(message) > 300 {
		return message[:297] + "..."
	}
	return message
}

// maxPages bounds a listing, so a provider that always says there is a next
// page is a finished listing rather than a loop.
const maxPages = 100
