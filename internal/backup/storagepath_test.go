package backup

import "testing"

// TestTheBucketsAddressIsWhereTheJobGoes: the host and port the jobs' network
// policy names are the ones the presigned URL sends them to.
func TestTheBucketsAddressIsWhereTheJobGoes(t *testing.T) {
	for _, c := range []struct {
		endpoint string
		host     string
		port     int
	}{
		{"http://10.1.2.3:9000", "10.1.2.3", 9000},
		{"https://s3.eu-central-1.amazonaws.com", "s3.eu-central-1.amazonaws.com", 443},
		{"http://minio.lan", "minio.lan", 80},
		{"minio.lan:9000", "minio.lan", 9000},
		{"objects.example.com", "objects.example.com", 443},
		{"http://[fd00::1]:9000", "fd00::1", 9000},
	} {
		host, port, err := (&Storage{endpoint: c.endpoint}).Address()
		if err != nil {
			t.Errorf("%s: %v", c.endpoint, err)
			continue
		}
		if host != c.host || port != c.port {
			t.Errorf("%s is %s:%d, want %s:%d", c.endpoint, host, port, c.host, c.port)
		}
	}
	if _, _, err := (&Storage{endpoint: "http://minio.lan:99999"}).Address(); err == nil {
		t.Error("a port that cannot exist was accepted")
	}
}
