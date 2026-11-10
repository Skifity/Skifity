package notify

import (
	"os"
	"regexp"
	"testing"
)

// TestUntestedIsWhatTheCrosscheckDoesNotDeliverTo: the panel marks a kind
// "not tested end to end" from the untested set, and that is only honest while
// the set is exactly the built-in kinds test/cluster/verify.sh makes no channel
// of. A kind the crosscheck starts delivering to fails here until its mark is
// taken off, and one it stops delivering to fails until the mark is put on.
func TestUntestedIsWhatTheCrosscheckDoesNotDeliverTo(t *testing.T) {
	script, err := os.ReadFile("../../test/cluster/verify.sh")
	if err != nil {
		t.Fatal(err)
	}
	crosschecked := map[string]bool{}
	for _, match := range regexp.MustCompile(`"kind":\s*"([a-z]+)"`).FindAllStringSubmatch(string(script), -1) {
		if IsBuiltIn(match[1]) {
			crosschecked[match[1]] = true
		}
	}
	if len(crosschecked) == 0 {
		t.Fatal("verify.sh makes no notification channel this test can find; the pattern no longer matches how it makes one")
	}

	for _, kind := range BuiltInKinds() {
		switch {
		case kind.Untested && crosschecked[kind.Kind]:
			t.Errorf("%s is marked untested, and verify.sh delivers to it", kind.Kind)
		case !kind.Untested && !crosschecked[kind.Kind]:
			t.Errorf("%s is not marked untested, and verify.sh never delivers to it", kind.Kind)
		}
	}
	for kind := range untested {
		if !IsBuiltIn(kind) {
			t.Errorf("%s is marked untested and is not a built-in kind", kind)
		}
	}
}
