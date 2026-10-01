// Package sigv4 signs an HTTP request the way AWS asks: Signature Version 4.
//
// It is here for one caller, Route 53, and is the smallest thing that does the
// job with the standard library: a canonical request, a string to sign, a key
// derived from the secret for the day, the region and the service, and an
// Authorization header. The AWS SDK would do it too, and would bring a few
// hundred packages into a static binary to sign what amounts to four HMACs.
//
// What is not here, because Route 53 does not need it: presigned URLs, a
// session token, streaming (chunked) payloads, and the S3 exceptions to the
// canonical path. The test holds it to AWS's published get-vanilla vector.
//
// https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv-create-signed-request.html
package sigv4

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Algorithm is the only one this package speaks.
const Algorithm = "AWS4-HMAC-SHA256"

// Credentials are an access key: its public id and its secret, and the
// session token that comes with temporary ones.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// Sign adds X-Amz-Date and an Authorization header to a request whose body is
// payload. The request's Host is signed, as AWS requires; any X-Amz-* headers
// already on it are signed too.
func Sign(req *http.Request, payload []byte, creds Credentials, region, service string, now time.Time) {
	now = now.UTC()
	stamp := now.Format("20060102T150405Z")
	day := now.Format("20060102")
	req.Header.Set("X-Amz-Date", stamp)
	if creds.SessionToken != "" {
		// Signed like every other X-Amz-* header.
		req.Header.Set("X-Amz-Security-Token", creds.SessionToken)
	}

	canonical, signed := CanonicalRequest(req, payload)
	scope := day + "/" + region + "/" + service + "/aws4_request"
	toSign := strings.Join([]string{Algorithm, stamp, scope, hexSHA256([]byte(canonical))}, "\n")

	key := hmacSHA256([]byte("AWS4"+creds.SecretAccessKey), []byte(day))
	key = hmacSHA256(key, []byte(region))
	key = hmacSHA256(key, []byte(service))
	key = hmacSHA256(key, []byte("aws4_request"))
	signature := hex.EncodeToString(hmacSHA256(key, []byte(toSign)))

	req.Header.Set("Authorization", Algorithm+" Credential="+creds.AccessKeyID+"/"+scope+
		", SignedHeaders="+signed+", Signature="+signature)
}

// CanonicalRequest is the request as AWS hashes it, and the list of headers
// that went into it. Exported so a test can compare it with the published one.
func CanonicalRequest(req *http.Request, payload []byte) (string, string) {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	headers := map[string]string{"host": host}
	for name, values := range req.Header {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-amz-") || lower == "content-type" {
			headers[lower] = strings.Join(trimAll(values), ",")
		}
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	var block strings.Builder
	for _, name := range names {
		block.WriteString(name + ":" + headers[name] + "\n")
	}
	signed := strings.Join(names, ";")

	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	return strings.Join([]string{
		req.Method,
		path,
		canonicalQuery(req.URL.Query()),
		block.String(),
		signed,
		hexSHA256(payload),
	}, "\n"), signed
}

// canonicalQuery sorts the parameters by name, then value, each escaped the
// way AWS escapes them: everything but unreserved characters, a space as %20.
func canonicalQuery(values url.Values) string {
	var pairs []string
	for name, list := range values {
		for _, value := range list {
			pairs = append(pairs, escape(name)+"="+escape(value))
		}
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "&")
}

func escape(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

func trimAll(values []string) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = strings.Join(strings.Fields(value), " ")
	}
	return out
}

func hexSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}
