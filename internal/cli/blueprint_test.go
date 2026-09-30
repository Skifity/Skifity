package cli

import (
	"testing"

	"skifity/internal/blueprint"
)

func TestAStepIsSaidWithItsOwnName(t *testing.T) {
	for _, c := range []struct {
		step blueprint.Step
		want string
	}{
		// An app's own settings and scaling are the app's.
		{blueprint.Step{Kind: "scaling", App: "web", Name: "web", Detail: "1 → 2"}, "web: scaling — 1 → 2"},
		{blueprint.Step{Kind: "settings", App: "web", Name: "web"}, "web: settings"},
		// A process that happens to share its app's name is still named.
		{blueprint.Step{Kind: "process", App: "worker", Name: "worker"}, "worker: process worker"},
		{blueprint.Step{Kind: "variable", App: "web", Name: "LOG_LEVEL"}, "web: variable LOG_LEVEL"},
		{blueprint.Step{Kind: "app", App: "web", Name: "web"}, "app web"},
	} {
		if got := describe(c.step); got != c.want {
			t.Errorf("%+v is said as %q, want %q", c.step, got, c.want)
		}
	}
}
