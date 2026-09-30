package secretmgr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// dopplerAPI is where Doppler answers. There is no self-hosted Doppler; a test
// points this at a fake of its own.
var dopplerAPI = "https://api.doppler.com"

// doppler reads Doppler with a service token.
//
// A service token belongs to one config of one project, so a secret is named
// by its name alone: GET /v3/configs/config/secret?name=NAME answers
// {"name": ..., "value": {"raw": ..., "computed": ...}}. GET /v3/me describes
// the token and reads no secret, which is the test.
type doppler struct {
	caller
	base  string
	token string
}

func newDoppler(c caller, credentials map[string]string) *doppler {
	return &doppler{caller: c, base: strings.TrimSuffix(dopplerAPI, "/"), token: credentials["token"]}
}

func (d *doppler) headers() map[string]string {
	return map[string]string{"Authorization": "Bearer " + d.token}
}

// message is Doppler's own explanation: {"messages": ["..."], "success": false}.
func (d *doppler) message(a answer) string {
	var body struct {
		Messages []string `json:"messages"`
	}
	if json.Unmarshal(a.body, &body) != nil {
		return ""
	}
	return strings.Join(body.Messages, "; ")
}

func (d *doppler) test(ctx context.Context) error {
	a, err := d.do(ctx, http.MethodGet, d.base+"/v3/me", d.headers(), nil)
	if err != nil {
		return err
	}
	if a.status != http.StatusOK {
		return d.status(a, "the service token", d.message(a))
	}
	var me map[string]any
	return d.decode(a, &me)
}

func (d *doppler) fetch(ctx context.Context, name string) (secret, error) {
	a, err := d.do(ctx, http.MethodGet, d.base+"/v3/configs/config/secret?"+url.Values{"name": {name}}.Encode(),
		d.headers(), nil)
	if err != nil {
		return secret{}, err
	}
	if a.status != http.StatusOK {
		return secret{}, d.status(a, name, d.message(a))
	}
	var read struct {
		Name  string `json:"name"`
		Value *struct {
			Raw      *string `json:"raw"`
			Computed *string `json:"computed"`
		} `json:"value"`
	}
	if err := d.decode(a, &read); err != nil {
		return secret{}, err
	}
	if read.Value == nil {
		return secret{}, failure(BadAnswer, "Doppler answered %s without its value", name)
	}
	// The computed value, with ${OTHER} references filled in, is what
	// Doppler's own CLI hands an app. A restricted secret comes back with
	// neither.
	switch {
	case read.Value.Computed != nil:
		return secret{value: *read.Value.Computed}, nil
	case read.Value.Raw != nil:
		return secret{value: *read.Value.Raw}, nil
	}
	return secret{}, failure(Denied, "Doppler hides the value of %s from this service token", name)
}
