package edgerules

import (
	"strings"
	"testing"
)

// Every spelling an app routes to /admin is /admin to a rule, and what cannot
// be read one way is refused rather than guessed at.
func TestCanonicalPath(t *testing.T) {
	for _, tc := range []struct {
		raw, want string
	}{
		// The ways around a rule about /admin that apps route to /admin.
		{"/%61dmin", "/admin"},
		{"//admin", "/admin"},
		{"/a/../admin", "/admin"},
		{"/./admin", "/admin"},
		{"/admin;jsessionid=1", "/admin"},
		{`\admin`, "/admin"},
		{`\\admin`, "/admin"},
		{"/%2e%2e/admin", "/admin"},
		{"/../../admin", "/admin"},
		{"/%2E%2E%2Fadmin", "/admin"},
		{"/%5cadmin", "/admin"},
		{"/;x/admin", "/admin"},
		{"/admin/./users/../", "/admin/"},
		// What is decoded once is decoded once.
		{"/admin%2fx", "/admin/x"},
		{"/api/v4/projects/group%2Fproject", "/api/v4/projects/group/project"},
		{"/100%25", "/100%"},
		{"/caf%C3%A9", "/café"},
		// What is kept.
		{"/admin/", "/admin/"},
		{"/Admin", "/Admin"},
		{"/admin/.", "/admin/"},
		{"/a/..", "/"},
		{"/admin/;x", "/admin/"},
		{"", "/"},
		{"/", "/"},
		{"admin", "/admin"},
		{"/file..txt", "/file..txt"},
		{"/.well-known/acme", "/.well-known/acme"},
		// The query and the fragment are not the path.
		{"/a?next=/admin", "/a"},
		{"/a#/admin", "/a"},
		{"/%61dmin?x=%zz", "/admin"},
	} {
		got, err := CanonicalPath(tc.raw)
		if err != nil {
			t.Errorf("%q: %v", tc.raw, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%q read as %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestAPathThatCannotBeReadOneWayIsRefused(t *testing.T) {
	for _, raw := range []string{
		"/%zz",
		"/%0",
		"/admin%",
		"/%00",
		"/%0a",
		"/%0D%0A",
		"/%7f",
		"/admin\x00",
		"/admin\n",
		"/%252f",
		"/%255C",
		"/%252E%252E/admin",
		// The same twice-encoded slash, spelled differently the first time.
		"/%25%32%66",
	} {
		if got, err := CanonicalPath(raw); err == nil {
			t.Errorf("%q was read as %q; it cannot be read one way", raw, got)
		} else if strings.TrimSpace(err.Error()) == "" {
			t.Errorf("%q was refused with no reason", raw)
		}
	}
}

// The fast path must agree with the slow one: a path it hands back unchanged
// is one the full reading would also leave unchanged.
func TestAPathAlreadyCanonicalIsLeftAlone(t *testing.T) {
	for _, raw := range []string{"/", "/admin", "/admin/", "/a/b.c/d", "/file..txt", "/.well-known/x", "/a./b"} {
		if !alreadyCanonical(raw) {
			t.Errorf("%q was not recognised as canonical", raw)
		}
		got, err := CanonicalPath(raw)
		if err != nil || got != raw {
			t.Errorf("%q read as %q (%v)", raw, got, err)
		}
	}
	for _, raw := range []string{"", "admin", "//a", "/a//b", "/./a", "/a/.", "/a/..", "/a/../b", "/a;b", `/a\b`, "/%61"} {
		if alreadyCanonical(raw) {
			t.Errorf("%q was taken as canonical", raw)
		}
	}
}

// A rule is compared in the same reading as the request, so the two ways of
// writing one rule are one rule.
func TestARuleOnThePathIsComparedAsAnAppReadsIt(t *testing.T) {
	block := func(op Operator, value string) RuleSet {
		return RuleSet{Default: ActionAllow, Rules: []Rule{{
			Name: "n", Action: ActionBlock, Enabled: true, Expr: test(FieldPath, op, value),
		}}}
	}
	for _, tc := range []struct {
		op    Operator
		value string
		path  string
		want  Action
	}{
		{OpIn, "/Admin/", "/admin/", ActionBlock},
		{OpIn, "/%61dmin/", "/admin/", ActionBlock},
		{OpIn, "/admin;x", "/admin", ActionBlock},
		{OpStartsWith, "/%61dmin", "/admin/users", ActionBlock},
		{OpStartsWith, `\admin`, "/admin", ActionBlock},
		{OpNotIn, "/%61dmin", "/admin", ActionAllow},
		{OpEndsWith, ".php", "/index.php", ActionBlock},
		{OpEndsWith, "%2Ephp", "/index.php", ActionBlock},
		{OpContains, "wp-%61dmin", "/blog/wp-admin/x", ActionBlock},
		{OpContains, `admin\users`, "/admin/users", ActionBlock},
		{OpContains, "admin", "/shop", ActionAllow},
	} {
		got := block(tc.op, tc.value).Evaluate(Request{Path: tc.path})
		if got.Action != tc.want {
			t.Errorf("path %s %q against %q: %s, want %s", tc.op, tc.value, tc.path, got.Action, tc.want)
		}
	}

	// A pattern is matched as written, against the canonical path.
	pattern := block(OpMatches, "^/Admin$")
	if got := pattern.Evaluate(Request{Path: "/admin"}); got.Action != ActionAllow {
		t.Error("a pattern was rewritten; it is compared as it was written")
	}
}

func TestARuleWhosePathCannotBeReadIsRefused(t *testing.T) {
	for _, tc := range []struct {
		op    Operator
		value string
	}{
		{OpIn, "/%zz"},
		{OpStartsWith, "/admin%00"},
		{OpContains, "%252f"},
		{OpStartsWith, "/admin?next=1"},
		{OpIn, "/admin#top"},
		{OpContains, ";jsessionid=1"},
	} {
		set := RuleSet{Rules: []Rule{{Name: "n", Action: ActionBlock, Enabled: true, Expr: test(FieldPath, tc.op, tc.value)}}}
		if err := set.Validate(); err == nil {
			t.Errorf("path %s %q was accepted", tc.op, tc.value)
		}
	}

	// A pattern is not a path: it is compiled, not read.
	pattern := RuleSet{Rules: []Rule{{Name: "n", Action: ActionBlock, Enabled: true, Expr: test(FieldPath, OpMatches, `^/a%zz?$`)}}}
	if err := pattern.Validate(); err != nil {
		t.Errorf("a pattern was read as a path: %v", err)
	}

	// And one stored before the check does not decide.
	stored := RuleSet{Default: ActionAllow, Rules: []Rule{{Name: "old", Action: ActionBlock, Enabled: true, Expr: test(FieldPath, OpStartsWith, "/%zz")}}}
	if got := stored.Evaluate(Request{Path: "/x"}); got.Action != ActionAllow || len(got.Skipped) != 1 {
		t.Errorf("a stored rule that cannot be read decided: %+v", got)
	}
}

func TestTestsPathSeesOnlyRulesThatAreOn(t *testing.T) {
	address := RuleSet{Rules: []Rule{{Name: "a", Action: ActionBlock, Enabled: true, Expr: test(FieldIP, OpIn, "203.0.113.7")}}}
	if address.TestsPath() {
		t.Error("a set about addresses said it looks at the path")
	}
	nested := RuleSet{Rules: []Rule{{Name: "p", Action: ActionBlock, Enabled: true, Expr: Expr{
		Any: []Expr{test(FieldIP, OpIn, "203.0.113.7"), {Not: &Expr{All: []Expr{test(FieldPath, OpStartsWith, "/admin")}}}},
	}}}}
	if !nested.TestsPath() {
		t.Error("a path test inside a group was missed")
	}
	off := nested
	off.Rules = []Rule{nested.Rules[0]}
	off.Rules[0].Enabled = false
	if off.TestsPath() {
		t.Error("a rule that is switched off was counted")
	}
	if (RuleSet{}).TestsPath() {
		t.Error("a set with no rules looks at nothing")
	}
}
