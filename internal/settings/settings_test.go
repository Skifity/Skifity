package settings

import "testing"

// A setting whose kind does not match its validator is a form that accepts what
// the server will reject: a text box for a yes/no value, or a free-text field
// for something that must be one of four words.
func TestEveryDefinitionHasAKindItsValidatorAgrees(t *testing.T) {
	for _, def := range Definitions {
		t.Run(def.Key, func(t *testing.T) {
			kind := def.ResolvedKind()

			switch kind {
			case KindBool:
				// The switch writes exactly these two, so the validator has to
				// take exactly these two.
				for _, value := range []string{"true", "false"} {
					if def.Validate != nil {
						if err := def.Validate(value); err != nil {
							t.Errorf("a switch writes %q, which the validator rejects: %v", value, err)
						}
					}
				}
				if def.Validate != nil {
					if err := def.Validate("yes"); err == nil {
						t.Error("a bool setting should not accept free text such as \"yes\"")
					}
				}
			case KindChoice:
				if len(def.Options) == 0 {
					t.Fatal("a choice setting with no options is an empty dropdown")
				}
				for _, option := range def.Options {
					if def.Validate != nil {
						if err := def.Validate(option); err != nil {
							t.Errorf("option %q is offered but rejected: %v", option, err)
						}
					}
				}
				if def.Validate != nil {
					if err := def.Validate("definitely-not-an-option"); err == nil {
						t.Error("a choice setting accepted a value that is not one of its options")
					}
				}
			case KindNumber:
				if def.Validate != nil {
					if err := def.Validate("12"); err != nil {
						t.Errorf("a number field rejects a number: %v", err)
					}
					if err := def.Validate("not a number"); err == nil {
						t.Error("a number setting accepted text")
					}
				}
			}

			// Clearing a setting is how an integration is disconnected, so an
			// empty value must always be allowed whatever the kind.
			if def.Validate != nil {
				if err := def.Validate(""); err != nil {
					t.Errorf("an empty value must be allowed, so a setting can be cleared: %v", err)
				}
			}

			if def.Label == "" || def.Help == "" || def.Group == "" {
				t.Error("every setting needs a label, a group and a line of help")
			}
		})
	}
}

// A secret that is also a switch, or a choice with no options, is a control the
// panel cannot draw.
func TestKindsAreCoherent(t *testing.T) {
	for _, def := range Definitions {
		kind := def.ResolvedKind()
		if def.Secret && kind == KindBool {
			t.Errorf("%s: a secret cannot be a switch", def.Key)
		}
		if len(def.Options) > 0 && kind != KindChoice {
			t.Errorf("%s: has options but is drawn as %s", def.Key, kind)
		}
		if def.Multiline && kind != KindText {
			t.Errorf("%s: only a text setting can be multiline, got %s", def.Key, kind)
		}
	}
}

// TestK3sVersionPinAcceptsOnlyReleasesThatExist: a wrong version here is not a
// validation error later, it is a server that downloads nothing and fails
// halfway through being added, which looks like a network problem.
func TestK3sVersionPinAcceptsOnlyReleasesThatExist(t *testing.T) {
	for _, good := range []string{"", "v1.34.1+k3s1", "v1.28.10+k3s2"} {
		if err := validateK3sVersion(good); err != nil {
			t.Errorf("validateK3sVersion(%q): %v", good, err)
		}
	}
	for _, bad := range []string{
		"1.34.1+k3s1", // no leading v
		"v1.34.1",     // a Kubernetes version, not a k3s release
		"v1.34+k3s1",  // not three parts
		"stable",      // a channel, which the installer takes differently
		"latest",
		"v1.34.1+k3s1 ; rm -rf /",
	} {
		if err := validateK3sVersion(bad); err == nil {
			t.Errorf("validateK3sVersion(%q) was accepted", bad)
		}
	}
}

// What people paste instead of the tunnel token, and what happens to it.
//
// The dashboard shows the token inside a `cloudflared service install <token>`
// command line, and the tunnel's UUID is in the address bar above it. Both get
// pasted. Catching them in a text box is the difference between a message and a
// connector that restarts for ever.
func TestValidateCloudflareTunnelToken(t *testing.T) {
	// Obviously fake: the account, the tunnel and the secret are all zeroes.
	const good = "eyJhIjoiMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMCIsInQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJzIjoiMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAifQ=="

	cases := []struct {
		name  string
		value string
		ok    bool
	}{
		{"a token", good, true},
		{"nothing, which is how an integration is disconnected", "", true},
		{"the whole install command", "cloudflared service install " + good, false},
		{"the tunnel's ID from the dashboard URL", "3a7f1b2c-4d5e-6f70-8192-a3b4c5d6e7f8", false},
		{"a sentence", "my cloudflare token", false},
		{"base64 of something else entirely", "aGVsbG8gdGhlcmUsIG5vdCBhIHRva2Vu", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCloudflareTunnelToken(tc.value)
			if tc.ok && err != nil {
				t.Fatalf("rejected a value it should accept: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("accepted a value that is not a tunnel token")
			}
		})
	}
}

// Every setting the panel offers has to have a validator, or saving one is a
// field that accepts anything and fails somewhere else.
func TestCloudflareTunnelTokenIsASecret(t *testing.T) {
	def, ok := Lookup(KeyCloudflareTunnelToken)
	if !ok {
		t.Fatal("the tunnel token is not in the catalogue")
	}
	if !def.Secret {
		t.Error("a credential that opens somebody's Cloudflare account must be stored sealed")
	}
	if def.Group != GroupDomains {
		t.Errorf("group = %q, want %q", def.Group, GroupDomains)
	}
}
