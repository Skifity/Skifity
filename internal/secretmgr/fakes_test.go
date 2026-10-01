package secretmgr

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"skifity/internal/sigv4"
)

// Fakes of each manager's API, answering what its public documentation says
// it answers, for the requests the panel makes. Each keeps count of what it
// was asked, so a test can say "once".
//
// Every credential here is obviously fake.

const (
	fakeVaultToken    = "hvs.fake-vault-token-for-tests-only"
	fakeRoleID        = "fake-role-id"
	fakeSecretID      = "fake-secret-id"
	fakeInfisicalID   = "fake-client-id"
	fakeInfisicalKey  = "fake-client-secret"
	fakeDopplerToken  = "dp.st.fake-doppler-token-for-tests-only"
	fakeAWSAccessKey  = "AKIAFAKEFAKEFAKEFAKE"
	fakeAWSSecretKey  = "fake/secret/access/key/for/tests/only000"
	fakeProjectID     = "prj-fake"
	fakeEnvironment   = "prod"
	stripeKey         = "sk_test_fake_stripe_key"
	oversizedAnswer   = maxAnswer + 10
	slowAnswerTimeout = 300 * time.Millisecond
)

// testClient reaches the loopback address the fakes listen on, which the
// panel's own client refuses; that refusal is tested on its own.
func testClient() *http.Client { return &http.Client{Timeout: slowAnswerTimeout} }

func newServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server.URL
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// slowOrHuge answers the two paths every fake has for the limits: one that
// never answers in time, and one that answers too much.
func slowOrHuge(w http.ResponseWriter, r *http.Request, name string) bool {
	switch {
	case strings.Contains(name, "slow"):
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
		return true
	case strings.Contains(name, "huge"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, strings.Repeat(" ", oversizedAnswer))
		return true
	}
	return false
}

// --- Vault ---

type fakeVault struct {
	server    *httptest.Server
	reads     atomic.Int32
	namespace string
}

func newFakeVault(t *testing.T) *fakeVault {
	t.Helper()
	f := &fakeVault{}
	mux := http.NewServeMux()
	approleToken := "hvs.fake-approle-issued-token"
	authorised := func(r *http.Request) bool {
		token := r.Header.Get("X-Vault-Token")
		return (token == fakeVaultToken || token == approleToken) && r.Header.Get("X-Vault-Namespace") == f.namespace
	}
	mux.HandleFunc("POST /v1/auth/approle/login", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["role_id"] != fakeRoleID || body["secret_id"] != fakeSecretID {
			writeJSON(w, http.StatusBadRequest, map[string]any{"errors": []string{"invalid role or secret ID"}})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"auth": map[string]any{
			"client_token": approleToken, "accessor": "fake", "policies": []string{"default", "shop"},
			"lease_duration": 3600, "renewable": true,
		}})
	})
	mux.HandleFunc("GET /v1/auth/token/lookup-self", func(w http.ResponseWriter, r *http.Request) {
		if !authorised(r) {
			writeJSON(w, http.StatusForbidden, map[string]any{"errors": []string{"permission denied"}})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
			"accessor": "fake", "policies": []string{"default"}, "ttl": 3600, "display_name": "token",
		}})
	})
	mux.HandleFunc("GET /v1/secret/data/{path...}", func(w http.ResponseWriter, r *http.Request) {
		f.reads.Add(1)
		path := r.PathValue("path")
		if slowOrHuge(w, r, path) {
			return
		}
		if !authorised(r) {
			writeJSON(w, http.StatusForbidden, map[string]any{"errors": []string{"permission denied"}})
			return
		}
		switch path {
		case "shop":
			writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
				"data": map[string]any{"stripe_key": stripeKey, "port": 8080, "empty": ""},
				"metadata": map[string]any{"created_time": "2026-09-30T02:24:06Z", "deletion_time": "",
					"destroyed": false, "version": 3},
			}})
		case "single":
			writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
				"data": map[string]any{"value": "only-one"}, "metadata": map[string]any{"version": 1},
			}})
		case "team/billing":
			writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
				"data": map[string]any{"invoice_key": "fake-invoice"}, "metadata": map[string]any{"version": 1},
			}})
		case "deleted":
			writeJSON(w, http.StatusNotFound, map[string]any{"data": map[string]any{
				"data": nil, "metadata": map[string]any{"deletion_time": "2026-09-30T02:24:06Z", "version": 2},
			}})
		case "forbidden":
			writeJSON(w, http.StatusForbidden, map[string]any{"errors": []string{"1 error occurred:\n\t* permission denied\n\n"}})
		default:
			writeJSON(w, http.StatusNotFound, map[string]any{"errors": []string{}})
		}
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeVault) settings() map[string]string {
	return map[string]string{"address": f.server.URL, "mount": "secret", "auth": "token", "namespace": f.namespace}
}

func (f *fakeVault) credentials() map[string]string {
	return map[string]string{"token": fakeVaultToken}
}

// --- Infisical ---

type fakeInfisical struct {
	server *httptest.Server
	logins atomic.Int32
}

func newFakeInfisical(t *testing.T) *fakeInfisical {
	t.Helper()
	f := &fakeInfisical{}
	mux := http.NewServeMux()
	accessToken := "fake.infisical.access-token"
	unauthorised := func(w http.ResponseWriter) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"reqId": "req-fake", "statusCode": 401, "message": "Invalid credentials", "error": "UnauthorizedError",
		})
	}
	mux.HandleFunc("POST /api/v1/auth/universal-auth/login", func(w http.ResponseWriter, r *http.Request) {
		f.logins.Add(1)
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["clientId"] != fakeInfisicalID || body["clientSecret"] != fakeInfisicalKey {
			unauthorised(w)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"accessToken": accessToken, "expiresIn": 7200, "accessTokenMaxTTL": 43200, "tokenType": "Bearer",
		})
	})
	mux.HandleFunc("GET /api/v4/secrets/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if slowOrHuge(w, r, name) {
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+accessToken {
			unauthorised(w)
			return
		}
		q := r.URL.Query()
		if q.Get("projectId") != fakeProjectID || q.Get("environment") != fakeEnvironment {
			writeJSON(w, http.StatusForbidden, map[string]any{
				"reqId": "req-fake", "statusCode": 403, "message": "You are not allowed to read secrets", "error": "PermissionDenied",
			})
			return
		}
		secretValue := map[string]string{
			"/:STRIPE_KEY":     stripeKey,
			"/backend:CONFIG":  `{"stripe_key":"` + stripeKey + `","retries":3}`,
			"/:HIDDEN":         "<hidden-by-infisical>",
			"/backend:MISSING": "",
		}
		value, ok := secretValue[q.Get("secretPath")+":"+name]
		if !ok || value == "" {
			writeJSON(w, http.StatusNotFound, map[string]any{
				"reqId": "req-fake", "statusCode": 404, "message": fmt.Sprintf("Secret with name '%s' not found", name), "error": "NotFound",
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"secret": map[string]any{
			"id": "sec-fake", "_id": "sec-fake", "workspace": fakeProjectID, "environment": fakeEnvironment,
			"version": 4, "type": "shared", "secretKey": name, "secretValue": value, "secretComment": "",
			"secretValueHidden": name == "HIDDEN", "secretPath": q.Get("secretPath"), "tags": []any{},
		}})
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeInfisical) settings() map[string]string {
	return map[string]string{"site_url": f.server.URL, "project_id": fakeProjectID, "environment": fakeEnvironment}
}

func (f *fakeInfisical) credentials() map[string]string {
	return map[string]string{"client_id": fakeInfisicalID, "client_secret": fakeInfisicalKey}
}

// --- Doppler ---

func newFakeDoppler(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	authorised := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer "+fakeDopplerToken {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"messages": []string{"Invalid Auth token"}, "success": false})
			return false
		}
		return true
	}
	mux.HandleFunc("GET /v3/me", func(w http.ResponseWriter, r *http.Request) {
		if !authorised(w, r) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"workplace": map[string]any{"slug": "acme", "name": "Acme"}, "type": "service_token",
			"token_preview": "dp.st…only", "slug": "panel", "name": "panel", "success": true,
		})
	})
	mux.HandleFunc("GET /v3/configs/config/secret", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		if slowOrHuge(w, r, strings.ToLower(name)) {
			return
		}
		if !authorised(w, r) {
			return
		}
		switch name {
		case "STRIPE_KEY":
			writeJSON(w, http.StatusOK, map[string]any{"name": name, "value": map[string]any{
				"raw": "${OTHER}", "computed": stripeKey, "note": "", "rawVisibility": "masked",
			}, "success": true})
		case "RESTRICTED":
			writeJSON(w, http.StatusOK, map[string]any{"name": name, "value": map[string]any{
				"raw": nil, "computed": nil, "rawVisibility": "restricted",
			}, "success": true})
		case "FORBIDDEN":
			writeJSON(w, http.StatusForbidden, map[string]any{"messages": []string{"You do not have access to this config"}, "success": false})
		default:
			writeJSON(w, http.StatusNotFound, map[string]any{"messages": []string{"Could not find requested secret: " + name}, "success": false})
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// useDoppler points the Doppler provider at a fake for one test.
func useDoppler(t *testing.T, server *httptest.Server) {
	t.Helper()
	was := dopplerAPI
	dopplerAPI = server.URL
	t.Cleanup(func() { dopplerAPI = was })
}

// --- AWS ---

// newFakeAWS answers Secrets Manager's GetSecretValue and STS's
// GetCallerIdentity on one address, the way LocalStack does, and checks each
// request's signature by signing it again with the secret it knows.
func newFakeAWS(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !validSignature(r, body) {
			if r.Header.Get("X-Amz-Target") != "" {
				writeJSON(w, http.StatusBadRequest, map[string]any{
					"__type":  "UnrecognizedClientException",
					"message": "The security token included in the request is invalid.",
				})
				return
			}
			w.Header().Set("Content-Type", "text/xml")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `<ErrorResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><Error><Type>Sender</Type>`+
				`<Code>InvalidClientTokenId</Code><Message>The security token included in the request is invalid.</Message>`+
				`</Error><RequestId>fake</RequestId></ErrorResponse>`)
			return
		}
		switch r.Header.Get("X-Amz-Target") {
		case "":
			form, _ := url.ParseQuery(string(body))
			if form.Get("Action") != "GetCallerIdentity" {
				http.Error(w, "unexpected action", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "text/xml")
			_, _ = io.WriteString(w, `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">`+
				`<GetCallerIdentityResult><Arn>arn:aws:iam::123456789012:user/panel</Arn><UserId>AIDAFAKE</UserId>`+
				`<Account>123456789012</Account></GetCallerIdentityResult><ResponseMetadata><RequestId>fake</RequestId>`+
				`</ResponseMetadata></GetCallerIdentityResponse>`)
		case "secretsmanager.GetSecretValue":
			var request struct {
				SecretID string `json:"SecretId"`
			}
			_ = json.Unmarshal(body, &request)
			if slowOrHuge(w, r, request.SecretID) {
				return
			}
			switch request.SecretID {
			case "shop/production", "arn:aws:secretsmanager:eu-central-1:123456789012:secret:shop/production-AbCdEf":
				writeJSON(w, http.StatusOK, map[string]any{
					"ARN": "arn:aws:secretsmanager:eu-central-1:123456789012:secret:shop/production-AbCdEf", "Name": "shop/production",
					"SecretString": `{"stripe_key":"` + stripeKey + `","port":8080}`, "VersionId": "v1",
					"VersionStages": []string{"AWSCURRENT"}, "CreatedDate": 1.7e9,
				})
			case "plain":
				writeJSON(w, http.StatusOK, map[string]any{"Name": "plain", "SecretString": "just-a-string"})
			case "binary":
				writeJSON(w, http.StatusOK, map[string]any{"Name": "binary", "SecretBinary": "AAEC"})
			case "denied":
				writeJSON(w, http.StatusBadRequest, map[string]any{
					"__type":  "AccessDeniedException",
					"Message": "User: arn:aws:iam::123456789012:user/panel is not authorized to perform: secretsmanager:GetSecretValue",
				})
			default:
				writeJSON(w, http.StatusBadRequest, map[string]any{
					"__type":  "ResourceNotFoundException",
					"Message": "Secrets Manager can't find the specified secret.",
				})
			}
		default:
			http.Error(w, "unexpected target", http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// validSignature signs a copy of the request again with the secret the fake
// knows, at the time the request says, and compares.
func validSignature(r *http.Request, body []byte) bool {
	authorization := r.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, sigv4.Algorithm+" Credential="+fakeAWSAccessKey+"/") {
		return false
	}
	at, err := time.Parse("20060102T150405Z", r.Header.Get("X-Amz-Date"))
	if err != nil {
		return false
	}
	service := "sts"
	if r.Header.Get("X-Amz-Target") != "" {
		service = "secretsmanager"
	}
	if !strings.Contains(authorization, "/eu-central-1/"+service+"/aws4_request") {
		return false
	}
	again, _ := http.NewRequest(r.Method, "http://"+r.Host+r.URL.RequestURI(), nil)
	for _, name := range []string{"Content-Type", "X-Amz-Target", "X-Amz-Security-Token"} {
		if value := r.Header.Get(name); value != "" {
			again.Header.Set(name, value)
		}
	}
	sigv4.Sign(again, body, sigv4.Credentials{AccessKeyID: fakeAWSAccessKey, SecretAccessKey: fakeAWSSecretKey,
		SessionToken: r.Header.Get("X-Amz-Security-Token")}, "eu-central-1", service, at)
	return again.Header.Get("Authorization") == authorization
}

func awsSettings(server *httptest.Server) map[string]string {
	return map[string]string{"region": "eu-central-1", "endpoint": server.URL}
}

func awsCredentialsFor(secretKey string) map[string]string {
	return map[string]string{"access_key_id": fakeAWSAccessKey, "secret_access_key": secretKey}
}
