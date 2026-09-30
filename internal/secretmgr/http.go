package secretmgr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"skifity/internal/netguard"
)

// caller makes the requests to one manager.
type caller struct {
	client *http.Client
	// label names the manager in a sentence: "Vault", "Doppler".
	label string
}

// guarded is the client every request goes through: netguard's, which will
// not dial the metadata service or the panel itself, with a timeout.
func guarded() *http.Client { return netguard.Client(requestTimeout) }

// sameHost answers a redirect to another host with an error rather than
// following it.
//
// Go's client drops the Authorization header on a redirect to another host,
// and keeps every other one: Vault's X-Vault-Token would go to wherever the
// redirect pointed. A manager behind a load balancer does not redirect; one
// that does is sending the panel somewhere it was not configured to go.
func sameHost(client *http.Client) *http.Client {
	copied := *client
	copied.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if req.URL.Host != via[0].URL.Host || req.URL.Scheme != via[0].URL.Scheme {
			return fmt.Errorf("it redirected to %s, which is not the address it was given", req.URL.Host)
		}
		return nil
	}
	return &copied
}

// answer is what came back.
type answer struct {
	status int
	body   []byte
}

// do sends a request and reads at most maxAnswer of what comes back.
//
// A failure is always an *Error whose sentence names no value: the address
// and the path are in it, a header or a body never is.
func (c caller) do(ctx context.Context, method, address string, headers map[string]string, body []byte) (answer, error) {
	return c.send(ctx, method, address, headers, body, nil)
}

// send is do with a last word on the request before it goes: AWS signs it.
func (c caller) send(ctx context.Context, method, address string, headers map[string]string, body []byte,
	prepare func(*http.Request)) (answer, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, address, reader)
	if err != nil {
		return answer{}, failure(BadAnswer, "%s's address could not be used", c.label)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "skifity")
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if prepare != nil {
		prepare(req)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return answer{}, c.unreachable(req.URL, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer+1))
	if err != nil {
		return answer{}, c.unreachable(req.URL, err)
	}
	if len(data) > maxAnswer {
		return answer{}, failure(BadAnswer, "%s answered with more than %d KiB, which is not a secret", c.label, maxAnswer/1024)
	}
	return answer{status: resp.StatusCode, body: data}, nil
}

// unreachable says why a request got no answer, in words that name the host
// and nothing else from the request.
func (c caller) unreachable(address *url.URL, err error) *Error {
	host := address.Host
	var blocked *netguard.Blocked
	var netErr net.Error
	switch {
	case errors.As(err, &blocked):
		return failure(Unreachable, "the panel will not connect to %s: %s", host, blocked.Error())
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded),
		errors.As(err, &netErr) && netErr.Timeout():
		return failure(Unreachable, "%s at %s did not answer in time", c.label, host)
	case errors.Is(err, context.Canceled):
		return failure(Unreachable, "the request to %s at %s was cancelled", c.label, host)
	}
	// The redirect refusal above, a refused connection, a name that does not
	// resolve, a certificate that does not verify. The url.Error around it
	// repeats the address with its query, so only the cause is kept.
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	return failure(Unreachable, "%s at %s could not be reached: %s", c.label, host, quote(err.Error()))
}

// decode reads an answer as the JSON the manager sends. The decoder's own
// message is dropped: it can quote a piece of the body, and the body can
// hold a secret.
func (c caller) decode(a answer, into any) error {
	if err := json.Unmarshal(a.body, into); err != nil {
		return failure(BadAnswer, "%s answered %d with something that is not the JSON it sends", c.label, a.status)
	}
	return nil
}

// status turns an answer that is not a success into an *Error, with the
// manager's own message when it gave one.
func (c caller) status(a answer, what string, message string) *Error {
	suffix := ""
	if message = quote(message); message != "" {
		suffix = ": " + message
	}
	switch {
	case a.status == http.StatusUnauthorized || a.status == http.StatusForbidden:
		return failure(Denied, "%s refused %s (%d)%s", c.label, what, a.status, suffix)
	case a.status == http.StatusNotFound:
		return failure(NotFound, "%s has nothing at %s (%d)%s", c.label, what, a.status, suffix)
	case a.status == http.StatusTooManyRequests:
		return failure(Unreachable, "%s is limiting how often it is asked (%d)%s", c.label, a.status, suffix)
	case a.status >= 500:
		return failure(Unreachable, "%s failed while answering %s (%d)%s", c.label, what, a.status, suffix)
	}
	return failure(BadAnswer, "%s answered %d to %s%s", c.label, a.status, what, suffix)
}

// joinPath escapes each segment of a path for a URL.
func joinPath(path string) string {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}
