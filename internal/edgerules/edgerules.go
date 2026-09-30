// Package edgerules decides whether a request is allowed to reach an app.
//
// The shape is the one people already know from Cloudflare: an ordered list of
// rules, each an expression over the request, each with an action. The first
// rule whose expression is true decides, and a request no rule matched gets the
// set's default action.
//
// An expression is a tree rather than a string. Cloudflare writes theirs as a
// small language and then has to parse it; the panel does not need one, because
// the interface builds the tree directly — "match all of these" and "match any
// of these" are groups on a form, not syntax somebody has to learn. The tree is
// also what makes the JSON in the database reviewable by eye.
//
// # Unknown is a third answer
//
// A rule on the visitor's country is worthless if nothing knows the country,
// and the two ways of pretending otherwise are both wrong: treating it as false
// means a block rule silently stops blocking, and treating it as true means an
// allow rule silently blocks everybody. So a test on data that is not there is
// neither true nor false but unknown, and unknown propagates the way it does in
// SQL: an AND with one false is false however many unknowns it holds, an OR
// with one true is true, and anything else touching an unknown is unknown. A
// rule that comes out unknown does not decide; it is skipped and the skip is
// reported, so the reason is visible instead of being a decision nobody made.
//
// The real fix is upstream: the panel refuses to save a rule about a country
// when no country database is configured. This is the backstop for the case
// where the database is configured and then goes away.
//
// # A group with nothing in it
//
// A rule with no conditions at all matches every request. That is deliberate,
// and it is how a set says "this action, to everybody".
//
// A group that is present and empty is not the same thing and never means it.
// It is what the interface produces when somebody deletes the last condition
// out of an "all of" group, and reading it as "no conditions, so true" turned a
// half-finished rule into one that matched every request — a block rule that
// locked everybody out of the app, or out of this panel, with nothing on the
// screen to say so. So it is refused when the set is saved, and at evaluation
// time it is unknown rather than true: it does not decide, and it is named
// among the skipped rules where somebody can see it.
package edgerules

import (
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// Action is what happens to a request a rule matched.
type Action string

const (
	// ActionAllow lets the request through and stops evaluating.
	ActionAllow Action = "allow"
	// ActionBlock answers 403 and never reaches the app.
	ActionBlock Action = "block"
)

// Valid reports whether an action is one that exists.
func (a Action) Valid() bool { return a == ActionAllow || a == ActionBlock }

// Field is one thing about a request that a test can look at.
type Field string

const (
	// FieldIP is the visitor's address. Its values are addresses or CIDRs.
	FieldIP Field = "ip.src"
	// FieldCountry is the ISO 3166-1 alpha-2 country the address is in.
	FieldCountry Field = "ip.country"
	// FieldASN is the autonomous system number, written with or without "AS".
	FieldASN Field = "ip.asn"
	// FieldHost is the hostname the request asked for.
	FieldHost Field = "http.host"
	// FieldPath is the path, without the query string.
	FieldPath Field = "http.path"
	// FieldMethod is GET, POST and the rest, compared case-insensitively.
	FieldMethod Field = "http.method"
	// FieldUserAgent is the User-Agent header.
	FieldUserAgent Field = "http.user_agent"
	// FieldHeader is any header, named by the test's Key.
	FieldHeader Field = "http.header"
)

// Fields are the fields a rule may test, in the order the interface lists them.
var Fields = []Field{
	FieldIP, FieldCountry, FieldASN,
	FieldHost, FieldPath, FieldMethod, FieldUserAgent, FieldHeader,
}

// NeedsGeoCountry and NeedsGeoASN report whether a field cannot be answered
// without a geo database.
func (f Field) NeedsGeoCountry() bool { return f == FieldCountry }

// NeedsGeoASN reports whether a field needs the autonomous system database.
func (f Field) NeedsGeoASN() bool { return f == FieldASN }

// Operator is how a field's value is compared with the test's values.
type Operator string

const (
	// OpIn is true when the field equals any value. For ip.src the values may
	// be CIDRs, and membership is what is tested.
	OpIn Operator = "in"
	// OpNotIn is the negation of OpIn.
	OpNotIn Operator = "not_in"
	// OpContains, OpStartsWith and OpEndsWith are substring tests, true when
	// any value matches. They are case-insensitive, because a rule about
	// "/admin" that misses "/Admin" is a rule that does not work.
	OpContains   Operator = "contains"
	OpStartsWith Operator = "starts_with"
	OpEndsWith   Operator = "ends_with"
	// OpMatches is a regular expression, true when any of them matches.
	//
	// Go's regexp is RE2: it has no backtracking and runs in time linear in the
	// input, so a rule cannot be written that costs a second of CPU per
	// request. That is the only reason this operator is offered at all.
	OpMatches Operator = "matches"
)

// Operators are the operators a rule may use.
var Operators = []Operator{OpIn, OpNotIn, OpContains, OpStartsWith, OpEndsWith, OpMatches}

// Test is one comparison.
type Test struct {
	Field Field `json:"field"`
	// Key names the header when Field is http.header, and is empty otherwise.
	Key    string   `json:"key,omitempty"`
	Op     Operator `json:"op"`
	Values []string `json:"values"`
}

// Expr is a condition: a group of conditions, a negation, or one test.
//
// Exactly one of the four is set. Rendering it as four optional fields rather
// than a tagged union keeps the stored JSON readable — `{"all":[...]}` says what
// it is without a type field to cross-reference.
type Expr struct {
	// All is true when every condition in it is true.
	//
	// Present and empty is refused rather than read as true; see the package
	// comment. A rule that is meant to match everything has no All, no Any, no
	// Not and no Test at all.
	All []Expr `json:"all,omitempty"`
	// Any is true when at least one condition is true. Present and empty is
	// refused, for the same reason as All.
	Any  []Expr `json:"any,omitempty"`
	Not  *Expr  `json:"not,omitempty"`
	Test *Test  `json:"test,omitempty"`
}

// Rule is one entry in a set.
type Rule struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Action  Action `json:"action"`
	Expr    Expr   `json:"expr"`
	Enabled bool   `json:"enabled"`
}

// RuleSet is what protects one app, or the panel itself.
type RuleSet struct {
	Rules []Rule `json:"rules"`
	// Default is what happens to a request no rule matched.
	//
	// Allow, always, unless the operator changes it. A rule set whose default
	// was block would mean adding a first rule locked everybody out, which is
	// not a lesson anybody should learn from their own panel.
	Default Action `json:"default"`
}

// Request is everything a rule can look at.
//
// Country and ASN are empty and zero when nothing could work them out, which is
// what makes a test on them unknown rather than false.
type Request struct {
	IP        netip.Addr
	Country   string
	ASN       uint32
	Host      string
	Path      string
	Method    string
	UserAgent string
	// Header returns a header's value, or "". Nil is treated as a request with
	// no headers rather than panicking, because a caller building a Request by
	// hand in a test should not have to supply one.
	Header func(name string) string
}

func (r Request) header(name string) string {
	if r.Header == nil {
		return ""
	}
	return r.Header(name)
}

// Decision is the answer, and why.
type Decision struct {
	Action Action
	// RuleID and RuleName are the rule that decided, empty when the set's
	// default did.
	RuleID   string
	RuleName string
	// Skipped names the rules that could not be evaluated because something
	// they test was not known. It is the difference between a rule that did not
	// match and a rule that could not be asked, and the panel shows it.
	Skipped []string
}

// truth is three-valued: true, false, or not knowable from this request.
type truth int

const (
	isFalse truth = iota
	isTrue
	isUnknown
)

// Evaluate runs a rule set against a request.
//
// Rules are tried in order and the first one that is true decides, which is the
// order they are shown in and the order somebody reading them would assume.
func (s RuleSet) Evaluate(req Request) Decision {
	decision := Decision{Action: s.Default}
	if !decision.Action.Valid() {
		decision.Action = ActionAllow
	}
	for _, rule := range s.Rules {
		if !rule.Enabled {
			continue
		}
		switch rule.Expr.evaluate(req) {
		case isTrue:
			decision.Action = rule.Action
			decision.RuleID = rule.ID
			decision.RuleName = rule.Name
			return decision
		case isUnknown:
			// Named rather than counted: "2 rules were skipped" sends somebody
			// looking, and the name is what they are looking for.
			decision.Skipped = append(decision.Skipped, rule.Name)
		}
	}
	return decision
}

func (e Expr) evaluate(req Request) truth {
	switch {
	case e.Test != nil:
		return e.Test.evaluate(req)
	case e.Not != nil:
		switch e.Not.evaluate(req) {
		case isTrue:
			return isFalse
		case isFalse:
			return isTrue
		default:
			return isUnknown
		}
	case len(e.Any) > 0:
		// One true is enough, however many unknowns are beside it.
		unknown := false
		for _, sub := range e.Any {
			switch sub.evaluate(req) {
			case isTrue:
				return isTrue
			case isUnknown:
				unknown = true
			}
		}
		if unknown {
			return isUnknown
		}
		return isFalse
	case len(e.All) > 0:
		// One false is enough, however many unknowns are beside it.
		unknown := false
		for _, sub := range e.All {
			switch sub.evaluate(req) {
			case isFalse:
				return isFalse
			case isUnknown:
				unknown = true
			}
		}
		if unknown {
			return isUnknown
		}
		return isTrue
	case e.All != nil || e.Any != nil:
		// A group that is present and holds nothing. Refused when the set is
		// saved, so reaching this means a set stored before that check
		// existed, or one written straight into the database. It does not
		// decide: "the conditions were deleted" is not "every request".
		return isUnknown
	default:
		// No conditions at all, which is how a rule says it applies to
		// everybody — the shape of "block everyone except".
		return isTrue
	}
}

func (t Test) evaluate(req Request) truth {
	switch t.Field {
	case FieldIP:
		if !req.IP.IsValid() {
			return isUnknown
		}
		return negateIf(t.Op == OpNotIn, ipMatches(req.IP, t.Values))

	case FieldCountry:
		if req.Country == "" {
			return isUnknown
		}
		return t.compare(req.Country)

	case FieldASN:
		if req.ASN == 0 {
			return isUnknown
		}
		return t.compare(strconv.FormatUint(uint64(req.ASN), 10))

	case FieldHost:
		return t.compare(req.Host)
	case FieldPath:
		return t.compare(req.Path)
	case FieldMethod:
		return t.compare(req.Method)
	case FieldUserAgent:
		return t.compare(req.UserAgent)
	case FieldHeader:
		return t.compare(req.header(t.Key))
	default:
		// A field this build does not know is not a reason to let a request
		// through, and not a reason to refuse one either.
		return isUnknown
	}
}

// compare applies the operator to one value.
func (t Test) compare(value string) truth {
	// ASN values are written "AS13335" as often as "13335", and a country is
	// written in either case. Normalising here means the rule works whichever
	// the person typed.
	lower := strings.ToLower(value)
	if t.Field == FieldASN {
		lower = strings.TrimPrefix(lower, "as")
	}

	matched := false
	for _, want := range t.Values {
		wantLower := strings.ToLower(strings.TrimSpace(want))
		if t.Field == FieldASN {
			wantLower = strings.TrimPrefix(wantLower, "as")
		}
		switch t.Op {
		case OpIn, OpNotIn:
			matched = lower == wantLower
		case OpContains:
			matched = wantLower != "" && strings.Contains(lower, wantLower)
		case OpStartsWith:
			matched = wantLower != "" && strings.HasPrefix(lower, wantLower)
		case OpEndsWith:
			matched = wantLower != "" && strings.HasSuffix(lower, wantLower)
		case OpMatches:
			// Compiled here rather than cached, because Validate has already
			// refused anything that does not compile and Go caches nothing for
			// us; if this ever shows up in a profile it is a map away.
			re, err := regexp.Compile(want)
			if err != nil {
				return isUnknown
			}
			matched = re.MatchString(value)
		default:
			return isUnknown
		}
		if matched {
			break
		}
	}
	return negateIf(t.Op == OpNotIn, boolTruth(matched))
}

// ipMatches reports whether an address is in any of the values, each of which
// is an address or a CIDR.
func ipMatches(ip netip.Addr, values []string) truth {
	// An IPv4 address that arrived as ::ffff:1.2.3.4 must match a rule written
	// as 1.2.3.0/24, which it does not until it is unmapped.
	ip = ip.Unmap()
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if strings.Contains(value, "/") {
			prefix, err := netip.ParsePrefix(value)
			if err != nil {
				continue
			}
			if prefix.Addr().Unmap().BitLen() == ip.BitLen() && prefix.Contains(ip) {
				return isTrue
			}
			continue
		}
		addr, err := netip.ParseAddr(value)
		if err != nil {
			continue
		}
		if addr.Unmap() == ip {
			return isTrue
		}
	}
	return isFalse
}

func negateIf(negate bool, t truth) truth {
	if !negate || t == isUnknown {
		return t
	}
	if t == isTrue {
		return isFalse
	}
	return isTrue
}

func boolTruth(b bool) truth {
	if b {
		return isTrue
	}
	return isFalse
}

// Needs reports which geo databases a rule set cannot work without.
//
// The panel asks before saving, so that a rule about a country is refused while
// somebody is looking at the form rather than becoming a rule that is skipped
// on every request for ever.
func (s RuleSet) Needs() (country, asn bool) {
	for _, rule := range s.Rules {
		c, a := rule.Expr.needs()
		country = country || c
		asn = asn || a
	}
	return country, asn
}

func (e Expr) needs() (country, asn bool) {
	if e.Test != nil {
		return e.Test.Field.NeedsGeoCountry(), e.Test.Field.NeedsGeoASN()
	}
	if e.Not != nil {
		return e.Not.needs()
	}
	for _, sub := range append(append([]Expr{}, e.All...), e.Any...) {
		c, a := sub.needs()
		country = country || c
		asn = asn || a
	}
	return country, asn
}

// Validate refuses a rule set that would behave in a way nobody intended.
//
// Everything here is something that produces a rule which silently never
// matches, which is the worst outcome for a security control: it looks
// configured and does nothing.
func (s RuleSet) Validate() error {
	if s.Default != "" && !s.Default.Valid() {
		return fmt.Errorf("%q is not an action; it is %q or %q", s.Default, ActionAllow, ActionBlock)
	}
	if len(s.Rules) > maxRules {
		return fmt.Errorf("a rule set holds at most %d rules", maxRules)
	}
	for i, rule := range s.Rules {
		where := fmt.Sprintf("rule %d", i+1)
		if rule.Name != "" {
			where = fmt.Sprintf("rule %q", rule.Name)
		}
		if strings.TrimSpace(rule.Name) == "" {
			return fmt.Errorf("%s has no name; a rule nobody can identify is a rule nobody will dare delete", where)
		}
		if !rule.Action.Valid() {
			return fmt.Errorf("%s has no action; it is %q or %q", where, ActionAllow, ActionBlock)
		}
		if err := rule.Expr.validate(0); err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
	}
	return nil
}

const (
	maxRules = 200
	maxDepth = 8
)

func (e Expr) validate(depth int) error {
	if depth > maxDepth {
		return fmt.Errorf("the conditions are nested more than %d deep", maxDepth)
	}
	set := 0
	for _, has := range []bool{len(e.All) > 0, len(e.Any) > 0, e.Not != nil, e.Test != nil} {
		if has {
			set++
		}
	}
	if set > 1 {
		return fmt.Errorf("a condition is one of a test, a negation, all-of or any-of, and this is several at once")
	}
	// A group that is there and empty is what the interface produces when the
	// last condition is deleted out of it. Read as "no conditions", it would
	// match every request, which for a block rule is everybody locked out.
	if set == 0 && (e.All != nil || e.Any != nil) {
		return fmt.Errorf(
			"a group of conditions has nothing in it; add a condition, or remove the rule and set the default action instead")
	}
	switch {
	case e.Test != nil:
		return e.Test.validate()
	case e.Not != nil:
		return e.Not.validate(depth + 1)
	default:
		for _, sub := range append(append([]Expr{}, e.All...), e.Any...) {
			if err := sub.validate(depth + 1); err != nil {
				return err
			}
		}
		return nil
	}
}

func (t Test) validate() error {
	known := false
	for _, field := range Fields {
		if t.Field == field {
			known = true
		}
	}
	if !known {
		return fmt.Errorf("%q is not something a rule can look at", t.Field)
	}
	if t.Field == FieldHeader && strings.TrimSpace(t.Key) == "" {
		return fmt.Errorf("a test on a header has to name the header")
	}
	if t.Field != FieldHeader && t.Key != "" {
		return fmt.Errorf("only a test on a header takes a header name")
	}
	valid := false
	for _, op := range Operators {
		if t.Op == op {
			valid = true
		}
	}
	if !valid {
		return fmt.Errorf("%q is not a comparison", t.Op)
	}
	if len(t.Values) == 0 {
		return fmt.Errorf("a test on %s has nothing to compare against", t.Field)
	}
	if len(t.Values) > maxValues {
		return fmt.Errorf("a test holds at most %d values", maxValues)
	}

	switch t.Field {
	case FieldIP:
		if t.Op != OpIn && t.Op != OpNotIn {
			return fmt.Errorf("an address is tested with %q or %q, not %q", OpIn, OpNotIn, t.Op)
		}
		for _, value := range t.Values {
			value = strings.TrimSpace(value)
			if strings.Contains(value, "/") {
				if _, err := netip.ParsePrefix(value); err != nil {
					return fmt.Errorf("%q is not an address range; they look like 203.0.113.0/24", value)
				}
				continue
			}
			if _, err := netip.ParseAddr(value); err != nil {
				return fmt.Errorf("%q is not an address or an address range", value)
			}
		}
	case FieldCountry:
		if t.Op != OpIn && t.Op != OpNotIn {
			return fmt.Errorf("a country is tested with %q or %q, not %q", OpIn, OpNotIn, t.Op)
		}
		for _, value := range t.Values {
			if !isCountryCode(value) {
				return fmt.Errorf("%q is not a two-letter country code; Indonesia is ID", value)
			}
		}
	case FieldASN:
		if t.Op != OpIn && t.Op != OpNotIn {
			return fmt.Errorf("a network is tested with %q or %q, not %q", OpIn, OpNotIn, t.Op)
		}
		for _, value := range t.Values {
			trimmed := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(value)), "AS")
			n, err := strconv.ParseUint(trimmed, 10, 32)
			if err != nil || n == 0 {
				return fmt.Errorf("%q is not a network number; they look like AS13335", value)
			}
		}
	}

	if t.Op == OpMatches {
		for _, value := range t.Values {
			if _, err := regexp.Compile(value); err != nil {
				return fmt.Errorf("%q is not a valid pattern: %w", value, err)
			}
		}
	}
	for _, value := range t.Values {
		if len(value) > maxValueLength {
			return fmt.Errorf("a value is longer than %d characters", maxValueLength)
		}
	}
	return nil
}

const (
	maxValues      = 1000
	maxValueLength = 512
)

func isCountryCode(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 2 {
		return false
	}
	for _, r := range strings.ToUpper(value) {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}
