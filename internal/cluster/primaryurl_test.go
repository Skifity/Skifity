package cluster

import (
	"testing"

	"skifity/internal/store"
)

func TestTheAddressAnAppReadsIsItsOwnDomainFirst(t *testing.T) {
	cases := []struct {
		domains []store.Domain
		want    string
	}{
		{nil, ""},
		{[]store.Domain{{Hostname: "web-production.203-0-113-7.sslip.io", Auto: true}}, "http://web-production.203-0-113-7.sslip.io"},
		{[]store.Domain{
			{Hostname: "web-production.203-0-113-7.sslip.io", Auto: true},
			{Hostname: "shop.example.com", TLS: true, Path: "/"},
		}, "https://shop.example.com"},
		{[]store.Domain{{Hostname: "example.com", TLS: true, Path: "/app/"}}, "https://example.com/app"},
	}
	for _, tc := range cases {
		if got := primaryURL(tc.domains); got != tc.want {
			t.Errorf("primaryURL(%+v) = %q, want %q", tc.domains, got, tc.want)
		}
	}
}
