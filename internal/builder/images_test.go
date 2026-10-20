package builder

import (
	"strings"
	"testing"
)

// Every image a build runs is pinned, by version and by digest. A tag like
// latest or master means a build on Tuesday can run different tools from the
// one on Monday with nothing in the repository changed, and whoever can move
// the tag chooses what runs next to the team's build variables.
func TestEveryBuildImageIsPinned(t *testing.T) {
	spec := baseJob()
	spec.Defaults()
	for name, image := range map[string]string{
		"git": spec.GitImage, "buildkit": spec.BuildKitImage,
		"railpack prepare": spec.RailpackImage, "railpack frontend": spec.RailpackFrontend,
	} {
		tag := image
		if at := strings.Index(image, "@"); at >= 0 {
			tag = image[:at]
		} else {
			t.Errorf("%s is %s, which is not pinned by digest", name, image)
		}
		for _, floating := range []string{":latest", ":master", ":nightly"} {
			if strings.HasSuffix(tag, floating) {
				t.Errorf("%s follows %s", name, floating)
			}
		}
		if !strings.Contains(image, "@sha256:") || len(image[strings.Index(image, "@sha256:")+8:]) != 64 {
			t.Errorf("%s does not carry a full sha256 digest: %s", name, image)
		}
	}
}

// The step that writes a Railpack plan and the frontend that reads it are one
// program, and must be one version of it.
func TestThePlanIsWrittenAndReadByTheSameRailpack(t *testing.T) {
	spec := baseJob()
	spec.Defaults()
	if spec.RailpackImage != spec.RailpackFrontend {
		t.Fatalf("the plan is written by %s and read by %s", spec.RailpackImage, spec.RailpackFrontend)
	}
}

// The build container runs the daemon and buildctl, so its image has to be
// the rootless one, of the version the panel reports.
func TestTheBuildRunsTheRootlessBuildKit(t *testing.T) {
	image := BuildKitImage[:strings.Index(BuildKitImage, "@")]
	if !strings.HasSuffix(image, ":"+BuildKitVersion+"-rootless") {
		t.Fatalf("the build runs %s, not BuildKit %s rootless", BuildKitImage, BuildKitVersion)
	}
}

// Nobody publishes an image with the nixpacks command in it, and the one the
// panel used to default to is the base image Nixpacks builds start from.
func TestNixpacksHasNoDefaultImage(t *testing.T) {
	spec := baseJob()
	spec.Builder = BuilderNixpacks
	if _, err := BuildJob(spec); err == nil {
		t.Fatal("a Nixpacks build was rendered with no image that has nixpacks in it")
	}
}
