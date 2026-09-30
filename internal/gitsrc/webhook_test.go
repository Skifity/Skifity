package gitsrc

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"testing"
)

// TestAForkIsRecognised is what decides whether a preview environment is
// handed the project's secrets. A pull request from another repository can be
// opened by anyone, and its code decides what runs in the container.
func TestAForkIsRecognised(t *testing.T) {
	header := http.Header{}
	header.Set("X-GitHub-Event", "pull_request")

	body := func(headRepo, baseRepo string) []byte {
		return []byte(`{
			"action": "opened",
			"number": 7,
			"pull_request": {
				"head": {"ref": "feature", "sha": "abc123", "repo": {"full_name": "` + headRepo + `"}},
				"base": {"repo": {"full_name": "` + baseRepo + `"}},
				"title": "Add a thing",
				"user": {"login": "someone"}
			},
			"repository": {"clone_url": "https://github.com/acme/shop.git", "full_name": "` + baseRepo + `"}
		}`)
	}

	same, err := ParseWebhook(header, body("acme/shop", "acme/shop"))
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if same.Fork {
		t.Error("a pull request from the repository itself was treated as a fork")
	}

	fork, err := ParseWebhook(header, body("attacker/shop", "acme/shop"))
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if !fork.Fork {
		t.Fatal("a pull request from another repository was not treated as a fork")
	}

	// Anything missing counts as a fork: the safe answer to "whose code is
	// this" is the cautious one.
	missing, err := ParseWebhook(header, []byte(`{
		"action": "opened", "number": 7,
		"pull_request": {"head": {"ref": "feature", "sha": "abc123"}, "title": "t", "user": {"login": "x"}},
		"repository": {"clone_url": "https://github.com/acme/shop.git"}
	}`))
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if !missing.Fork {
		t.Fatal("a pull request with no head repository was trusted")
	}
}

// TestAGitLabForkIsRecognised: GitLab says it with project ids rather than
// names, and the same rule applies.
func TestAGitLabForkIsRecognised(t *testing.T) {
	header := http.Header{}
	header.Set("X-Gitlab-Event", "Merge Request Hook")

	body := func(source, target int) []byte {
		return []byte(fmt.Sprintf(`{
			"object_attributes": {
				"action": "open", "iid": 4, "source_branch": "feature",
				"source_project_id": %d, "target_project_id": %d,
				"last_commit": {"id": "abc123", "message": "m"}, "title": "t"
			},
			"project": {"git_http_url": "https://gitlab.example.com/acme/shop.git"},
			"user": {"name": "someone"}
		}`, source, target))
	}

	same, err := ParseWebhook(header, body(12, 12))
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if same.Fork {
		t.Error("a merge request from the project itself was treated as a fork")
	}
	fork, err := ParseWebhook(header, body(99, 12))
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if !fork.Fork {
		t.Fatal("a merge request from another project was not treated as a fork")
	}

	// A payload with the project ids missing has to count as a fork. The code
	// says so and nothing held it: this is the shape an odd GitLab version, or
	// somebody probing, would send, and reading it as "not a fork" hands the
	// project's secrets to whoever opened the merge request.
	for _, missing := range []string{
		`"action": "open", "iid": 4, "source_branch": "feature", "target_project_id": 12`,
		`"action": "open", "iid": 4, "source_branch": "feature", "source_project_id": 12`,
		`"action": "open", "iid": 4, "source_branch": "feature"`,
	} {
		raw := []byte(`{"object_attributes": {` + missing + `},
			"project": {"git_http_url": "https://gitlab.example.com/acme/shop.git"},
			"user": {"name": "someone"}}`)
		event, err := ParseWebhook(header, raw)
		if err != nil {
			t.Fatalf("ParseWebhook: %v", err)
		}
		if !event.Fork {
			t.Errorf("a merge request with %s was not treated as a fork", missing)
		}
	}
}

// Gitea's own header is a digest, not the secret.
//
// It used to be checked against the secret itself — comparing a secret with an
// HMAC, two things that can never be equal — so a push from a Gitea that sends
// only this header was always rejected as unverified, and the message blamed
// the operator's secret.
func TestAGiteaSignatureIsADigestAndNotTheSecret(t *testing.T) {
	const secret = "not-a-real-webhook-secret"
	body := []byte(`{"ref":"refs/heads/main"}`)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	digest := hex.EncodeToString(mac.Sum(nil))

	if err := VerifyGiteaSignature(secret, body, digest); err != nil {
		t.Fatalf("a real Gitea signature was rejected: %v", err)
	}
	// Recent Gitea sends GitHub's spelling as well, and both are accepted.
	if err := VerifyGiteaSignature(secret, body, "sha256="+digest); err != nil {
		t.Errorf("the prefixed spelling was rejected: %v", err)
	}
	// The secret itself is not a signature, which is exactly what was wrong.
	if err := VerifyGiteaSignature(secret, body, secret); err == nil {
		t.Error("the secret was accepted in place of a signature")
	}
	if err := VerifyGiteaSignature(secret, body, ""); err == nil {
		t.Error("an empty signature was accepted")
	}
	if err := VerifyGiteaSignature(secret, []byte("a different body"), digest); err == nil {
		t.Error("a signature for a different body was accepted")
	}
	if err := VerifyGiteaSignature("", body, digest); err == nil {
		t.Error("a connection with no secret verified a push")
	}
}
