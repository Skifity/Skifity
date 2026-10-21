package settings_test

import (
	"strconv"
	"testing"

	"skifity/internal/backup"
	"skifity/internal/settings"
	"skifity/internal/store"
	"skifity/internal/watch"
)

// TestDefaultsAgreeWithTheCodeThatReadsThem: a default here is what the form
// shows, and the constant in the package that reads the setting is what the
// panel does. Two copies drift, so these are held to each other — the ones
// whose fallback is a constant somebody else declared.
func TestDefaultsAgreeWithTheCodeThatReadsThem(t *testing.T) {
	retention := store.DefaultRetention()
	for key, want := range map[string]string{
		settings.KeyPreviewTTLDays:      strconv.Itoa(watch.DefaultPreviewTTLDays),
		settings.KeyPanelBackupSchedule: backup.DefaultPanelSchedule,
		settings.KeyPanelBackupKeep:     strconv.Itoa(backup.DefaultPanelKeep),
		settings.KeyDeploymentHistory:   strconv.Itoa(retention.DeploymentsPerApp),
		settings.KeyAuditHistoryDays:    strconv.Itoa(retention.AuditDays),
		settings.KeyBuildConcurrency:    strconv.Itoa(settings.DefaultBuildConcurrency),
	} {
		def, ok := settings.Lookup(key)
		if !ok {
			t.Fatalf("%s is not a setting", key)
		}
		if def.Default != want {
			t.Errorf("%s shows %q as its default, and the panel uses %q when it is unset", key, def.Default, want)
		}
	}
}
