package secretmgr

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// vault reads a KV version 2 mount of HashiCorp Vault or OpenBao.
//
// The API is the same for both: GET /v1/<mount>/data/<path> answers
// {"data": {"data": {...}, "metadata": {...}}}, a token goes in X-Vault-Token,
// and an Enterprise or OpenBao namespace in X-Vault-Namespace. An AppRole
// signs in at /v1/auth/<mount>/login and is given a token for the rest.
type vault struct {
	caller
	address      string
	mount        string
	namespace    string
	auth         string
	approleMount string
	token        string
	roleID       string
	secretID     string
	// issued is the token an AppRole login was given, kept for the rest of
	// this provider's short life: one resolution, one login.
	issued string
}

func newVault(c caller, settings, credentials map[string]string) *vault {
	return &vault{
		caller:       c,
		address:      strings.TrimSuffix(settings["address"], "/"),
		mount:        strings.Trim(settings["mount"], "/"),
		namespace:    settings["namespace"],
		auth:         settings["auth"],
		approleMount: strings.Trim(settings["approle_mount"], "/"),
		token:        credentials["token"],
		roleID:       credentials["role_id"],
		secretID:     credentials["secret_id"],
	}
}

// vaultErrors is how Vault says what went wrong: {"errors": ["..."]}.
type vaultErrors struct {
	Errors []string `json:"errors"`
}

func (v *vault) message(a answer) string {
	var body vaultErrors
	if json.Unmarshal(a.body, &body) != nil {
		return ""
	}
	return strings.Join(body.Errors, "; ")
}

func (v *vault) request(ctx context.Context, method, path, token string, body []byte) (answer, error) {
	headers := map[string]string{"X-Vault-Request": "true"}
	if token != "" {
		headers["X-Vault-Token"] = token
	}
	if v.namespace != "" {
		headers["X-Vault-Namespace"] = v.namespace
	}
	return v.do(ctx, method, v.address+"/v1/"+path, headers, body)
}

// clientToken is the token the rest of the requests carry: the one
// configured, or one an AppRole login is given.
func (v *vault) clientToken(ctx context.Context) (string, error) {
	if v.auth != "approle" {
		return v.token, nil
	}
	if v.issued != "" {
		return v.issued, nil
	}
	body, _ := json.Marshal(map[string]string{"role_id": v.roleID, "secret_id": v.secretID})
	a, err := v.request(ctx, http.MethodPost, "auth/"+joinPath(v.approleMount)+"/login", "", body)
	if err != nil {
		return "", err
	}
	if a.status != http.StatusOK {
		// Vault answers a wrong role or secret id with 400, not 403.
		if a.status == http.StatusBadRequest {
			a.status = http.StatusForbidden
		}
		return "", v.status(a, "the AppRole login", v.message(a))
	}
	var login struct {
		Auth *struct {
			ClientToken string `json:"client_token"`
		} `json:"auth"`
	}
	if err := v.decode(a, &login); err != nil {
		return "", err
	}
	if login.Auth == nil || login.Auth.ClientToken == "" {
		return "", failure(BadAnswer, "Vault answered the AppRole login without a token")
	}
	v.issued = login.Auth.ClientToken
	return v.issued, nil
}

// test asks Vault about the token itself, which every token's default policy
// allows and which reads no secret.
func (v *vault) test(ctx context.Context) error {
	token, err := v.clientToken(ctx)
	if err != nil {
		return err
	}
	a, err := v.request(ctx, http.MethodGet, "auth/token/lookup-self", token, nil)
	if err != nil {
		return err
	}
	if a.status != http.StatusOK {
		return v.status(a, "the token", v.message(a))
	}
	var lookup struct {
		Data map[string]any `json:"data"`
	}
	if err := v.decode(a, &lookup); err != nil {
		return err
	}
	if lookup.Data == nil {
		return failure(BadAnswer, "Vault answered the token lookup without the token's details")
	}
	return nil
}

func (v *vault) fetch(ctx context.Context, path string) (secret, error) {
	token, err := v.clientToken(ctx)
	if err != nil {
		return secret{}, err
	}
	where := v.mount + "/" + strings.Trim(path, "/")
	a, err := v.request(ctx, http.MethodGet, joinPath(v.mount)+"/data/"+joinPath(path), token, nil)
	if err != nil {
		return secret{}, err
	}
	if a.status != http.StatusOK {
		return secret{}, v.status(a, where, v.message(a))
	}
	var read struct {
		Data *struct {
			Data     map[string]any `json:"data"`
			Metadata struct {
				Destroyed    bool   `json:"destroyed"`
				DeletionTime string `json:"deletion_time"`
			} `json:"metadata"`
		} `json:"data"`
	}
	if err := v.decode(a, &read); err != nil {
		return secret{}, err
	}
	if read.Data == nil {
		return secret{}, failure(BadAnswer, "Vault answered %s without the secret; is %s a KV version 2 mount?", where, v.mount)
	}
	if read.Data.Data == nil {
		// The latest version was deleted or destroyed: the metadata is there,
		// the values are not.
		return secret{}, failure(NotFound, "the latest version of %s in Vault was deleted", where)
	}
	fields := make(map[string]string, len(read.Data.Data))
	for name, value := range read.Data.Data {
		fields[name] = stringOf(value)
	}
	return secret{fields: fields}, nil
}
