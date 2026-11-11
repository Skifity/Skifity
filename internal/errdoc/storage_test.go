package errdoc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Backup storage is the panel's, set by a panel administrator under
// Settings → General → Backup storage. The errors about it said "This team
// has no storage" and "Open Settings, then Storage": a tab no team member has,
// on a page that does not have one by that name.
const storagePath = "Settings → General → Backup storage"

func TestStorageErrorsNameThePageItIsSetOn(t *testing.T) {
	for _, problem := range []*Problem{
		StorageNotConfigured(),
		StorageEndpointInvalid("s3 example", "no scheme"),
		BackupPassphraseMissing(),
		BackupWrongPassphrase(),
		TemplateNeedsBackups("web"),
	} {
		if !strings.Contains(problem.Fix, storagePath) {
			t.Errorf("%s says to %q, which does not name %s", problem.Code, problem.Fix, storagePath)
		}
	}
	if strings.Contains(StorageNotConfigured().Cause, "team") {
		t.Errorf("the storage is the panel's, and the cause says %q", StorageNotConfigured().Cause)
	}
}

// The old path is gone from everything the panel says, the templates' notes
// included.
func TestNothingSendsAnybodyToSettingsThenStorage(t *testing.T) {
	root := filepath.Join("..", "..", "internal")
	checked := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if ext := filepath.Ext(path); ext != ".go" && ext != ".yaml" {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		checked++
		if strings.Contains(string(body), "Settings, then Storage") {
			t.Errorf("%s still sends people to Settings, then Storage; it is %s", path, storagePath)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 100 {
		t.Fatalf("only %d files were read; this test is not reading internal/", checked)
	}
}
