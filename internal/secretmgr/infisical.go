package secretmgr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// infisical reads Infisical, the cloud one or a self-hosted one, as a machine
// identity with universal auth.
//
// It signs in at POST /api/v1/auth/universal-auth/login with the identity's
// client id and secret, and is given a short-lived access token. A secret is
// GET /api/v4/secrets/<name>?projectId=&environment=&secretPath=, and answers
// {"secret": {"secretKey": ..., "secretValue": ...}}.
type infisical struct {
	caller
	site         string
	projectID    string
	environment  string
	clientID     string
	clientSecret string
	// accessToken is what the login gave, kept for the rest of this
	// provider's short life: one resolution, one login.
	accessToken string
}

func newInfisical(c caller, settings, credentials map[string]string) *infisical {
	return &infisical{
		caller:       c,
		site:         strings.TrimSuffix(settings["site_url"], "/"),
		projectID:    settings["project_id"],
		environment:  settings["environment"],
		clientID:     credentials["client_id"],
		clientSecret: credentials["client_secret"],
	}
}

// infisicalError is how Infisical says what went wrong.
type infisicalError struct {
	Message any    `json:"message"`
	Error   string `json:"error"`
}

func (i *infisical) message(a answer) string {
	var body infisicalError
	if json.Unmarshal(a.body, &body) != nil {
		return ""
	}
	if text, ok := body.Message.(string); ok && text != "" {
		return text
	}
	return body.Error
}

// login is the access token the rest of the requests carry. Signing in is
// also the test: it is the one request that proves the credentials and reads
// nothing.
func (i *infisical) login(ctx context.Context) (string, error) {
	body, _ := json.Marshal(map[string]string{"clientId": i.clientID, "clientSecret": i.clientSecret})
	a, err := i.do(ctx, http.MethodPost, i.site+"/api/v1/auth/universal-auth/login", nil, body)
	if err != nil {
		return "", err
	}
	if a.status != http.StatusOK {
		// A client id Infisical does not know is a 404 and a wrong secret a
		// 401; to whoever typed them both are wrong credentials.
		if a.status == http.StatusNotFound || a.status == http.StatusBadRequest {
			a.status = http.StatusUnauthorized
		}
		return "", i.status(a, "the machine identity's login", i.message(a))
	}
	var login struct {
		AccessToken string `json:"accessToken"`
	}
	if err := i.decode(a, &login); err != nil {
		return "", err
	}
	if login.AccessToken == "" {
		return "", failure(BadAnswer, "Infisical answered the login without an access token")
	}
	return login.AccessToken, nil
}

func (i *infisical) test(ctx context.Context) error {
	_, err := i.login(ctx)
	return err
}

// token is the access token, signing in the first time it is needed.
func (i *infisical) token(ctx context.Context) (string, error) {
	if i.accessToken == "" {
		token, err := i.login(ctx)
		if err != nil {
			return "", err
		}
		i.accessToken = token
	}
	return i.accessToken, nil
}

// split turns a path into the folder Infisical calls secretPath and the
// secret's name: /backend/STRIPE_KEY is STRIPE_KEY in /backend.
func splitInfisicalPath(path string) (folder, name string) {
	path = "/" + strings.Trim(path, "/")
	cut := strings.LastIndex(path, "/")
	folder, name = path[:cut], path[cut+1:]
	if folder == "" {
		folder = "/"
	}
	return folder, name
}

func (i *infisical) fetch(ctx context.Context, path string) (secret, error) {
	token, err := i.token(ctx)
	if err != nil {
		return secret{}, err
	}
	folder, name := splitInfisicalPath(path)
	query := url.Values{
		"projectId":              {i.projectID},
		"environment":            {i.environment},
		"secretPath":             {folder},
		"viewSecretValue":        {"true"},
		"expandSecretReferences": {"true"},
		"includeImports":         {"true"},
	}
	where := i.environment + ":" + strings.TrimSuffix(folder, "/") + "/" + name
	a, err := i.do(ctx, http.MethodGet, i.site+"/api/v4/secrets/"+url.PathEscape(name)+"?"+query.Encode(),
		map[string]string{"Authorization": "Bearer " + token}, nil)
	if err != nil {
		return secret{}, err
	}
	if a.status != http.StatusOK {
		return secret{}, i.status(a, where, i.message(a))
	}
	var read struct {
		Secret *struct {
			SecretKey         string  `json:"secretKey"`
			SecretValue       *string `json:"secretValue"`
			SecretValueHidden bool    `json:"secretValueHidden"`
		} `json:"secret"`
	}
	if err := i.decode(a, &read); err != nil {
		return secret{}, err
	}
	if read.Secret == nil {
		return secret{}, failure(BadAnswer, "Infisical answered %s without the secret", where)
	}
	if read.Secret.SecretValueHidden || read.Secret.SecretValue == nil {
		return secret{}, failure(Denied, "Infisical hides the value of %s from this machine identity", where)
	}
	return secret{value: *read.Secret.SecretValue}, nil
}
