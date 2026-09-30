package api

import (
	"fmt"

	"skifity/internal/crypto"
)

// saveKeyringTo writes the keyring, wrapping the error so a failure during
// rotation says what went wrong rather than just failing.
func saveKeyringTo(path string, ring *crypto.Keyring) error {
	if path == "" {
		return fmt.Errorf("no master key path is configured, so the rotated key cannot be saved")
	}
	if err := crypto.SaveKeyring(path, ring); err != nil {
		return fmt.Errorf("save the master key file at %s: %w", path, err)
	}
	return nil
}
