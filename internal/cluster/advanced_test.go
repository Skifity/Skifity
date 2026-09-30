package cluster

import (
	"strings"
	"testing"

	"skifity/internal/store"
)

// The Advanced view renders an app's Kubernetes objects, and the Secret that
// carries its variables is not one of them: a value read from a secret
// manager, or any secret variable, reaches the cluster there and must not
// reach a page anybody in the team can open. The Deployment names the Secret;
// it never holds what is in it.
func TestTheAdvancedViewNeverRendersTheVariablesSecret(t *testing.T) {
	c, db, app, env, _ := autoDomainFixture(t)
	connection := store.SecretConnection{ID: store.NewID("sm"), TeamID: mustTeam(t, db, app.ID), Name: "company-vault",
		Kind: "vault", Settings: map[string]string{"address": "https://vault.example.test"}}
	if err := db.CreateSecretConnection(t.Context(), &connection, "sealed-in-test"); err != nil {
		t.Fatal(err)
	}
	for _, v := range []store.Variable{
		{AppID: app.ID, Key: "STRIPE_KEY", IsSecret: true,
			Reference: &store.SecretReference{ConnectionID: connection.ID, Path: "shop", Key: "stripe_key"}},
		{AppID: app.ID, Key: "PLAIN", IsSecret: false},
	} {
		if err := db.SetVariable(t.Context(), &v, "sealed-value-that-must-not-appear"); err != nil {
			t.Fatal(err)
		}
	}

	manifests, err := c.Manifests(t.Context(), app, env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(manifests, "kind: Deployment") {
		t.Fatalf("the view rendered no Deployment, so this test checks nothing:\n%s", manifests)
	}
	if strings.Contains(manifests, "kind: Secret") {
		t.Errorf("the Advanced view renders a Secret:\n%s", manifests)
	}
	for _, leaked := range []string{"sealed-value-that-must-not-appear", "STRIPE_KEY", "stripe_key"} {
		if strings.Contains(manifests, leaked) {
			t.Errorf("the Advanced view carries %q:\n%s", leaked, manifests)
		}
	}
}

func mustTeam(t *testing.T, db *store.DB, appID string) string {
	t.Helper()
	teamID, err := db.TeamIDForApp(t.Context(), appID)
	if err != nil {
		t.Fatal(err)
	}
	return teamID
}
