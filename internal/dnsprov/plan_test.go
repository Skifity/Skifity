package dnsprov

import (
	"errors"
	"testing"
)

const marker = "managed by Skifity 0123456789ab"

// The rules for one name, one at a time. Every case is a name with some
// records on it, some of them in the panel's books, and what the panel wants
// there.

func a(content string) Want    { return Want{Type: "A", Content: content} }
func aaaa(content string) Want { return Want{Type: "AAAA", Content: content} }

func TestANameWithNothingOnItGetsTheRecordsWanted(t *testing.T) {
	plan, err := Decide("blog.example.com", nil, nil, []Want{a("203.0.113.10"), aaaa("2001:db8::10")}, marker, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Create) != 2 || len(plan.Update)+len(plan.Delete)+len(plan.Keep)+len(plan.Elsewhere) != 0 {
		t.Errorf("an empty name was planned as %+v", plan)
	}
}

// Somebody else's record that already says what is wanted is theirs, and it
// stays theirs: nothing is created beside it, and nothing is taken over.
func TestARecordSomebodyElseMadeThatPointsHereIsLeftToThem(t *testing.T) {
	existing := []Record{{ID: "r1", Type: "A", Name: "blog.example.com", Content: "203.0.113.10"}}
	plan, err := Decide("blog.example.com", existing, nil, []Want{a("203.0.113.10")}, marker, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Elsewhere) != 1 || len(plan.Create) != 0 || len(plan.Update) != 0 || len(plan.Adopt) != 0 {
		t.Errorf("somebody else's record was not left alone: %+v", plan)
	}
}

// And one that says something else is a conflict that names it. Nothing is
// planned at all: not the A, not the AAAA beside it.
func TestARecordSomebodyElseMadeThatPointsElsewhereIsAConflict(t *testing.T) {
	existing := []Record{{ID: "r1", Type: "A", Name: "blog.example.com", Content: "198.51.100.7"}}
	plan, err := Decide("blog.example.com", existing, nil, []Want{a("203.0.113.10"), aaaa("2001:db8::10")}, marker, true)
	var conflict *Conflict
	if !errors.As(err, &conflict) {
		t.Fatalf("a record pointing elsewhere was planned over: %+v", plan)
	}
	if conflict.Found.Content != "198.51.100.7" || conflict.Reason != "value" {
		t.Errorf("the conflict names %+v", conflict)
	}
	if len(plan.Create)+len(plan.Update)+len(plan.Delete) != 0 {
		t.Errorf("a plan with a conflict still does something: %+v", plan)
	}
}

// A CNAME shares its name with nothing, in either direction.
func TestACNAMEConflictsWithAnythingAtItsName(t *testing.T) {
	cname := []Record{{ID: "r1", Type: "CNAME", Name: "shop.example.com", Content: "shops.myshopify.com"}}
	if _, err := Decide("shop.example.com", cname, nil, []Want{a("203.0.113.10")}, marker, true); !isConflict(err, "cname") {
		t.Errorf("an A was planned beside somebody's CNAME: %v", err)
	}
	txt := []Record{{ID: "r2", Type: "TXT", Name: "shop.example.com", Content: "v=spf1 -all"}}
	want := []Want{{Type: "CNAME", Content: "abc.cfargotunnel.com", Proxied: true}}
	if _, err := Decide("shop.example.com", txt, nil, want, marker, true); !isConflict(err, "cname") {
		t.Errorf("a CNAME was planned beside somebody's TXT record: %v", err)
	}
	// A TXT record beside an A is no business of the panel's.
	if plan, err := Decide("shop.example.com", txt, nil, []Want{a("203.0.113.10")}, marker, true); err != nil || len(plan.Create) != 1 {
		t.Errorf("a TXT record stopped an A: %+v, %v", plan, err)
	}
}

// An old AAAA left beside the A is the commonest way a domain half works.
func TestAnAddressOfATypeNotWantedIsAConflict(t *testing.T) {
	existing := []Record{{ID: "r1", Type: "AAAA", Name: "blog.example.com", Content: "2001:db8::99"}}
	_, err := Decide("blog.example.com", existing, nil, []Want{a("203.0.113.10")}, marker, true)
	if !isConflict(err, "extra") {
		t.Errorf("an AAAA pointing elsewhere was left beside a new A: %v", err)
	}
}

// The panel's own record, still as it left it, is kept — or pointed at the
// new address when the address changed.
func TestThePanelsOwnRecordIsKeptOrUpdated(t *testing.T) {
	existing := []Record{{ID: "r1", Type: "A", Name: "blog.example.com", Content: "203.0.113.10", Note: marker}}
	owned := []Owned{{Row: "dnsr_1", RemoteID: "r1", Type: "A", Content: "203.0.113.10"}}

	plan, err := Decide("blog.example.com", existing, owned, []Want{a("203.0.113.10")}, marker, true)
	if err != nil || len(plan.Keep) != 1 || len(plan.Create)+len(plan.Update) != 0 {
		t.Errorf("an unchanged record was not kept: %+v, %v", plan, err)
	}

	plan, err = Decide("blog.example.com", existing, owned, []Want{a("198.51.100.20")}, marker, true)
	if err != nil || len(plan.Update) != 1 || plan.Update[0].To != "198.51.100.20" || plan.Update[0].Record.ID != "r1" {
		t.Errorf("a new address did not update the panel's record: %+v, %v", plan, err)
	}
}

// A record the panel made and somebody changed is theirs now: forgotten, and
// what it says is a conflict rather than something to put back.
func TestARecordSomebodyChangedIsNotThePanelsAnyMore(t *testing.T) {
	owned := []Owned{{Row: "dnsr_1", RemoteID: "r1", Type: "A", Content: "203.0.113.10"}}

	edited := []Record{{ID: "r1", Type: "A", Name: "blog.example.com", Content: "198.51.100.7", Note: marker}}
	plan, err := Decide("blog.example.com", edited, owned, []Want{a("203.0.113.10")}, marker, true)
	if !isConflict(err, "value") || len(plan.Changed) != 1 || len(plan.Forget) != 1 {
		t.Errorf("a changed record was treated as the panel's: %+v, %v", plan, err)
	}

	// The note taken off is a change too, where the provider keeps notes.
	renoted := []Record{{ID: "r1", Type: "A", Name: "blog.example.com", Content: "203.0.113.10", Note: "ours now"}}
	plan, err = Decide("blog.example.com", renoted, owned, []Want{a("198.51.100.20")}, marker, true)
	if err == nil || len(plan.Update) != 0 {
		t.Errorf("a record somebody re-labelled was updated: %+v", plan)
	}

	// A set that grew a value the panel did not put there.
	grown := []Record{
		{ID: "www/A", Type: "A", Name: "blog.example.com", Content: "203.0.113.10"},
		{ID: "www/A", Type: "A", Name: "blog.example.com", Content: "198.51.100.7"},
	}
	owned[0].RemoteID = "www/A"
	plan, err = Decide("blog.example.com", grown, owned, []Want{a("203.0.113.10")}, marker, false)
	if err == nil || len(plan.Changed) != 1 {
		t.Errorf("a set with somebody's value in it was treated as the panel's: %+v, %v", plan, err)
	}
}

// A record that is not at the provider any more is forgotten and made again.
func TestAGoneRecordIsForgottenAndMadeAgain(t *testing.T) {
	owned := []Owned{{Row: "dnsr_1", RemoteID: "r1", Type: "A", Content: "203.0.113.10"}}
	plan, err := Decide("blog.example.com", nil, owned, []Want{a("203.0.113.10")}, marker, true)
	if err != nil || len(plan.Forget) != 1 || len(plan.Create) != 1 {
		t.Errorf("a vanished record was not forgotten and remade: %+v, %v", plan, err)
	}
}

// The panel's records of a type no longer wanted go: the A when a tunnel's
// CNAME takes over, the AAAA when the IPv6 address is cleared.
func TestThePanelsRecordsNoLongerWantedAreDeleted(t *testing.T) {
	existing := []Record{
		{ID: "r1", Type: "A", Name: "blog.example.com", Content: "203.0.113.10", Note: marker},
		{ID: "r2", Type: "AAAA", Name: "blog.example.com", Content: "2001:db8::10", Note: marker},
	}
	owned := []Owned{
		{Row: "dnsr_1", RemoteID: "r1", Type: "A", Content: "203.0.113.10"},
		{Row: "dnsr_2", RemoteID: "r2", Type: "AAAA", Content: "2001:db8::10"},
	}
	want := []Want{{Type: "CNAME", Content: "abc.cfargotunnel.com", Proxied: true}}
	plan, err := Decide("blog.example.com", existing, owned, want, marker, true)
	if err != nil || len(plan.Delete) != 2 || len(plan.Create) != 1 {
		t.Errorf("switching to a CNAME was planned as %+v, %v", plan, err)
	}
}

// A record with this panel's own note the books have lost — a panel restored
// from an older backup — is taken back rather than duplicated. Another
// panel's note is somebody else's.
func TestARecordWithThisPanelsNoteIsTakenBack(t *testing.T) {
	existing := []Record{{ID: "r1", Type: "A", Name: "blog.example.com", Content: "203.0.113.10", Note: marker}}
	plan, err := Decide("blog.example.com", existing, nil, []Want{a("203.0.113.10")}, marker, true)
	if err != nil || len(plan.Adopt) != 1 || len(plan.Create) != 0 {
		t.Errorf("the panel's own record was not taken back: %+v, %v", plan, err)
	}
	existing[0].Note = "managed by Skifity ffffffffffff"
	plan, err = Decide("blog.example.com", existing, nil, []Want{a("203.0.113.10")}, marker, true)
	if err != nil || len(plan.Adopt) != 0 || len(plan.Elsewhere) != 1 {
		t.Errorf("another panel's record was taken: %+v, %v", plan, err)
	}
	// Without notes there is nothing to know it by.
	existing[0].Note = marker
	plan, _ = Decide("blog.example.com", existing, nil, []Want{a("203.0.113.10")}, marker, false)
	if len(plan.Adopt) != 0 {
		t.Errorf("a provider without notes had a record adopted: %+v", plan)
	}
}

// Addresses and names compare the way DNS does.
func TestSpellingsCompareAsDNSDoes(t *testing.T) {
	existing := []Record{{ID: "r1", Type: "CNAME", Name: "Blog.Example.com.", Content: "LB.Example.net."}}
	plan, err := Decide("blog.example.com", existing, nil, []Want{{Type: "CNAME", Content: "lb.example.net"}}, marker, true)
	if err != nil || len(plan.Elsewhere) != 1 {
		t.Errorf("the same name in another spelling was a conflict: %+v, %v", plan, err)
	}
	existing = []Record{{ID: "r1", Type: "AAAA", Name: "blog.example.com", Content: "2001:DB8:0:0::10"}}
	plan, err = Decide("blog.example.com", existing, nil, []Want{aaaa("2001:db8::10")}, marker, true)
	if err != nil || len(plan.Elsewhere) != 1 {
		t.Errorf("the same address in another spelling was a conflict: %+v, %v", plan, err)
	}
}

func TestTheZoneIsTheLongestThatContainsTheHostname(t *testing.T) {
	zones := []string{"example.co.uk", "shop.example.co.uk", "co.uk", "myexample.co.uk", "example.com"}
	cases := map[string]int{
		"www.shop.example.co.uk": 1,
		"shop.example.co.uk":     1,
		"blog.example.co.uk":     0,
		"example.co.uk":          0,
		"www.myexample.co.uk":    3,
		"notexample.com":         -1,
		"EXAMPLE.COM.":           4,
		"example.org":            -1,
		"other.uk":               -1,
	}
	for hostname, want := range cases {
		if got := MatchZone(hostname, zones); got != want {
			t.Errorf("%s matched zone %d, want %d", hostname, got, want)
		}
	}
	// Two connections with the same zone: the first.
	if got := MatchZone("a.example.com", []string{"example.com", "example.com"}); got != 0 {
		t.Errorf("a zone reached twice went to %d", got)
	}
}

func TestAWildcardCoversOneLabel(t *testing.T) {
	cases := map[[2]string]string{
		{"web-production.apps.example.com", "example.com"}: "*.apps.example.com",
		{"www.example.com", "example.com"}:                 "*.example.com",
		{"example.com", "example.com"}:                     "",
		{"www.example.org", "example.com"}:                 "",
	}
	for in, want := range cases {
		if got := Wildcard(in[0], in[1]); got != want {
			t.Errorf("Wildcard(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
	records := []Record{{Type: "A", Content: "203.0.113.10"}}
	if !Covers(records, []Want{a("203.0.113.10")}) || Covers(records, []Want{a("203.0.113.10"), aaaa("2001:db8::10")}) ||
		Covers(records, []Want{a("198.51.100.7")}) {
		t.Error("Covers does not compare every wanted record")
	}
}

func isConflict(err error, reason string) bool {
	var conflict *Conflict
	return errors.As(err, &conflict) && conflict.Reason == reason
}
