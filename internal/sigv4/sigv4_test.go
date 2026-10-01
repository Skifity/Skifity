package sigv4

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// AWS's own test vector, get-vanilla, from the Signature Version 4 test suite
// (aws-sig-v4-test-suite/get-vanilla, published with the SigV4 documentation):
// a GET of / on example.amazonaws.com at 20150830T123600Z, signed for the
// service "service" in us-east-1 with the example key AWS documents.
//
// The expected values below are the suite's .creq, .sts and .authz files. A
// signer that gets one character of the canonical request wrong gets a
// different signature, so matching it is the whole test.
func TestTheGetVanillaVectorSigns(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://example.amazonaws.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "example.amazonaws.com"
	at := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)
	creds := Credentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"}

	Sign(req, nil, creds, "us-east-1", "service", at)

	canonical, signed := CanonicalRequest(req, nil)
	wantCanonical := strings.Join([]string{
		"GET",
		"/",
		"",
		"host:example.amazonaws.com",
		"x-amz-date:20150830T123600Z",
		"",
		"host;x-amz-date",
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	}, "\n")
	if canonical != wantCanonical {
		t.Errorf("the canonical request is\n%s\nwant\n%s", canonical, wantCanonical)
	}
	if signed != "host;x-amz-date" {
		t.Errorf("signed headers %q", signed)
	}
	if got := hexSHA256([]byte(canonical)); got != "bb579772317eb040ac9ed261061d46c1f17a8133879d6129b6e1c25292927e63" {
		t.Errorf("the canonical request hashes to %s, not the one the string to sign carries", got)
	}

	want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, " +
		"SignedHeaders=host;x-amz-date, " +
		"Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"
	if got := req.Header.Get("Authorization"); got != want {
		t.Errorf("Authorization is\n%s\nwant\n%s", got, want)
	}
}

// A query is sorted and escaped the way AWS reads it, and a body changes the
// signature: the payload's hash is part of what is signed.
func TestTheQueryAndTheBodyAreSigned(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://route53.amazonaws.com/2013-04-01/hostedzone/Z1/rrset?type=A&name=a b.example.com.", nil)
	canonical, _ := CanonicalRequest(req, nil)
	if !strings.Contains(canonical, "\nname=a%20b.example.com.&type=A\n") {
		t.Errorf("the query was not sorted and escaped:\n%s", canonical)
	}

	creds := Credentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "not-a-real-secret"}
	at := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	one, _ := http.NewRequest(http.MethodPost, "https://route53.amazonaws.com/2013-04-01/hostedzone/Z1/rrset/", nil)
	two, _ := http.NewRequest(http.MethodPost, "https://route53.amazonaws.com/2013-04-01/hostedzone/Z1/rrset/", nil)
	Sign(one, []byte("<a/>"), creds, "us-east-1", "route53", at)
	Sign(two, []byte("<b/>"), creds, "us-east-1", "route53", at)
	if one.Header.Get("Authorization") == two.Header.Get("Authorization") {
		t.Error("two different bodies were given the same signature")
	}
}
