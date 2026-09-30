package templates

import (
	"testing"

	"skifity/internal/kube"
)

// What a template can ask of the engine beyond an image and variables: a
// command, files and a database in pieces. Each is checked here, before an
// install finds out.

func TestEveryTemplateFileCanBeMounted(t *testing.T) {
	for _, tpl := range All() {
		for _, svc := range tpl.Services {
			total, seen := 0, map[string]bool{}
			for _, file := range svc.Files {
				if err := kube.ValidateFilePath(file.Path); err != nil {
					t.Errorf("%s/%s: %v", tpl.ID, svc.Name, err)
				}
				if seen[file.Path] {
					t.Errorf("%s/%s has two files at %s", tpl.ID, svc.Name, file.Path)
				}
				seen[file.Path] = true
				for _, v := range svc.Volumes {
					if v.MountPath == file.Path {
						t.Errorf("%s/%s puts a file where the volume %s is mounted", tpl.ID, svc.Name, v.Name)
					}
				}
				if len(file.Content) > kube.MaxFileBytes {
					t.Errorf("%s/%s: %s is over %d bytes", tpl.ID, svc.Name, file.Path, kube.MaxFileBytes)
				}
				total += len(file.Content)
			}
			if total > kube.MaxAllFilesBytes || len(svc.Files) > kube.MaxFiles {
				t.Errorf("%s/%s has more files than an app can hold", tpl.ID, svc.Name)
			}
		}
	}
}

// A database's pieces arrive as variables, so each needs a name a container
// can carry, and two pieces under one name would leave one of them missing.
func TestEveryDatabasePieceArrivesAsAVariable(t *testing.T) {
	for _, tpl := range All() {
		for _, db := range tpl.Databases {
			names := map[string]bool{db.VarName: db.VarName != ""}
			for _, name := range []string{db.Vars.Host, db.Vars.Port, db.Vars.Name, db.Vars.User, db.Vars.Password} {
				if name == "" {
					continue
				}
				if clean, err := kube.SanitiseEnvKey(name); err != nil || clean != name {
					t.Errorf("%s/%s: %q is not a variable name a container carries", tpl.ID, db.Name, name)
				}
				if names[name] {
					t.Errorf("%s/%s delivers two things as %s", tpl.ID, db.Name, name)
				}
				names[name] = true
			}
		}
	}
}
