package gitsrc

import "testing"

// TestValidateRepoURLRefusesWhatAShellWouldRead: the address ends up in the
// build pod, and the build pod has the team's Git token in its environment.
func TestValidateRepoURLRefusesWhatAShellWouldRead(t *testing.T) {
	for _, raw := range []string{
		"",
		"   ",
		"github.com/acme/shop",
		"http://github.com/acme/shop",
		"ftp://github.com/acme/shop",
		"file:///etc/passwd",
		"https://",
		"https://github.com",
		"https://github.com/",
		"https://token:x@github.com/acme/shop",
		`https://github.com/acme/shop"; id; echo "`,
		"https://github.com/acme/$(id).git",
		"https://github.com/acme/shop.git\nrm -rf /",
		"https://github.com/acme/shop.git; rm -rf /",
		"https://github.com/acme/shop.git | sh",
		"https://github.com/acme/`id`.git",
	} {
		if got, err := ValidateRepoURL(raw); err == nil {
			t.Errorf("ValidateRepoURL(%q) accepted it as %q", raw, got)
		}
	}
}

func TestValidateRepoURLAcceptsRealRepositories(t *testing.T) {
	for raw, want := range map[string]string{
		"https://github.com/acme/shop":            "https://github.com/acme/shop",
		"  https://github.com/acme/shop.git  ":    "https://github.com/acme/shop.git",
		"https://gitlab.example.com/team/app.git": "https://gitlab.example.com/team/app.git",
		"https://git.example.com:8443/a/b.git":    "https://git.example.com:8443/a/b.git",
		// A query and a fragment are never part of a clone URL, and carrying
		// them into the build would only be one more thing to go wrong.
		"https://github.com/acme/shop.git?ref=main#readme": "https://github.com/acme/shop.git",
	} {
		got, err := ValidateRepoURL(raw)
		if err != nil {
			t.Errorf("ValidateRepoURL(%q): %v", raw, err)
			continue
		}
		if got != want {
			t.Errorf("ValidateRepoURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

// TestSameHostDecidesWhereATokenGoes is the check that stops a team's GitHub
// token being posted to a host somebody named in a form.
func TestSameHostDecidesWhereATokenGoes(t *testing.T) {
	for _, tc := range []struct {
		repo, base string
		want       bool
	}{
		// A GitHub connection has no base address of its own.
		{"https://github.com/acme/shop", "", true},
		{"https://GitHub.com/acme/shop", "", true},
		{"https://attacker.test/acme/shop", "", false},
		{"https://github.com.attacker.test/acme/shop", "", false},
		{"https://notgithub.com/acme/shop", "", false},
		{"https://gitlab.example.com/a/b", "https://gitlab.example.com", true},
		{"https://gitlab.example.com/a/b", "gitlab.example.com", true},
		{"https://gitlab.example.com/a/b", "https://gitlab.other.test", false},
		{"", "https://github.com", false},
		{"https://github.com/a/b", "https://", false},
	} {
		if got := SameHost(tc.repo, tc.base); got != tc.want {
			t.Errorf("SameHost(%q, %q) = %v, want %v", tc.repo, tc.base, got, tc.want)
		}
	}
}

func TestValidateBaseURLClosesTheSameDoor(t *testing.T) {
	// A Git connection's base URL is stored by a team administrator and the
	// panel's own process then makes requests to it, so it goes through the
	// same door a repository address does.
	refused := []string{
		"http://git.example.test",                    // not https
		"https://user:token@git.example.test",        // credentials belong in the token field
		"https://",                                   // no host
		"ftp://git.example.test",                     // not https
		"https://git.example.test/\nX-Injected: yes", // a control character
	}
	for _, bad := range refused {
		if _, err := ValidateBaseURL(bad); err == nil {
			t.Errorf("%q was accepted as a Git server address", bad)
		}
	}

	// Empty means the hosted provider, which is the ordinary case.
	if got, err := ValidateBaseURL("  "); err != nil || got != "" {
		t.Errorf("an empty address gave (%q, %v), want it accepted as the hosted provider", got, err)
	}

	// A self-hosted provider is the whole reason this field exists.
	for raw, want := range map[string]string{
		"https://git.example.test":        "https://git.example.test",
		"https://git.example.test/":       "https://git.example.test",
		"https://git.example.test:3000":   "https://git.example.test:3000",
		"https://git.example.test/gitea/": "https://git.example.test",
		"https://git.example.test?a=b":    "https://git.example.test",
	} {
		got, err := ValidateBaseURL(raw)
		if err != nil {
			t.Errorf("ValidateBaseURL(%q): %v", raw, err)
			continue
		}
		if got != want {
			t.Errorf("ValidateBaseURL(%q) = %q, want %q", raw, got, want)
		}
	}
}
