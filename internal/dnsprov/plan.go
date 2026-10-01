package dnsprov

import (
	"fmt"
	"strings"
)

// Deciding what to do at one name.
//
// The rule the whole feature rests on: the panel only ever changes or deletes
// a record it created, and never one somebody else made. "Created" is decided
// by the panel's own books — a row for each record, with the id the provider
// gave it and the value it was given — and, where the provider keeps a note,
// by the note still being the one the panel wrote. A record that is not in the
// books is somebody else's, whatever it says.
//
// A record somebody else made that already says what the panel wants is left
// alone and reported as managed elsewhere. One that says something else is a
// conflict: nothing at the name is touched, and the conflict names the record
// so a person can decide. So is a record the panel made that somebody has
// changed since: it is theirs now.

// Want is a record the panel wants a hostname to have.
type Want struct {
	Type    string
	Content string
	// Proxied is Cloudflare's proxy, which only the CNAME to a tunnel asks for.
	Proxied bool
}

// Describe is a wanted record as a sentence names it: "A 203.0.113.10".
func (w Want) Describe() string { return w.Type + " " + w.Content }

// Owned is a record the panel's books say it created.
type Owned struct {
	// Row is the books' own id for it.
	Row      string
	RemoteID string
	Type     string
	Content  string
}

// Update is a record of the panel's to point somewhere else.
type Update struct {
	Owned  Owned
	Record Record
	To     string
}

// Kept is a record of the panel's that already says what is wanted.
type Kept struct {
	Owned  Owned
	Record Record
}

// Changed is a record the panel made that somebody has changed since, as it
// is now. The panel forgets it and leaves it where it is.
type Changed struct {
	Owned Owned
	Now   []Record
}

// Plan is what to do at one name, in the order it is done: forget, delete,
// update, create.
type Plan struct {
	// Forget are rows whose record is gone from the provider, or was
	// changed by somebody else.
	Forget []Owned
	// Delete are the panel's records no longer wanted: the AAAA once there is
	// no IPv6 address, the A when a tunnel's CNAME replaces it.
	Delete []Kept
	Update []Update
	Create []Want
	Keep   []Kept
	// Adopt are records carrying this panel's own note that the books lost —
	// a panel restored from an older backup — and that already say what is
	// wanted. They go back in the books rather than being made again.
	Adopt []Record
	// Elsewhere are records somebody else made that already say what is
	// wanted.
	Elsewhere []Record
	// Changed are records the panel made that somebody has changed since.
	Changed []Changed
}

// Conflict is a record the panel did not create standing where one it wants
// would go.
type Conflict struct {
	Name  string
	Found Record
	Want  Want
	// Reason is which of three it is: "value", a record of the wanted type
	// saying something else; "cname", a CNAME that cannot share its name with
	// anything, or anything where a CNAME is wanted; "extra", an address of a
	// type the panel is not creating, which some visitors would reach instead.
	Reason string
}

func (c *Conflict) Error() string {
	return fmt.Sprintf("%s already has %s, which Skifity did not create", c.Name, c.Found.Describe())
}

// Decide works out what to do at name, given every record there now, the
// ones the books say the panel created there, and the ones it wants. notes
// says whether the provider keeps a note with a record, and marker is the
// note this panel writes.
//
// With a conflict, the plan still carries what to forget, and nothing else.
func Decide(name string, existing []Record, owned []Owned, want []Want, marker string, notes bool) (Plan, error) {
	name = Canonical(name)
	var plan Plan

	here := make([]Record, 0, len(existing))
	for _, r := range existing {
		if Canonical(r.Name) == name {
			r.Content = canonicalContent(r.Type, r.Content)
			here = append(here, r)
		}
	}
	for i := range want {
		want[i].Type = strings.ToUpper(want[i].Type)
		want[i].Content = canonicalContent(want[i].Type, want[i].Content)
	}

	// Which records are the panel's. An id can be shared by the values of
	// one set, so a row claims the one value it was given, and a set that
	// has grown a value the panel did not put there is not the panel's.
	ours := map[int]Owned{}
	for _, o := range owned {
		o.Type = strings.ToUpper(o.Type)
		o.Content = canonicalContent(o.Type, o.Content)
		var same []int
		for i, r := range here {
			if r.ID == o.RemoteID && r.Type == o.Type {
				same = append(same, i)
			}
		}
		switch {
		case len(same) == 0:
			plan.Forget = append(plan.Forget, o)
		case len(same) == 1 && here[same[0]].Content == o.Content && (!notes || here[same[0]].Note == marker):
			ours[same[0]] = o
		default:
			now := make([]Record, 0, len(same))
			for _, i := range same {
				now = append(now, here[i])
			}
			plan.Changed = append(plan.Changed, Changed{Owned: o, Now: now})
			plan.Forget = append(plan.Forget, o)
		}
	}

	wanted := map[string]Want{}
	for _, w := range want {
		wanted[w.Type] = w
	}

	// A record with this panel's own note that the books do not have, and
	// that says exactly what is wanted, is the panel's: nobody else writes
	// that note.
	adopted := map[int]bool{}
	if notes && marker != "" {
		for i, r := range here {
			if _, claimed := ours[i]; claimed || r.Note != marker {
				continue
			}
			if w, ok := wanted[r.Type]; ok && w.Content == r.Content && sharing(here, r) == 1 {
				adopted[i] = true
				plan.Adopt = append(plan.Adopt, r)
			}
		}
	}
	mine := func(i int) bool { _, claimed := ours[i]; return claimed || adopted[i] }

	var conflict *Conflict
	refuse := func(r Record, w Want, reason string) {
		if conflict == nil {
			conflict = &Conflict{Name: name, Found: r, Want: w, Reason: reason}
		}
	}

	for _, w := range want {
		handled := false
		for i, r := range here {
			if r.Type != w.Type || !mine(i) {
				continue
			}
			handled = true
			switch {
			case adopted[i]:
			case r.Content == w.Content:
				plan.Keep = append(plan.Keep, Kept{Owned: ours[i], Record: r})
			default:
				plan.Update = append(plan.Update, Update{Owned: ours[i], Record: r, To: w.Content})
			}
		}

		// Somebody else's record of the same type beside the panel's, or in
		// place of one, has to say the same thing: two A records are two
		// places visitors are sent, one at a time.
		var foreign []Record
		for i, r := range here {
			if !mine(i) && r.Type == w.Type {
				foreign = append(foreign, r)
				if r.Content != w.Content {
					refuse(r, w, "value")
				}
			}
		}
		if handled {
			continue
		}
		if len(foreign) > 0 {
			plan.Elsewhere = append(plan.Elsewhere, foreign...)
			continue
		}

		for i, r := range here {
			if mine(i) {
				continue
			}
			if w.Type == "CNAME" || r.Type == "CNAME" {
				refuse(r, w, "cname")
			}
		}
		plan.Create = append(plan.Create, w)
	}

	// An address of a type nobody asked for sends some visitors elsewhere:
	// an old AAAA beside the new A is the usual one.
	for i, r := range here {
		if mine(i) {
			continue
		}
		if _, ok := wanted[r.Type]; ok {
			continue
		}
		if r.Type == "A" || r.Type == "AAAA" {
			refuse(r, Want{Type: r.Type}, "extra")
		}
	}

	for i, r := range here {
		if o, claimed := ours[i]; claimed {
			if _, ok := wanted[r.Type]; !ok {
				plan.Delete = append(plan.Delete, Kept{Owned: o, Record: r})
			}
		}
	}

	if conflict != nil {
		return Plan{Forget: plan.Forget, Changed: plan.Changed}, conflict
	}
	return plan, nil
}

// sharing is how many values the set a record is in has.
func sharing(records []Record, record Record) int {
	n := 0
	for _, r := range records {
		if r.ID == record.ID && r.Type == record.Type {
			n++
		}
	}
	return n
}

// Wildcard is the name of the wildcard record that would answer for a
// hostname: *.shop.example.com for www.shop.example.com. The zone's own name
// has none.
func Wildcard(hostname, zone string) string {
	hostname, zone = Canonical(hostname), Canonical(zone)
	if hostname == zone {
		return ""
	}
	_, parent, found := strings.Cut(hostname, ".")
	if !found || (parent != zone && !strings.HasSuffix(parent, "."+zone)) {
		return ""
	}
	return "*." + parent
}

// Covers reports whether a wildcard's records already say everything wanted,
// so a hostname under it needs no record of its own.
func Covers(records []Record, want []Want) bool {
	if len(want) == 0 {
		return false
	}
	for _, w := range want {
		found := false
		for _, r := range records {
			if r.Type == strings.ToUpper(w.Type) {
				if canonicalContent(r.Type, r.Content) != canonicalContent(r.Type, w.Content) {
					return false
				}
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Still finds the record the books say the panel created, if it is still at
// the provider as the panel left it: the same id, the same value, the only
// value in its set, and — where the provider keeps notes — the panel's note.
// Anything else is gone, or somebody's now.
func Still(existing []Record, owned Owned, marker string, notes bool) (Record, bool) {
	kind := strings.ToUpper(owned.Type)
	content := canonicalContent(kind, owned.Content)
	var found []Record
	for _, r := range existing {
		if r.ID == owned.RemoteID && strings.ToUpper(r.Type) == kind {
			found = append(found, r)
		}
	}
	if len(found) != 1 || canonicalContent(kind, found[0].Content) != content || (notes && found[0].Note != marker) {
		return Record{}, false
	}
	return found[0], true
}
