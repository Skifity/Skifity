package notify

import (
	"maps"
	"strings"
)

// Editing a channel.
//
// A channel could be created, tested and deleted, and not changed: a token
// that was rotated, a second event somebody wanted, a typo in a chat id, all
// meant deleting the channel and typing every one of its settings again.
//
// The settings are sealed as one document and some of them are secrets, which
// are write-only everywhere in the panel: once stored, a secret is never sent
// back to a browser. So an edit is a merge. The form shows what is not
// secret, shows that a secret is stored without showing it, and a secret left
// empty keeps what is stored. Anything the form does not know is treated as a
// secret, because the cost of being wrong that way is a value kept, and the
// cost of being wrong the other way is a credential shown or lost.

// FormOf returns the fields a kind's form has: the built-in one, or the one
// its plugin declares. known is false for a kind nothing describes right now —
// a plugin that is not installed, or could not be asked.
func FormOf(kind string, provided []ChannelKind) (fields []Field, known bool) {
	if form, ok := builtInForms[kind]; ok {
		return form, true
	}
	for _, candidate := range provided {
		if candidate.Kind == kind {
			return candidate.Fields, true
		}
	}
	return nil, false
}

// secretIn reports whether a key holds a secret in a form. A key the form does
// not declare, and every key of a form nobody can read, counts as one.
func secretIn(fields []Field, known bool, key string) bool {
	if !known {
		return true
	}
	for _, field := range fields {
		if field.Key == key {
			return field.Secret || field.Kind == "password"
		}
	}
	return true
}

// Revealable splits a stored configuration into what the form may show again —
// every declared setting that is not a secret — and the names of the secrets
// that are stored, so the form can say they are there without saying what
// they are. A setting the form does not declare is neither: it stays where it
// is, unseen and unchanged.
func Revealable(fields []Field, known bool, stored map[string]string) (values map[string]string, secrets []string) {
	values = map[string]string{}
	secrets = []string{}
	if !known {
		return values, secrets
	}
	for _, field := range fields {
		value := stored[field.Key]
		if strings.TrimSpace(value) == "" {
			continue
		}
		if secretIn(fields, known, field.Key) {
			secrets = append(secrets, field.Key)
			continue
		}
		values[field.Key] = value
	}
	return values, secrets
}

// MergeConfig applies an edit to a stored configuration and returns the
// result; neither argument is changed.
//
// A key the edit does not mention is kept. A secret sent empty is kept too,
// which is what an untouched password box sends. Anything else sent empty is
// cleared, and anything sent with a value replaces what was there.
func MergeConfig(fields []Field, known bool, stored, edit map[string]string) map[string]string {
	merged := maps.Clone(stored)
	if merged == nil {
		merged = map[string]string{}
	}
	for key, value := range edit {
		if strings.TrimSpace(value) != "" {
			merged[key] = value
			continue
		}
		if secretIn(fields, known, key) {
			continue
		}
		delete(merged, key)
	}
	return merged
}
