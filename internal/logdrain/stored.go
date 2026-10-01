package logdrain

import (
	"encoding/json"
	"errors"
	"fmt"

	"skifity/internal/store"
)

// A drain as it is kept: its settings in the row, its secrets sealed beside
// them under store.LogDrainContext — the team, the drain and the address it
// sends to. The API and the collector both read a drain through FromRow, so
// both open the same secrets under the same context.

// Opener opens sealed values; the panel's keyring is one.
type Opener interface {
	Open(stored, context string) ([]byte, error)
}

// Sealer seals them.
type Sealer interface {
	Seal(plaintext []byte, context string) (string, error)
}

// ErrNoKeyring is a drain's credentials asked for by a panel with no keyring
// to open them with.
var ErrNoKeyring = errors.New("the log drain's credentials are sealed and this panel has no keyring to open them")

// SealContext is what a drain's secrets are sealed under.
func SealContext(d Drain) (string, error) {
	address, err := Address(d.Kind, d.Settings)
	if err != nil {
		return "", err
	}
	return store.LogDrainContext(d.TeamID, d.ID, address), nil
}

// Seal seals a drain's secrets for keeping. A drain with none keeps nothing.
func Seal(sealer Sealer, d Drain) (string, error) {
	if len(d.Secrets) == 0 {
		return "", nil
	}
	context, err := SealContext(d)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(d.Secrets)
	if err != nil {
		return "", err
	}
	return sealer.Seal(raw, context)
}

// FromRow is a kept drain with its secrets open.
func FromRow(opener Opener, row store.LogDrainRow) (Drain, error) {
	d := Drain{
		ID: row.ID, TeamID: row.TeamID, Name: row.Name, Kind: row.Kind,
		Settings: row.Settings, Secrets: map[string]string{},
		Scoped: row.Scoped, Projects: row.Projects, IncludeBuilds: row.IncludeBuilds,
	}
	if row.SealedSecrets == "" {
		return d, nil
	}
	if opener == nil {
		return d, ErrNoKeyring
	}
	context, err := SealContext(d)
	if err != nil {
		return d, err
	}
	raw, err := opener.Open(row.SealedSecrets, context)
	if err != nil {
		return d, fmt.Errorf("open the credentials of the log drain %s: %w", row.Name, err)
	}
	if err := json.Unmarshal(raw, &d.Secrets); err != nil {
		return d, fmt.Errorf("read the credentials of the log drain %s: %w", row.Name, err)
	}
	return d, nil
}
