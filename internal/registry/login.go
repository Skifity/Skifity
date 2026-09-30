package registry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ErrLoginRefused is a registry that answered and said the credentials are
// wrong. Anything else that goes wrong is a registry that could not be asked.
var ErrLoginRefused = errors.New("the registry refused these credentials")

// CheckLogin asks a registry whether a username and password are good, the
// way docker login does: the /v2/ endpoint says how it wants to be asked, and
// then it is asked.
//
// client is the caller's, and should be the guarded one (internal/netguard):
// the host is something a person typed, and this request is the panel's.
func CheckLogin(ctx context.Context, client *http.Client, host, username, password string) error {
	base := "https://" + apiHost(host)
	challenge, status, err := get(ctx, client, base+"/v2/", "", "")
	if err != nil {
		return err
	}
	if status == http.StatusOK {
		// No login asked for at all: nothing to be wrong about.
		return nil
	}
	if status != http.StatusUnauthorized {
		return fmt.Errorf("the registry answered %d to /v2/, which is not how a registry answers", status)
	}

	scheme, params := parseChallenge(challenge)
	switch scheme {
	case "basic":
		_, status, err := get(ctx, client, base+"/v2/", username, password)
		if err != nil {
			return err
		}
		return loginStatus(status)
	case "bearer":
		realm, err := url.Parse(params["realm"])
		if err != nil || realm.Scheme != "https" {
			return fmt.Errorf("the registry's token service is not an https address")
		}
		query := realm.Query()
		if params["service"] != "" {
			query.Set("service", params["service"])
		}
		query.Set("account", username)
		realm.RawQuery = query.Encode()
		_, status, err := get(ctx, client, realm.String(), username, password)
		if err != nil {
			return err
		}
		return loginStatus(status)
	}
	return fmt.Errorf("the registry asks for %q authentication, which is not one Skifity speaks", scheme)
}

// apiHost is where a registry's API is: Docker Hub's is not the name an image
// reference carries.
func apiHost(host string) string {
	if host == "docker.io" {
		return "registry-1.docker.io"
	}
	return host
}

func loginStatus(status int) error {
	switch status {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrLoginRefused
	}
	return fmt.Errorf("the registry answered %d to the login", status)
}

// get makes one request and returns the challenge header and the status.
func get(ctx context.Context, client *http.Client, address, username, password string) (string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return "", 0, err
	}
	if username != "" || password != "" {
		req.SetBasicAuth(username, password)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return resp.Header.Get("WWW-Authenticate"), resp.StatusCode, nil
}

// parseChallenge reads `Bearer realm="…",service="…"` into its scheme and
// parameters.
func parseChallenge(header string) (string, map[string]string) {
	scheme, rest, _ := strings.Cut(strings.TrimSpace(header), " ")
	params := map[string]string{}
	for rest != "" {
		var pair string
		rest = strings.TrimLeft(rest, " ,")
		key, value, found := strings.Cut(rest, "=")
		if !found {
			break
		}
		value = strings.TrimLeft(value, " ")
		if strings.HasPrefix(value, `"`) {
			end := strings.Index(value[1:], `"`)
			if end < 0 {
				break
			}
			pair, rest = value[1:1+end], value[2+end:]
		} else {
			pair, rest, _ = strings.Cut(value, ",")
		}
		params[strings.ToLower(strings.TrimSpace(key))] = pair
	}
	return strings.ToLower(scheme), params
}
