package crypto

import "testing"

// TestRandomNameIsUsableAsAKubernetesName: RandomToken is base64url, which
// holds "_" and uppercase, and a Kubernetes object name may hold neither. A
// name built from one is refused by the API server, which is a failure a long
// way from its cause.
func TestRandomNameIsUsableAsAKubernetesName(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		name, err := RandomName(8)
		if err != nil {
			t.Fatalf("RandomName: %v", err)
		}
		if len(name) != 8 {
			t.Fatalf("RandomName(8) returned %d characters: %q", len(name), name)
		}
		for _, r := range name {
			if !((r >= 'a' && r <= 'z') || (r >= '2' && r <= '7')) {
				t.Fatalf("%q contains %q, which a Kubernetes name may not", name, r)
			}
		}
		seen[name] = true
	}
	if len(seen) < 190 {
		t.Fatalf("200 names produced %d distinct values", len(seen))
	}

	// The bounds exist so a caller cannot ask for something useless. This is
	// how the one-off command code got it wrong the first time, by calling
	// RandomToken with a number it refuses.
	if _, err := RandomName(2); err == nil {
		t.Error("a two-character name was accepted")
	}
	if _, err := RandomName(64); err == nil {
		t.Error("a sixty-four character name was accepted")
	}
}
