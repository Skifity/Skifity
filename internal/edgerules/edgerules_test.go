package edgerules

import (
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
)

func ip(s string) netip.Addr { return netip.MustParseAddr(s) }

func test(field Field, op Operator, values ...string) Expr {
	return Expr{Test: &Test{Field: field, Op: op, Values: values}}
}

// The shape people come here for: "allow my office and my country, block
// everything else", written as one rule with an OR inside it.
func TestAndOrTogether(t *testing.T) {
	set := RuleSet{
		Default: ActionBlock,
		Rules: []Rule{{
			ID: "r1", Name: "The office, or anybody in Indonesia on the public site",
			Action: ActionAllow, Enabled: true,
			Expr: Expr{Any: []Expr{
				test(FieldIP, OpIn, "203.0.113.0/24"),
				{All: []Expr{
					test(FieldCountry, OpIn, "ID"),
					{Not: &Expr{Test: &Test{
						Field: FieldPath, Op: OpStartsWith, Values: []string{"/admin"},
					}}},
				}},
			}},
		}},
	}
	if err := set.Validate(); err != nil {
		t.Fatalf("a rule set the interface can build was refused: %v", err)
	}

	cases := []struct {
		name string
		req  Request
		want Action
	}{
		{"the office, anywhere", Request{IP: ip("203.0.113.7"), Country: "SG", Path: "/admin"}, ActionAllow},
		{"Indonesia, public page", Request{IP: ip("10.20.30.40"), Country: "ID", Path: "/shop"}, ActionAllow},
		{"Indonesia, admin page", Request{IP: ip("10.20.30.40"), Country: "ID", Path: "/admin/users"}, ActionBlock},
		{"elsewhere", Request{IP: ip("10.20.30.40"), Country: "SG", Path: "/shop"}, ActionBlock},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := set.Evaluate(tc.req); got.Action != tc.want {
				t.Errorf("got %q (rule %q), want %q", got.Action, got.RuleName, tc.want)
			}
		})
	}
}

// The first matching rule decides, which is the order they are shown in.
func TestTheFirstMatchingRuleDecides(t *testing.T) {
	set := RuleSet{
		Default: ActionAllow,
		Rules: []Rule{
			{ID: "1", Name: "let the office in", Action: ActionAllow, Enabled: true,
				Expr: test(FieldIP, OpIn, "203.0.113.7")},
			{ID: "2", Name: "block that whole range", Action: ActionBlock, Enabled: true,
				Expr: test(FieldIP, OpIn, "203.0.113.0/24")},
		},
	}
	got := set.Evaluate(Request{IP: ip("203.0.113.7")})
	if got.Action != ActionAllow || got.RuleID != "1" {
		t.Errorf("rule %q decided %q; the earlier rule should have won", got.RuleID, got.Action)
	}
	if got := set.Evaluate(Request{IP: ip("203.0.113.8")}); got.Action != ActionBlock {
		t.Errorf("an address only the second rule covers got %q", got.Action)
	}
	// A rule switched off decides nothing.
	set.Rules[0].Enabled = false
	if got := set.Evaluate(Request{IP: ip("203.0.113.7")}); got.Action != ActionBlock {
		t.Errorf("a disabled rule still decided: %q", got.Action)
	}
}

// Unknown is the third answer, and the reason it exists.
//
// A block rule on a country must not silently stop blocking when the country is
// not known, and an allow rule must not silently block everybody. Neither is
// what a rule that cannot be evaluated should do, so it does not decide at all.
func TestAFactNobodyKnowsDoesNotDecide(t *testing.T) {
	set := RuleSet{
		Default: ActionAllow,
		Rules: []Rule{{
			ID: "1", Name: "block Antarctica", Action: ActionBlock, Enabled: true,
			Expr: test(FieldCountry, OpIn, "AQ"),
		}},
	}
	// No country known: the rule is skipped and says so.
	got := set.Evaluate(Request{IP: ip("203.0.113.7")})
	if got.Action != ActionAllow {
		t.Errorf("a rule that could not be evaluated decided %q", got.Action)
	}
	if len(got.Skipped) != 1 || got.Skipped[0] != "block Antarctica" {
		t.Errorf("the skip was not reported: %+v", got.Skipped)
	}
	// With the country known it decides normally.
	if got := set.Evaluate(Request{IP: ip("203.0.113.7"), Country: "AQ"}); got.Action != ActionBlock {
		t.Errorf("a known country was not blocked: %q", got.Action)
	}
}

// Three-valued logic, at the two places it is easy to get wrong.
func TestUnknownPropagatesLikeItDoesInSQL(t *testing.T) {
	unknown := test(FieldCountry, OpIn, "ID") // nothing knows the country
	yes := test(FieldPath, OpStartsWith, "/")
	no := test(FieldPath, OpStartsWith, "/nowhere")
	req := Request{IP: ip("203.0.113.7"), Path: "/shop"}

	cases := []struct {
		name string
		expr Expr
		want truth
	}{
		{"one false makes an AND false however many unknowns", Expr{All: []Expr{unknown, no}}, isFalse},
		{"an AND of true and unknown is unknown", Expr{All: []Expr{unknown, yes}}, isUnknown},
		{"one true makes an OR true however many unknowns", Expr{Any: []Expr{unknown, yes}}, isTrue},
		{"an OR of false and unknown is unknown", Expr{Any: []Expr{unknown, no}}, isUnknown},
		{"NOT of unknown stays unknown", Expr{Not: &unknown}, isUnknown},
		{"NOT of true is false", Expr{Not: &yes}, isFalse},
		{"an empty group matches everything", Expr{}, isTrue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.expr.evaluate(req); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// Addresses: CIDRs, single addresses, IPv6, and the IPv4-in-IPv6 form a proxy
// hands over, which must match a rule written the ordinary way.
func TestAddressMatching(t *testing.T) {
	cases := []struct {
		addr   string
		values []string
		want   truth
	}{
		{"203.0.113.7", []string{"203.0.113.0/24"}, isTrue},
		{"203.0.113.7", []string{"203.0.113.7"}, isTrue},
		{"203.0.114.7", []string{"203.0.113.0/24"}, isFalse},
		{"::ffff:203.0.113.7", []string{"203.0.113.0/24"}, isTrue},
		{"2001:db8::1", []string{"2001:db8::/32"}, isTrue},
		{"2001:db8::1", []string{"203.0.113.0/24"}, isFalse},
		{"203.0.113.7", []string{"2001:db8::/32"}, isFalse},
		// A value nobody could parse must not match, and must not match
		// everything either.
		{"203.0.113.7", []string{"not an address"}, isFalse},
	}
	for _, tc := range cases {
		if got := ipMatches(ip(tc.addr), tc.values); got != tc.want {
			t.Errorf("%s in %v = %v, want %v", tc.addr, tc.values, got, tc.want)
		}
	}
}

// An ASN is written both ways and a country in either case; a rule must work
// whichever the person typed.
func TestASNAndCountryAreForgivingAboutHowTheyAreWritten(t *testing.T) {
	req := Request{IP: ip("203.0.113.7"), ASN: 13335, Country: "id"}
	for _, value := range []string{"13335", "AS13335", "as13335"} {
		expr := test(FieldASN, OpIn, value)
		if got := expr.evaluate(req); got != isTrue {
			t.Errorf("AS13335 did not match a rule written %q", value)
		}
	}
	for _, value := range []string{"ID", "id", " Id "} {
		expr := test(FieldCountry, OpIn, value)
		if got := expr.evaluate(req); got != isTrue {
			t.Errorf("a visitor in ID did not match a rule written %q", value)
		}
	}
	// not_in is the negation, and must stay unknown when the fact is unknown.
	if got := test(FieldASN, OpNotIn, "13335").evaluate(req); got != isFalse {
		t.Errorf("not_in on the matching ASN = %v, want false", got)
	}
	if got := test(FieldASN, OpNotIn, "13335").evaluate(Request{IP: ip("203.0.113.7")}); got != isUnknown {
		t.Errorf("not_in with no ASN known = %v, want unknown", got)
	}
}

// Validation exists to stop a rule that looks configured and never matches.
func TestValidateRefusesRulesThatWouldNeverWork(t *testing.T) {
	cases := []struct {
		name string
		set  RuleSet
	}{
		{"no name", RuleSet{Rules: []Rule{{Action: ActionBlock, Expr: test(FieldPath, OpContains, "x")}}}},
		{"no action", RuleSet{Rules: []Rule{{Name: "n", Expr: test(FieldPath, OpContains, "x")}}}},
		{"an address that is not one", RuleSet{Rules: []Rule{{Name: "n", Action: ActionBlock,
			Expr: test(FieldIP, OpIn, "203.0.113.999")}}}},
		{"a country that is not one", RuleSet{Rules: []Rule{{Name: "n", Action: ActionBlock,
			Expr: test(FieldCountry, OpIn, "Indonesia")}}}},
		{"an ASN that is not one", RuleSet{Rules: []Rule{{Name: "n", Action: ActionBlock,
			Expr: test(FieldASN, OpIn, "Cloudflare")}}}},
		{"a pattern that does not compile", RuleSet{Rules: []Rule{{Name: "n", Action: ActionBlock,
			Expr: test(FieldPath, OpMatches, "([a-z")}}}},
		{"contains on an address, which would never match", RuleSet{Rules: []Rule{{Name: "n", Action: ActionBlock,
			Expr: test(FieldIP, OpContains, "203.0.113")}}}},
		{"a header test with no header named", RuleSet{Rules: []Rule{{Name: "n", Action: ActionBlock,
			Expr: test(FieldHeader, OpContains, "x")}}}},
		{"a field this build does not have", RuleSet{Rules: []Rule{{Name: "n", Action: ActionBlock,
			Expr: test(Field("ip.reputation"), OpIn, "bad")}}}},
		{"nothing to compare against", RuleSet{Rules: []Rule{{Name: "n", Action: ActionBlock,
			Expr: test(FieldPath, OpContains)}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.set.Validate(); err == nil {
				t.Error("accepted")
			}
		})
	}

	good := RuleSet{Default: ActionAllow, Rules: []Rule{{
		Name: "block a bad bot", Action: ActionBlock, Enabled: true,
		Expr: Expr{All: []Expr{
			test(FieldUserAgent, OpContains, "badbot"),
			{Test: &Test{Field: FieldHeader, Key: "X-Api-Key", Op: OpNotIn, Values: []string{"secret"}}},
		}},
	}}}
	if err := good.Validate(); err != nil {
		t.Errorf("a reasonable rule set was refused: %v", err)
	}
}

// The panel asks what a rule set needs before saving it, so that a rule about a
// country is refused while somebody is looking at the form rather than becoming
// a rule that is skipped on every request for ever.
func TestNeedsReportsTheDatabasesARuleSetCannotWorkWithout(t *testing.T) {
	set := RuleSet{Rules: []Rule{{
		Name: "n", Action: ActionBlock, Enabled: true,
		Expr: Expr{Any: []Expr{
			test(FieldIP, OpIn, "203.0.113.0/24"),
			{Not: &Expr{Test: &Test{Field: FieldASN, Op: OpIn, Values: []string{"13335"}}}},
		}},
	}}}
	country, asn := set.Needs()
	if country {
		t.Error("a set with no country test asked for the country database")
	}
	if !asn {
		t.Error("a set testing a network did not ask for the network database")
	}

	none := RuleSet{Rules: []Rule{{Name: "n", Action: ActionBlock, Expr: test(FieldPath, OpContains, "/x")}}}
	if c, a := none.Needs(); c || a {
		t.Error("a set of ordinary HTTP tests asked for a geo database")
	}
}

// A group whose conditions were all deleted must not become a rule that
// matches everything.
//
// It is what the interface produces when somebody removes the last condition
// from an "all of" group, and reading it as "no conditions, so true" turned a
// half-finished block rule into everybody locked out of the app — or out of
// this panel, which the same engine protects.
func TestAGroupWithNothingInItIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		expr Expr
	}{
		{"an empty all-of", Expr{All: []Expr{}}},
		{"an empty any-of", Expr{Any: []Expr{}}},
		{"an empty group inside another", Expr{All: []Expr{{Any: []Expr{}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set := RuleSet{Default: ActionAllow, Rules: []Rule{
				{ID: "r1", Name: "half-finished", Action: ActionBlock, Enabled: true, Expr: tc.expr},
			}}
			err := set.Validate()
			if err == nil {
				t.Fatal("it was accepted, so a half-finished rule could be saved")
			}
			if !strings.Contains(err.Error(), "nothing in it") {
				t.Errorf("the message does not say what is wrong: %v", err)
			}
		})
	}
}

// The backstop, for a set stored before that check existed: an empty group
// does not decide, and the skip is named where somebody can see it.
func TestAnEmptyGroupDoesNotDecide(t *testing.T) {
	set := RuleSet{Default: ActionAllow, Rules: []Rule{
		{ID: "r1", Name: "half-finished", Action: ActionBlock, Enabled: true, Expr: Expr{All: []Expr{}}},
	}}
	decision := set.Evaluate(Request{Host: "example.test", Path: "/"})
	if decision.Action != ActionAllow {
		t.Fatalf("a rule with no conditions in its group blocked the request")
	}
	if decision.RuleID != "" {
		t.Errorf("it decided, as rule %q", decision.RuleID)
	}
	if len(decision.Skipped) != 1 || decision.Skipped[0] != "half-finished" {
		t.Errorf("the skip was not reported by name: %v", decision.Skipped)
	}
}

// The deliberate shape stays: a rule with no conditions at all applies to
// everybody, which is how "block everyone except" is written.
func TestARuleWithNoConditionsAtAllStillMatchesEverything(t *testing.T) {
	set := RuleSet{Default: ActionAllow, Rules: []Rule{
		{ID: "allow-office", Name: "the office", Action: ActionAllow, Enabled: true,
			Expr: Expr{Test: &Test{Field: FieldIP, Op: OpIn, Values: []string{"203.0.113.0/24"}}}},
		{ID: "block-rest", Name: "everybody else", Action: ActionBlock, Enabled: true, Expr: Expr{}},
	}}
	if err := set.Validate(); err != nil {
		t.Fatalf("the block-everyone-except shape was refused: %v", err)
	}
	if got := set.Evaluate(Request{IP: netip.MustParseAddr("203.0.113.7")}); got.Action != ActionAllow {
		t.Errorf("the office was not allowed: %+v", got)
	}
	if got := set.Evaluate(Request{IP: netip.MustParseAddr("198.51.100.4")}); got.Action != ActionBlock {
		t.Errorf("everybody else was not blocked: %+v", got)
	}
}

// An empty group surviving a round trip through the stored JSON is the path
// that actually happens: the interface sends {"all":[]}.
func TestAnEmptyGroupIsRefusedWhenItArrivesAsJSON(t *testing.T) {
	var set RuleSet
	if err := json.Unmarshal([]byte(
		`{"default":"allow","rules":[{"id":"r1","name":"half-finished","action":"block","enabled":true,"expr":{"all":[]}}]}`,
	), &set); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if err := set.Validate(); err == nil {
		t.Fatal("the shape the interface sends when the last condition is deleted was accepted")
	}
}
