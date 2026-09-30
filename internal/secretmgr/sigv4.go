package secretmgr

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

// AWS Signature Version 4, for the two requests the panel makes to AWS.
//
// Written here rather than taken from the AWS SDK, which would be a dependency
// tree larger than the rest of the panel's for two POSTs. The algorithm is
// the published one — a canonical request, a string to sign, and a key
// derived from the secret by a chain of HMACs — and the test checks it
// against AWS's own test vector.

const sigv4Algorithm = "AWS4-HMAC-SHA256"

// awsCredentials are what a request is signed with.
type awsCredentials struct {
	accessKeyID     string
	secretAccessKey string
	sessionToken    string
}

// signV4 signs a request in place. Every header already on it is signed,
// along with Host and the X-Amz-Date this adds.
func signV4(req *http.Request, body []byte, creds awsCredentials, region, service string, now time.Time) {
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	date := now.Format("20060102")

	req.Header.Set("X-Amz-Date", amzDate)
	if creds.sessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", creds.sessionToken)
	}
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}

	// The headers, lowercased and sorted, each value with its runs of spaces
	// collapsed. User-Agent and Accept are left out: a proxy may change them,
	// and a signature over them would then not match.
	values := map[string]string{"host": host}
	for name, list := range req.Header {
		lower := strings.ToLower(name)
		if lower == "user-agent" || lower == "accept" || lower == "authorization" {
			continue
		}
		trimmed := make([]string, len(list))
		for i, v := range list {
			trimmed[i] = strings.Join(strings.Fields(v), " ")
		}
		values[lower] = strings.Join(trimmed, ",")
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	var canonicalHeaders strings.Builder
	for _, name := range names {
		canonicalHeaders.WriteString(name + ":" + values[name] + "\n")
	}
	signedHeaders := strings.Join(names, ";")

	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	payload := sha256.Sum256(body)
	canonicalRequest := strings.Join([]string{
		req.Method,
		path,
		canonicalQuery(req.URL.Query()),
		canonicalHeaders.String(),
		signedHeaders,
		hex.EncodeToString(payload[:]),
	}, "\n")

	scope := date + "/" + region + "/" + service + "/aws4_request"
	hashed := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{sigv4Algorithm, amzDate, scope, hex.EncodeToString(hashed[:])}, "\n")

	key := hmacSHA256([]byte("AWS4"+creds.secretAccessKey), date)
	key = hmacSHA256(key, region)
	key = hmacSHA256(key, service)
	key = hmacSHA256(key, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(key, stringToSign))

	req.Header.Set("Authorization", sigv4Algorithm+" Credential="+creds.accessKeyID+"/"+scope+
		", SignedHeaders="+signedHeaders+", Signature="+signature)
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

// canonicalQuery is the query with its names and values encoded the way AWS
// encodes them, sorted by name and then value.
func canonicalQuery(query url.Values) string {
	if len(query) == 0 {
		return ""
	}
	pairs := make([]string, 0, len(query))
	for name, list := range query {
		for _, value := range list {
			pairs = append(pairs, awsEscape(name)+"="+awsEscape(value))
		}
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "&")
}

// awsEscape percent-encodes everything but the unreserved characters, with
// a space as %20 rather than +.
func awsEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}
