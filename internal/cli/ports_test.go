package cli

import "testing"

func TestAPortIsWrittenAsANumberAndAProtocol(t *testing.T) {
	for arg, want := range map[string]struct {
		port     int
		protocol string
	}{"25565": {25565, "tcp"}, "19132/udp": {19132, "udp"}, "53/UDP": {53, "udp"}} {
		port, protocol, err := parsePortArg(arg)
		if err != nil || port != want.port || protocol != want.protocol {
			t.Errorf("%q read as %d/%s (%v)", arg, port, protocol, err)
		}
	}
	for _, bad := range []string{"", "http", "0", "70000", "80/sctp", "25565/"} {
		if _, _, err := parsePortArg(bad); err == nil {
			t.Errorf("%q was read as a port", bad)
		}
	}
}
