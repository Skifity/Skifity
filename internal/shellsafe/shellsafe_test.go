package shellsafe

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// runEcho puts a value through a generated script the way the panel does, and
// returns what the shell saw. A real shell, because the whole point is what a
// shell does with the result rather than what the quoting looks like.
func runEcho(t *testing.T, script string) string {
	t.Helper()
	out, err := exec.Command("sh", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("sh: %v\n%s\n--- script ---\n%s", err, out, script)
	}
	return strings.TrimRight(string(out), "\n")
}

func TestAValueCannotBecomeACommand(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell available")
	}

	// Everything somebody could put in a field that ends up in a script.
	values := []string{
		`1.2.3.4$(id -u)`,
		"1.2.3.4`id -u`",
		`; touch /tmp/skifity-should-not-exist`,
		`| cat /etc/passwd`,
		`$HOME`,
		`${HOME}`,
		`a'b`,
		`a"b`,
		`a\b`,
		`a b	c`,
		`--flag=value`,
		``,
		`$(echo nested $(echo deeper))`,
		"line\nbreak",
	}
	for _, value := range values {
		script := fmt.Sprintf("V=%s\nprintf '%%s' \"$V\"\n", Quote(value))
		if got := runEcho(t, script); got != value {
			t.Errorf("the shell read %q as %q — it was not passed through unchanged", value, got)
		}
	}
}

func TestTheTrapThisReplaces(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell available")
	}

	// %q looks like shell quoting. This is what it actually does, and is why
	// every generated script in this repository had to be changed.
	danger := `1.2.3.4$(id -u)`

	viaPercentQ := fmt.Sprintf("V=%q\nprintf '%%s' \"$V\"\n", danger)
	if got := runEcho(t, viaPercentQ); got == danger {
		t.Fatal("Go now quotes for a shell; this test and the package it guards can go")
	}

	viaQuote := fmt.Sprintf("V=%s\nprintf '%%s' \"$V\"\n", Quote(danger))
	if got := runEcho(t, viaQuote); got != danger {
		t.Fatalf("Quote let the shell change the value: %q", got)
	}
}

func TestQuoteAlwaysQuotes(t *testing.T) {
	// An unquoted empty word disappears, which changes how many arguments a
	// command is given — a different bug from injection and just as real.
	if Quote("") != "''" {
		t.Fatalf("Quote(\"\") = %s, want ''", Quote(""))
	}
	if !strings.HasPrefix(Quote("plain"), "'") {
		t.Errorf("Quote(%q) = %s, want it quoted", "plain", Quote("plain"))
	}
}

func TestJoinQuotesEveryWord(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell available")
	}
	// Three arguments, one of which would otherwise be two and one of which
	// would otherwise run a command.
	script := fmt.Sprintf("set -- %s\nprintf '%%s\\n' \"$#\"\n",
		Join("first", "two words", "$(id -u)"))
	if got := runEcho(t, script); got != "3" {
		t.Fatalf("the shell saw %s arguments, want 3", got)
	}
}
