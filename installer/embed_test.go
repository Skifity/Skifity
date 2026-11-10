package installer

import (
	"os/exec"
	"regexp"
	"testing"
)

// TestTheTestedK3sVersionIsReadFromTheInstaller: the panel installs the first
// server of a new cluster at the k3s version install.sh pins. A line Go could
// not read would quietly send that server to whatever k3s's stable channel
// says that day, which is what pinning it was for.
func TestTheTestedK3sVersionIsReadFromTheInstaller(t *testing.T) {
	if !regexp.MustCompile(`^v\d+\.\d+\.\d+\+k3s\d+$`).MatchString(K3sVersion) {
		t.Fatalf("K3S_VERSION in install.sh reads as %q; it has to be a k3s release such as v1.37.2+k3s1", K3sVersion)
	}

	// The shell's own reading of the same line, so a value written in a way
	// the pattern does not see (a default, an expansion) cannot pass here and
	// mean something else to sh.
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh to read install.sh with")
	}
	out, err := exec.Command(sh, "-c", `SKIFITY_INSTALLER_LIB=1 . ./install.sh; printf '%s' "$K3S_VERSION"`).Output()
	if err != nil {
		t.Fatalf("source install.sh: %v", err)
	}
	if string(out) != K3sVersion {
		t.Fatalf("sh reads K3S_VERSION as %q and the panel as %q", out, K3sVersion)
	}
}

func TestAK3sVersionLineThatIsNotPlainReadsAsNothing(t *testing.T) {
	for script, want := range map[string]string{
		"K3S_VERSION=\"v1.37.2+k3s1\"\n":                                 "v1.37.2+k3s1",
		"# K3S_VERSION=\"v1.30.0+k3s1\"\nK3S_VERSION=\"v1.37.2+k3s1\"\n": "v1.37.2+k3s1",
		"K3S_VERSION=\"${SKIFITY_K3S_VERSION:-v1.37.2+k3s1}\"\n":         "",
		"\tK3S_VERSION=\"v1.37.2+k3s1\"\n":                               "",
		"":                                                               "",
	} {
		if got := k3sVersion(script); got != want {
			t.Errorf("k3sVersion(%q) = %q, want %q", script, got, want)
		}
	}
}
