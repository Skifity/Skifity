package remote

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"skifity/internal/netguard"
	"skifity/internal/version"
)

// Downloading a catalogue.
//
// The address is something a team administrator typed, and the request is the
// panel's own — the process that holds the master key and can reach the
// Kubernetes API. So it goes through internal/netguard like every other
// address a person gives the panel: the cloud metadata service, loopback and
// the rest are refused on the address actually dialled, not on the name.
//
// Three things on top of that, because this download is larger than a webhook
// and repeats every day without anybody watching it:
//
//   - A size limit, read rather than believed: a Content-Length over it is
//     refused before the body is read, and a body with none is cut off one byte
//     past the limit.
//   - A time limit on the whole exchange, so a server that trickles a byte a
//     second holds nothing for long.
//   - Redirects that stay honest. Each must be https; no more than five; the
//     team's header is not sent on to a different host; and a redirect may not
//     lead to a private address the first request did not already reach. A
//     self-hosted Gitea on 10.0.0.5 is allowed, as netguard allows it
//     everywhere — typed in as the address, where the administrator can see
//     it. A public host that answers "go to 10.0.0.5 instead" is not the same
//     thing, and is refused.

// FetchTimeout bounds one download, start to finish.
const FetchTimeout = 30 * time.Second

// maxRedirects is how many redirects a download follows.
const maxRedirects = 5

// Fetcher downloads catalogues and their logos. The zero value is what the
// panel uses.
type Fetcher struct {
	// Allowed decides which addresses may be dialled at all. Nil is
	// netguard.Allowed; a test sets it to reach its own server on loopback.
	Allowed func(net.IP) bool
	// Private decides which addresses a redirect may not lead to unless the
	// first request already reached them. Nil is the private ranges.
	Private func(net.IP) bool
	// RootCAs are the certificates a server is trusted by. Nil is the
	// system's; a test gives its own server's.
	RootCAs *x509.CertPool
	// Timeout bounds one download. Zero is FetchTimeout.
	Timeout time.Duration
}

// Header is the one header a catalogue's address is asked with: the token a
// private Git host wants for its raw files.
type Header struct {
	Name  string
	Value string
}

// TooLargeError is a download over its limit.
type TooLargeError struct{ Limit int64 }

func (e *TooLargeError) Error() string {
	return fmt.Sprintf("the answer is larger than %s", sizeOf(e.Limit))
}

// StatusError is an answer other than 200.
type StatusError struct {
	Code   int
	Status string
}

func (e *StatusError) Error() string { return "the server answered " + e.Status }

// RedirectError is a redirect that was not followed, and why.
type RedirectError struct{ Reason string }

func (e *RedirectError) Error() string { return "refusing to follow a redirect: " + e.Reason }

// Fetch downloads an address, with the team's header, up to limit bytes.
func (f *Fetcher) Fetch(ctx context.Context, address string, header Header, limit int64) ([]byte, error) {
	if !netguard.Address.MatchString(address) {
		return nil, fmt.Errorf("%s is not an https address", redact(address))
	}
	target, err := url.Parse(address)
	if err != nil || target.Scheme != "https" || target.Host == "" {
		return nil, fmt.Errorf("%s is not an https address", redact(address))
	}
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = FetchTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client := f.client(target, header)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build the request for %s: %w", redact(address), err)
	}
	req.Header.Set("User-Agent", version.UserAgent())
	if header.Name != "" {
		req.Header.Set(header.Name, header.Value)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, unwrap(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &StatusError{Code: resp.StatusCode, Status: resp.Status}
	}
	if resp.ContentLength > limit {
		return nil, &TooLargeError{Limit: limit}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, unwrap(err)
	}
	if int64(len(body)) > limit {
		return nil, &TooLargeError{Limit: limit}
	}
	return body, nil
}

// client builds the client for one download. Its dialler remembers which
// private addresses the first request reached, which is what a redirect is
// held to, so it is not shared.
func (f *Fetcher) client(target *url.URL, header Header) *http.Client {
	allowed := f.Allowed
	if allowed == nil {
		allowed = netguard.Allowed
	}
	private := f.Private
	if private == nil {
		private = IsPrivate
	}
	var hops struct {
		sync.Mutex
		redirected bool
		first      map[string]bool
	}
	hops.first = map[string]bool{}

	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		// The check runs on the address being dialled, after the name was
		// resolved: see internal/netguard for why that is the only place
		// it holds.
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("refusing to connect to %q", address)
			}
			ip := net.ParseIP(host)
			if !allowed(ip) {
				return &netguard.Blocked{IP: ip}
			}
			if !private(ip) {
				return nil
			}
			hops.Lock()
			defer hops.Unlock()
			if !hops.redirected {
				hops.first[ip.String()] = true
				return nil
			}
			if !hops.first[ip.String()] {
				return &RedirectError{Reason: fmt.Sprintf(
					"it led to %s, a private address the catalogue's own address did not", ip)}
			}
			return nil
		},
	}
	return &http.Client{
		Transport: &http.Transport{
			// Never a proxy from the environment: the address checked above
			// would be the proxy's, and the proxy would go wherever it was
			// told.
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			TLSClientConfig:       &tls.Config{RootCAs: f.RootCAs, MinVersion: tls.VersionTLS12},
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 20 * time.Second,
			MaxIdleConns:          2,
			IdleConnTimeout:       10 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return &RedirectError{Reason: fmt.Sprintf("there were more than %d", maxRedirects)}
			}
			if req.URL.Scheme != "https" {
				return &RedirectError{Reason: fmt.Sprintf("it led to %s, which is not https", redact(req.URL.String()))}
			}
			// net/http drops Authorization on the way to another domain and
			// keeps every other header — PRIVATE-TOKEN, X-Api-Key — so the
			// team's token would go wherever the redirect said.
			if header.Name != "" && !SameHost(req.URL, target) {
				req.Header.Del(header.Name)
			}
			hops.Lock()
			hops.redirected = true
			hops.Unlock()
			return nil
		},
	}
}

// IsPrivate reports whether an address is on a private network: RFC 1918,
// unique-local IPv6, and the carrier-grade NAT range a tailnet lives on.
func IsPrivate(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return ip.IsPrivate() || carrierNAT.Contains(ip)
}

var carrierNAT = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// unwrap takes the address out of net/http's error, which quotes the whole
// URL: a redirect's address can carry a signature or a token in its query, and
// this sentence is stored and shown.
func unwrap(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("the download did not finish within %s: %w", FetchTimeout, err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("the download did not finish in time: %w", err)
	}
	return err
}

// redact is an address without its query, its fragment or anybody's
// credentials, for a sentence somebody will read.
func redact(address string) string {
	parsed, err := url.Parse(address)
	if err != nil {
		return "the address"
	}
	parsed.User, parsed.RawQuery, parsed.Fragment = nil, "", ""
	return parsed.String()
}

func sizeOf(n int64) string {
	switch {
	case n >= 1<<20 && n%(1<<20) == 0:
		return fmt.Sprintf("%d MB", n>>20)
	case n >= 1<<10 && n%(1<<10) == 0:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}

// credentialParameter is a query parameter that is somebody's secret:
// ?private_token=, ?access_token=, an S3 signature.
var credentialParameter = regexp.MustCompile(
	`(?i)(token|secret|passw|pwd|auth|credential|signature|^sig$|(^|[-_])(api)?key$)`)

// CheckURL says whether an address can be a catalogue's, and why not.
//
// https, with a host. No username or password in it and no query parameter
// that is one, because the address is shown to every member of the team and a
// token belongs in the header, where it is sealed and never shown again.
func CheckURL(address string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(address))
	switch {
	case err != nil || parsed.Host == "":
		return nil, errors.New("that is not an address: write it in full, starting with https://")
	case parsed.Scheme != "https":
		return nil, fmt.Errorf("a catalogue is fetched over https, and this is %s://", parsed.Scheme)
	case parsed.User != nil:
		return nil, errors.New("the address carries a username or a password; put the credential in the header instead, where it is sealed")
	}
	for name := range parsed.Query() {
		if credentialParameter.MatchString(name) {
			return nil, fmt.Errorf("the address carries %q, which looks like a credential; "+
				"put it in the header instead, where it is sealed and never shown again", name)
		}
	}
	parsed.Fragment = ""
	return parsed, nil
}

// forbiddenHeaders are headers that are the request's framing or its
// route, not a credential: set by hand they change what is asked or where.
var forbiddenHeaders = map[string]bool{
	"host": true, "content-length": true, "transfer-encoding": true, "connection": true,
	"upgrade": true, "te": true, "trailer": true, "keep-alive": true, "expect": true,
	"proxy-authorization": true, "proxy-connection": true,
}

// CheckHeader says whether a header can be sent with a catalogue's download.
func CheckHeader(name, value string) error {
	switch {
	case name == "" && value == "":
		return nil
	case name == "":
		return errors.New("a header's value was given without its name, such as Authorization or PRIVATE-TOKEN")
	case value == "":
		return fmt.Errorf("the header %s was named without a value", name)
	case len(name) > 100 || !headerName.MatchString(name):
		return fmt.Errorf("%q is not a header's name", name)
	case forbiddenHeaders[strings.ToLower(name)]:
		return fmt.Errorf("%s is part of how a request is sent, not a credential, and cannot be set", name)
	case len(value) > 4096 || !validHeaderValue(value):
		return fmt.Errorf("the value of %s is not one a header can carry", name)
	}
	return nil
}

// headerName is a header's name as RFC 9110 spells a token.
var headerName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

// validHeaderValue refuses what would end the header early or smuggle
// another in after it: a control character other than a tab.
func validHeaderValue(value string) bool {
	for _, r := range value {
		if (r < 0x20 && r != '\t') || r == 0x7f {
			return false
		}
	}
	return true
}
