package store

import (
	"maps"
	"slices"

	"skifity/internal/kube"
)

// BuildSpec is what a deployment was built from: the settings that go into an
// image (kube.BuildInputs), and the build-time variables by key, each with a
// digest of its value and never the value. It is stored sealed on the
// deployment, and compared with what the app has now to say which saved
// changes are waiting for a deploy.
type BuildSpec struct {
	RepoURL        string `json:"repo_url,omitempty"`
	Branch         string `json:"branch,omitempty"`
	RootDir        string `json:"root_dir,omitempty"`
	Builder        string `json:"builder,omitempty"`
	DockerfilePath string `json:"dockerfile_path,omitempty"`
	BuildCommand   string `json:"build_command,omitempty"`
	StaticDir      string `json:"static_dir,omitempty"`
	Image          string `json:"image,omitempty"`
	// Variables are the build-time variables: key to digest.
	Variables map[string]string `json:"variables,omitempty"`
}

// BuildSpecContext is what a deployment's build spec is sealed to: its app,
// so a rollback can carry its version's spec over and a row copied to another
// app does not open.
func BuildSpecContext(appID string) string { return "build_spec:" + appID }

// BuildSpecOf is the spec a deploy of the app would build from now, given its
// build-time variables' digests. Only what the app's kind of source builds
// with is in it: an image app builds nothing and is its image, and a folder
// sent with `skifity up` has no repository or branch.
func BuildSpecOf(app App, variables map[string]string) BuildSpec {
	if app.SourceType == "image" {
		return BuildSpec{Image: app.Image}
	}
	spec := BuildSpec{
		RootDir: app.RootDir, Builder: app.Builder, DockerfilePath: app.DockerfilePath,
		BuildCommand: app.BuildCommand, StaticDir: app.StaticDir, Variables: variables,
	}
	// No builder and "auto" are the same choice, made by the panel.
	if spec.Builder == "" {
		spec.Builder = "auto"
	}
	if app.SourceType != "upload" {
		spec.RepoURL, spec.Branch = app.RepoURL, app.Branch
	}
	return spec
}

// setting is one of the spec's settings by its API name, and whether the spec
// has such a setting.
func (s BuildSpec) setting(name string) (string, bool) {
	switch name {
	case "repo_url":
		return s.RepoURL, true
	case "branch":
		return s.Branch, true
	case "root_dir":
		return s.RootDir, true
	case "builder":
		return s.Builder, true
	case "dockerfile_path":
		return s.DockerfilePath, true
	case "build_command":
		return s.BuildCommand, true
	case "static_dir":
		return s.StaticDir, true
	case "image":
		return s.Image, true
	}
	return "", false
}

// BuildChange is one saved change the running version was not built with: a
// setting, from what to what, or a build-time variable by its key alone.
type BuildChange struct {
	Setting string `json:"setting"`
	// Key is the variable's, for the setting "variable". Its value, and the
	// digest of it, are never here.
	Key  string `json:"key,omitempty"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// BuildChanges says how the current spec differs from the one the live
// version was built from, in the order kube.BuildInputs gives the settings,
// with the variables last and by key. Equal specs give an empty list.
func BuildChanges(live, current BuildSpec) []BuildChange {
	changes := []BuildChange{}
	for _, input := range kube.BuildInputs {
		if input.Setting == "variable" {
			keys := map[string]bool{}
			for key := range live.Variables {
				keys[key] = true
			}
			for key := range current.Variables {
				keys[key] = true
			}
			for _, key := range slices.Sorted(maps.Keys(keys)) {
				was, wasSet := live.Variables[key]
				now, nowSet := current.Variables[key]
				if was != now || wasSet != nowSet {
					changes = append(changes, BuildChange{Setting: "variable", Key: key})
				}
			}
			continue
		}
		was, _ := live.setting(input.Setting)
		now, _ := current.setting(input.Setting)
		if was != now {
			changes = append(changes, BuildChange{Setting: input.Setting, From: was, To: now})
		}
	}
	return changes
}
