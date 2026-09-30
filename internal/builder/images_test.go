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
		"git": spec.GitImage, "buildctl": spec.BuildKitImage,
		"railpack prepare": spec.RailpackImage, "railpack frontend": spec.RailpackFrontend,
		"buildkit daemon": BuildKitDaemonImage,
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

// buildctl and the daemon it talks to are the same release.
func TestBuildctlMatchesTheDaemon(t *testing.T) {
	version := func(image string) string {
		image = image[:strings.Index(image, "@")]
		return strings.TrimSuffix(image[strings.LastIndex(image, ":")+1:], "-rootless")
	}
	if version(BuildKitClientImage) != version(BuildKitDaemonImage) {
		t.Fatalf("buildctl is %s and the daemon %s", BuildKitClientImage, BuildKitDaemonImage)
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
