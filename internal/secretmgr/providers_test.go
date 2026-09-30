package secretmgr

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Each manager against a fake of its API: signing in, reading, and each way
// that goes wrong — refused, not there, too slow, too much.

func fetchOne(t *testing.T, kind string, settings, credentials map[string]string, path, key string) (string, error) {
	t.Helper()
	settings, credentials, err := Normalize(kind, settings, credentials)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	p, err := newProvider(testClient(), kind, settings, credentials)
	if err != nil {
		t.Fatal(err)
	}
	s, err := p.fetch(t.Context(), path)
	if err != nil {
		return "", err
	}
	return pick(s, path, key)
}

func testOne(t *testing.T, kind string, settings, credentials map[string]string) error {
	t.Helper()
	settings, credentials, err := Normalize(kind, settings, credentials)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	p, err := newProvider(testClient(), kind, settings, credentials)
	if err != nil {
		t.Fatal(err)
	}
	return p.test(t.Context())
}

func reasonOf(t *testing.T, err error) Reason {
	t.Helper()
	var failed *Error
	if !errors.As(err, &failed) {
		t.Fatalf("%v is not a secret manager failure", err)
	}
	return failed.Reason
}

func expectReason(t *testing.T, what string, err error, want Reason) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: no error, want %s", what, want)
		return
	}
	if got := reasonOf(t, err); got != want {
		t.Errorf("%s: %s (%v), want %s", what, got, err, want)
	}
	// Whatever went wrong, the sentence never carries what was read.
	if strings.Contains(err.Error(), stripeKey) {
		t.Errorf("%s: the error quotes the secret: %v", what, err)
	}
}

func TestVault(t *testing.T) {
	f := newFakeVault(t)

	if err := testOne(t, KindVault, f.settings(), f.credentials()); err != nil {
		t.Errorf("a good token failed its test: %v", err)
	}
	expectReason(t, "a wrong token", testOne(t, KindVault, f.settings(), map[string]string{"token": "hvs.wrong"}), Denied)

	value, err := fetchOne(t, KindVault, f.settings(), f.credentials(), "shop", "stripe_key")
	if err != nil || value != stripeKey {
		t.Errorf("shop#stripe_key = %q, %v", value, err)
	}
	if value, err := fetchOne(t, KindVault, f.settings(), f.credentials(), "shop", "port"); err != nil || value != "8080" {
		t.Errorf("a number is its JSON: %q, %v", value, err)
	}
	if value, err := fetchOne(t, KindVault, f.settings(), f.credentials(), "single", ""); err != nil || value != "only-one" {
		t.Errorf("a secret with one key needs no key: %q, %v", value, err)
	}
	if value, err := fetchOne(t, KindVault, f.settings(), f.credentials(), "team/billing", "invoice_key"); err != nil || value != "fake-invoice" {
		t.Errorf("a nested path: %q, %v", value, err)
	}

	_, err = fetchOne(t, KindVault, f.settings(), f.credentials(), "shop", "")
	expectReason(t, "several keys and none named", err, NotFound)
	if err != nil && !strings.Contains(err.Error(), "stripe_key") {
		t.Errorf("the error does not say which keys there are: %v", err)
	}
	_, err = fetchOne(t, KindVault, f.settings(), f.credentials(), "shop", "nope")
	expectReason(t, "a key that is not there", err, NotFound)
	_, err = fetchOne(t, KindVault, f.settings(), f.credentials(), "shop", "empty")
	expectReason(t, "an empty value", err, NotFound)
	_, err = fetchOne(t, KindVault, f.settings(), f.credentials(), "missing", "x")
	expectReason(t, "a path with nothing at it", err, NotFound)
	_, err = fetchOne(t, KindVault, f.settings(), f.credentials(), "deleted", "x")
	expectReason(t, "a deleted latest version", err, NotFound)
	_, err = fetchOne(t, KindVault, f.settings(), f.credentials(), "forbidden", "x")
	expectReason(t, "a policy that does not allow it", err, Denied)
	_, err = fetchOne(t, KindVault, f.settings(), map[string]string{"token": "hvs.wrong"}, "shop", "stripe_key")
	expectReason(t, "a read with a wrong token", err, Denied)
	_, err = fetchOne(t, KindVault, f.settings(), f.credentials(), "slow", "x")
	expectReason(t, "a Vault that does not answer", err, Unreachable)
	_, err = fetchOne(t, KindVault, f.settings(), f.credentials(), "huge", "x")
	expectReason(t, "an answer too large to be a secret", err, BadAnswer)
}

func TestVaultNamespaceAndAppRole(t *testing.T) {
	f := newFakeVault(t)
	f.namespace = "team-a"

	settings := f.settings()
	settings["auth"] = "approle"
	approle := map[string]string{"role_id": fakeRoleID, "secret_id": fakeSecretID}
	if err := testOne(t, KindVault, settings, approle); err != nil {
		t.Errorf("an AppRole in a namespace failed its test: %v", err)
	}
	if value, err := fetchOne(t, KindVault, settings, approle, "shop", "stripe_key"); err != nil || value != stripeKey {
		t.Errorf("an AppRole read %q, %v", value, err)
	}
	expectReason(t, "a wrong secret id",
		testOne(t, KindVault, settings, map[string]string{"role_id": fakeRoleID, "secret_id": "wrong"}), Denied)

	// Without the namespace, the fake answers the way Vault does: the token
	// is not known there.
	settings["namespace"] = ""
	expectReason(t, "the wrong namespace", testOne(t, KindVault, settings, approle), Denied)
}

func TestInfisical(t *testing.T) {
	f := newFakeInfisical(t)

	if err := testOne(t, KindInfisical, f.settings(), f.credentials()); err != nil {
		t.Errorf("good credentials failed their test: %v", err)
	}
	expectReason(t, "a wrong client secret", testOne(t, KindInfisical, f.settings(),
		map[string]string{"client_id": fakeInfisicalID, "client_secret": "wrong"}), Denied)

	if value, err := fetchOne(t, KindInfisical, f.settings(), f.credentials(), "STRIPE_KEY", ""); err != nil || value != stripeKey {
		t.Errorf("STRIPE_KEY = %q, %v", value, err)
	}
	if value, err := fetchOne(t, KindInfisical, f.settings(), f.credentials(), "/backend/CONFIG", "stripe_key"); err != nil || value != stripeKey {
		t.Errorf("a key of a JSON secret in a folder = %q, %v", value, err)
	}
	if value, err := fetchOne(t, KindInfisical, f.settings(), f.credentials(), "/backend/CONFIG", "retries"); err != nil || value != "3" {
		t.Errorf("a number in a JSON secret = %q, %v", value, err)
	}

	_, err := fetchOne(t, KindInfisical, f.settings(), f.credentials(), "STRIPE_KEY", "nope")
	expectReason(t, "a key of a secret that is not JSON", err, NotFound)
	_, err = fetchOne(t, KindInfisical, f.settings(), f.credentials(), "/backend/MISSING", "")
	expectReason(t, "a secret that is not there", err, NotFound)
	_, err = fetchOne(t, KindInfisical, f.settings(), f.credentials(), "HIDDEN", "")
	expectReason(t, "a value hidden from the identity", err, Denied)
	other := f.settings()
	other["environment"] = "staging"
	_, err = fetchOne(t, KindInfisical, other, f.credentials(), "STRIPE_KEY", "")
	expectReason(t, "an environment the identity may not read", err, Denied)
	_, err = fetchOne(t, KindInfisical, f.settings(), f.credentials(), "slow", "")
	expectReason(t, "an Infisical that does not answer", err, Unreachable)
	_, err = fetchOne(t, KindInfisical, f.settings(), f.credentials(), "huge", "")
	expectReason(t, "an answer too large", err, BadAnswer)
}

func TestDoppler(t *testing.T) {
	useDoppler(t, newFakeDoppler(t))
	good := map[string]string{"token": fakeDopplerToken}

	if err := testOne(t, KindDoppler, nil, good); err != nil {
		t.Errorf("a good service token failed its test: %v", err)
	}
	expectReason(t, "a wrong token", testOne(t, KindDoppler, nil, map[string]string{"token": "dp.st.wrong"}), Denied)

	// The computed value, with its references filled in, not the raw one.
	if value, err := fetchOne(t, KindDoppler, nil, good, "STRIPE_KEY", ""); err != nil || value != stripeKey {
		t.Errorf("STRIPE_KEY = %q, %v", value, err)
	}
	_, err := fetchOne(t, KindDoppler, nil, good, "MISSING", "")
	expectReason(t, "a secret that is not there", err, NotFound)
	_, err = fetchOne(t, KindDoppler, nil, good, "RESTRICTED", "")
	expectReason(t, "a restricted secret", err, Denied)
	_, err = fetchOne(t, KindDoppler, nil, good, "FORBIDDEN", "")
	expectReason(t, "a config the token may not read", err, Denied)
	_, err = fetchOne(t, KindDoppler, nil, map[string]string{"token": "dp.st.wrong"}, "STRIPE_KEY", "")
	expectReason(t, "a read with a wrong token", err, Denied)
	_, err = fetchOne(t, KindDoppler, nil, good, "SLOW", "")
	expectReason(t, "a Doppler that does not answer", err, Unreachable)
	_, err = fetchOne(t, KindDoppler, nil, good, "HUGE", "")
	expectReason(t, "an answer too large", err, BadAnswer)
}

func TestAWSSecretsManager(t *testing.T) {
	server := newFakeAWS(t)
	good := awsCredentialsFor(fakeAWSSecretKey)

	if err := testOne(t, KindAWS, awsSettings(server), good); err != nil {
		t.Errorf("a good access key failed its test: %v", err)
	}
	expectReason(t, "a wrong secret key", testOne(t, KindAWS, awsSettings(server), awsCredentialsFor("wrong")), Denied)

	if value, err := fetchOne(t, KindAWS, awsSettings(server), good, "shop/production", "stripe_key"); err != nil || value != stripeKey {
		t.Errorf("shop/production#stripe_key = %q, %v", value, err)
	}
	arn := "arn:aws:secretsmanager:eu-central-1:123456789012:secret:shop/production-AbCdEf"
	if value, err := fetchOne(t, KindAWS, awsSettings(server), good, arn, "port"); err != nil || value != "8080" {
		t.Errorf("by ARN = %q, %v", value, err)
	}
	if value, err := fetchOne(t, KindAWS, awsSettings(server), good, "plain", ""); err != nil || value != "just-a-string" {
		t.Errorf("a plain string = %q, %v", value, err)
	}

	_, err := fetchOne(t, KindAWS, awsSettings(server), good, "shop/production", "")
	if err != nil {
		t.Errorf("without a key, the whole JSON is the value: %v", err)
	}
	_, err = fetchOne(t, KindAWS, awsSettings(server), good, "missing", "")
	expectReason(t, "a secret that is not there", err, NotFound)
	_, err = fetchOne(t, KindAWS, awsSettings(server), good, "denied", "")
	expectReason(t, "a policy that does not allow it", err, Denied)
	_, err = fetchOne(t, KindAWS, awsSettings(server), awsCredentialsFor("wrong"), "shop/production", "stripe_key")
	expectReason(t, "a read with a wrong secret key", err, Denied)
	_, err = fetchOne(t, KindAWS, awsSettings(server), good, "binary", "")
	expectReason(t, "a binary secret", err, BadAnswer)
	_, err = fetchOne(t, KindAWS, awsSettings(server), good, "slow", "")
	expectReason(t, "an AWS that does not answer", err, Unreachable)
	_, err = fetchOne(t, KindAWS, awsSettings(server), good, "huge", "")
	expectReason(t, "an answer too large", err, BadAnswer)
}

// AWS's own test vector, get-vanilla, from its Signature Version 4 test
// suite: the one request whose signature is published.
func TestSignatureVersion4MatchesAWSTestVector(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://example.amazonaws.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	signV4(req, nil, awsCredentials{accessKeyID: "AKIDEXAMPLE", secretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"},
		"us-east-1", "service", time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC))
	want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, " +
		"SignedHeaders=host;x-amz-date, Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"
	if got := req.Header.Get("Authorization"); got != want {
		t.Errorf("Authorization:\n got %s\nwant %s", got, want)
	}
}

// The panel's own client will not reach the metadata service or itself, and
// a connection pointed at either fails its test with a sentence saying so.
func TestThePanelsClientRefusesAddressesItMustNotReach(t *testing.T) {
	r := &Resolver{Client: guarded()}
	f := newFakeVault(t) // on 127.0.0.1, which is the panel's own machine
	for _, address := range []string{f.server.URL, "http://169.254.169.254", "http://127.0.0.2:8200"} {
		err := r.Test(t.Context(), KindVault, map[string]string{"address": address, "mount": "secret"},
			map[string]string{"token": fakeVaultToken})
		expectReason(t, address, err, Unreachable)
		if err != nil && !strings.Contains(err.Error(), "will not connect") {
			t.Errorf("%s: the refusal does not say it was the panel's: %v", address, err)
		}
	}
	if n := f.reads.Load(); n != 0 {
		t.Errorf("the fake on loopback was reached %d times", n)
	}
}

// A redirect to another host would carry Vault's token header with it.
func TestARedirectToAnotherHostIsNotFollowed(t *testing.T) {
	elsewhere := newFakeVault(t)
	redirecting := http.NewServeMux()
	redirecting.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(elsewhere.server.URL, "127.0.0.1", "localhost", 1)+r.URL.Path, http.StatusTemporaryRedirect)
	})
	server := newServer(t, redirecting)
	err := testOne(t, KindVault, map[string]string{"address": server}, map[string]string{"token": fakeVaultToken})
	expectReason(t, "a redirect to another host", err, Unreachable)
	if elsewhere.reads.Load() != 0 {
		t.Error("the redirect was followed")
	}
}

func TestNormalize(t *testing.T) {
	settings, credentials, err := Normalize(KindVault, map[string]string{"address": " https://vault.example.test/ "},
		map[string]string{"token": "hvs.x\n"})
	if err != nil {
		t.Fatal(err)
	}
	if settings["mount"] != "secret" || settings["auth"] != "token" || settings["address"] != "https://vault.example.test/" {
		t.Errorf("defaults: %v", settings)
	}
	if credentials["token"] != "hvs.x" {
		t.Errorf("a pasted newline was kept: %q", credentials["token"])
	}
	for name, c := range map[string]struct {
		kind                  string
		settings, credentials map[string]string
	}{
		"no address":        {KindVault, map[string]string{}, map[string]string{"token": "x"}},
		"not http":          {KindVault, map[string]string{"address": "file:///etc"}, map[string]string{"token": "x"}},
		"user in address":   {KindVault, map[string]string{"address": "https://u:p@vault"}, map[string]string{"token": "x"}},
		"no token":          {KindVault, map[string]string{"address": "https://vault"}, map[string]string{}},
		"approle no secret": {KindVault, map[string]string{"address": "https://vault", "auth": "approle"}, map[string]string{"role_id": "x"}},
		"unknown setting":   {KindDoppler, map[string]string{"project": "x"}, map[string]string{"token": "x"}},
		"bad region":        {KindAWS, map[string]string{"region": "moon"}, awsCredentialsFor("x")},
		"no project":        {KindInfisical, map[string]string{"environment": "prod"}, map[string]string{"client_id": "x", "client_secret": "y"}},
		"unknown kind":      {"keepass", nil, nil},
	} {
		if _, _, err := Normalize(c.kind, c.settings, c.credentials); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestParseReference(t *testing.T) {
	for text, want := range map[string][3]string{
		"vault-prod:shop#stripe_key": {"vault-prod", "shop", "stripe_key"},
		"doppler:STRIPE_KEY":         {"doppler", "STRIPE_KEY", ""},
		"aws:arn:aws:secretsmanager:eu-central-1:1:secret:shop-AbC#key": {"aws", "arn:aws:secretsmanager:eu-central-1:1:secret:shop-AbC", "key"},
	} {
		connection, path, key, err := ParseReference(text)
		if err != nil || [3]string{connection, path, key} != want {
			t.Errorf("%s = %q %q %q, %v", text, connection, path, key, err)
		}
	}
	for _, text := range []string{"no-colon", ":path", "conn:", "conn:path#"} {
		if _, _, _, err := ParseReference(text); err == nil {
			t.Errorf("%q was accepted", text)
		}
	}
	for kind, path := range map[string]string{KindVault: "../etc", KindDoppler: "lower-case-dash", KindInfisical: "a//b", KindAWS: "has space"} {
		if err := CheckReference(kind, path, ""); err == nil {
			t.Errorf("%s accepted %q", kind, path)
		}
	}
}
