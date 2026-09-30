package kube

import "testing"

// What an app can read about itself. These were Skifity_APP and
// Skifity_ENVIRONMENT, in mixed case, and nowhere documented.

func plainEnv(s AppSpec) map[string]string {
	out := map[string]string{}
	for _, e := range BuildDeployment(s).Spec.Template.Spec.Containers[0].Env {
		out[e.Name] = e.Value
	}
	return out
}

func TestAnAppKnowsWhoAndWhereItIs(t *testing.T) {
	spec := baseSpec()
	spec.CommitSHA = "0123456789abcdef0123456789abcdef01234567"
	spec.URL = "https://shop.example.com"
	env := plainEnv(spec)

	for name, want := range map[string]string{
		"SKIFITY_APP":         "web",
		"SKIFITY_ENVIRONMENT": "production",
		"SKIFITY_COMMIT_SHA":  "0123456789abcdef0123456789abcdef01234567",
		"SKIFITY_URL":         "https://shop.example.com",
	} {
		if env[name] != want {
			t.Errorf("%s = %q, want %q", name, env[name], want)
		}
	}
	for _, old := range []string{"Skifity_APP", "Skifity_ENVIRONMENT"} {
		if _, ok := env[old]; ok {
			t.Errorf("the mixed-case %s is still set", old)
		}
	}
	// Production is not a preview.
	if _, ok := env["SKIFITY_PREVIEW"]; ok {
		t.Error("a production app says it is a preview")
	}
}

func TestAPreviewKnowsItIsOne(t *testing.T) {
	spec := baseSpec()
	spec.Preview, spec.PullRequest = true, 12
	env := plainEnv(spec)
	if env["SKIFITY_PREVIEW"] != "true" || env["SKIFITY_PULL_REQUEST"] != "12" {
		t.Errorf("a preview reads SKIFITY_PREVIEW=%q SKIFITY_PULL_REQUEST=%q",
			env["SKIFITY_PREVIEW"], env["SKIFITY_PULL_REQUEST"])
	}
}

// Nothing is set that there is no value for, so an app can tell "not known"
// from "empty".
func TestUnknownValuesAreLeftUnset(t *testing.T) {
	env := plainEnv(baseSpec())
	for _, name := range []string{"SKIFITY_COMMIT_SHA", "SKIFITY_URL", "SKIFITY_PULL_REQUEST"} {
		if _, ok := env[name]; ok {
			t.Errorf("%s is set with nothing to say", name)
		}
	}
}
