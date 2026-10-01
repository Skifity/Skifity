package secretmgr

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"strings"
	"time"

	"skifity/internal/sigv4"
)

// awsSecrets reads AWS Secrets Manager with an access key.
//
// A secret is one signed POST to secretsmanager.<region>.amazonaws.com, the
// JSON 1.1 protocol: X-Amz-Target: secretsmanager.GetSecretValue and
// {"SecretId": ...}, answered with {"SecretString": ...}. The test is STS's
// GetCallerIdentity, which every valid key may call whatever its policy says,
// and which reads nothing.
//
// An endpoint in the settings replaces both addresses, for LocalStack or a
// VPC endpoint; the region still goes into the signature.
type awsSecrets struct {
	caller
	region   string
	endpoint string
	creds    sigv4.Credentials
	now      func() time.Time
}

func newAWS(c caller, settings, credentials map[string]string) *awsSecrets {
	return &awsSecrets{
		caller:   c,
		region:   settings["region"],
		endpoint: strings.TrimSuffix(settings["endpoint"], "/"),
		creds: sigv4.Credentials{
			AccessKeyID:     credentials["access_key_id"],
			SecretAccessKey: credentials["secret_access_key"],
			SessionToken:    credentials["session_token"],
		},
		now: time.Now,
	}
}

// address is where a service answers in this region.
func (s *awsSecrets) address(service string) string {
	if s.endpoint != "" {
		return s.endpoint + "/"
	}
	domain := "amazonaws.com"
	if strings.HasPrefix(s.region, "cn-") {
		domain = "amazonaws.com.cn"
	}
	return "https://" + service + "." + s.region + "." + domain + "/"
}

func (s *awsSecrets) sign(service string, body []byte) func(*http.Request) {
	return func(req *http.Request) {
		sigv4.Sign(req, body, s.creds, s.region, service, s.now())
	}
}

func (s *awsSecrets) test(ctx context.Context) error {
	body := []byte("Action=GetCallerIdentity&Version=2011-06-15")
	a, err := s.send(ctx, http.MethodPost, s.address("sts"),
		map[string]string{"Content-Type": "application/x-www-form-urlencoded; charset=utf-8"}, body, s.sign("sts", body))
	if err != nil {
		return err
	}
	if a.status != http.StatusOK {
		var failed struct {
			Error struct {
				Code    string `xml:"Code"`
				Message string `xml:"Message"`
			} `xml:"Error"`
		}
		_ = xml.Unmarshal(a.body, &failed)
		return s.failed(a, "the access key", failed.Error.Code, failed.Error.Message)
	}
	var identity struct {
		Result struct {
			Arn string `xml:"Arn"`
		} `xml:"GetCallerIdentityResult"`
	}
	if err := xml.Unmarshal(a.body, &identity); err != nil || identity.Result.Arn == "" {
		return failure(BadAnswer, "AWS answered GetCallerIdentity with something that is not its answer")
	}
	return nil
}

func (s *awsSecrets) fetch(ctx context.Context, id string) (secret, error) {
	body, _ := json.Marshal(map[string]string{"SecretId": id})
	a, err := s.send(ctx, http.MethodPost, s.address("secretsmanager"), map[string]string{
		"Content-Type": "application/x-amz-json-1.1",
		"X-Amz-Target": "secretsmanager.GetSecretValue",
	}, body, s.sign("secretsmanager", body))
	if err != nil {
		return secret{}, err
	}
	if a.status != http.StatusOK {
		var failed struct {
			Type         string `json:"__type"`
			Message      string `json:"Message"`
			MessageLower string `json:"message"`
		}
		_ = json.Unmarshal(a.body, &failed)
		return secret{}, s.failed(a, id, failed.Type, failed.Message+failed.MessageLower)
	}
	var read struct {
		SecretString *string `json:"SecretString"`
		SecretBinary *string `json:"SecretBinary"`
	}
	if err := s.decode(a, &read); err != nil {
		return secret{}, err
	}
	if read.SecretString == nil {
		if read.SecretBinary != nil {
			return secret{}, failure(BadAnswer, "%s is a binary secret, and a variable can only be read from a text one", id)
		}
		return secret{}, failure(BadAnswer, "AWS answered %s without the secret", id)
	}
	return secret{value: *read.SecretString}, nil
}

// failed turns an AWS error into an *Error by its code, which says more than
// the status: most of them are a 400.
func (s *awsSecrets) failed(a answer, what, code, message string) *Error {
	if i := strings.LastIndex(code, "#"); i >= 0 {
		code = code[i+1:]
	}
	if code != "" {
		message = code + ": " + message
	}
	switch code {
	case "ResourceNotFoundException", "InvalidRequestException":
		a.status = http.StatusNotFound
	case "AccessDeniedException", "UnrecognizedClientException", "InvalidSignatureException",
		"ExpiredTokenException", "IncompleteSignature", "MissingAuthenticationToken",
		"InvalidClientTokenId", "SignatureDoesNotMatch", "DecryptionFailure", "AccessDenied":
		a.status = http.StatusForbidden
	case "ThrottlingException", "Throttling":
		a.status = http.StatusTooManyRequests
	case "InternalServiceError", "InternalFailure", "ServiceUnavailable":
		a.status = http.StatusServiceUnavailable
	}
	return s.status(a, what, message)
}
