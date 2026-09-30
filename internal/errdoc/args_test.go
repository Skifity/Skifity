package errdoc

import (
	"slices"
	"testing"
)

// A Problem carries the values in its sentences, not only the sentences.
//
// The panel shows a sentence from its own locale, and a Russian sentence needs
// the hostname the English one has in it. Each value is rendered by the verb
// that was going to print it, so %q keeps its quotes and %d a number stays a
// number.
func TestAProblemCarriesTheValuesInItsSentences(t *testing.T) {
	p := New("domain.taken", "That hostname is in use").
		WithCause("%s already routes to %q.", "blog.example.com", "shop").
		WithImpact("Nothing was changed.").
		WithFix("Free it, or pick another. %d apps use this project.", 3)

	if want := []string{"blog.example.com", `"shop"`}; !slices.Equal(p.Args.Cause, want) {
		t.Errorf("cause args are %q, want %q", p.Args.Cause, want)
	}
	if len(p.Args.Impact) != 0 {
		t.Errorf("a sentence with no values carries %q", p.Args.Impact)
	}
	if want := []string{"3"}; !slices.Equal(p.Args.Fix, want) {
		t.Errorf("fix args are %q, want %q", p.Args.Fix, want)
	}

	// The English is unchanged: it is the fallback, and what the CLI prints.
	if p.Cause != `blog.example.com already routes to "shop".` {
		t.Errorf("the English cause changed: %q", p.Cause)
	}
}

// A literal per cent consumes no argument, so it must not shift the rest along.
func TestAPerCentSignDoesNotEatAnArgument(t *testing.T) {
	p := New("quota.full", "Out of room").WithCause("The disk is 100%% full on %s.", "node-1")
	if want := []string{"node-1"}; !slices.Equal(p.Args.Cause, want) {
		t.Errorf("cause args are %q, want %q", p.Args.Cause, want)
	}
	if p.Cause != "The disk is 100% full on node-1." {
		t.Errorf("the English cause is %q", p.Cause)
	}
}

// A width or precision between the per cent and the letter is part of the verb.
func TestAVerbWithFlagsIsReadWhole(t *testing.T) {
	p := New("x", "x").WithCause("%-10s and %+d", "left", 5)
	if want := []string{"left      ", "+5"}; !slices.Equal(p.Args.Cause, want) {
		t.Errorf("cause args are %q, want %q", p.Args.Cause, want)
	}
}
