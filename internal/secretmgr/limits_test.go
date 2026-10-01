package secretmgr

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// A prefix allows the path itself and what is under it at the kind's
// separator, and never a longer name that merely starts with it: "app" is not
// a way to "app-b".
func TestAPathLimitStopsAtTheSeparator(t *testing.T) {
	for _, tc := range []struct {
		kind     string
		prefixes []string
		path     string
		allowed  bool
	}{
		// Vault: under the mount, divided at /.
		{KindVault, []string{"app"}, "app", true},
		{KindVault, []string{"app"}, "app/db", true},
		{KindVault, []string{"app"}, "/app/db/", true},
		{KindVault, []string{"app"}, "app/deep/er", true},
		{KindVault, []string{"app"}, "app-b", false},
		{KindVault, []string{"app"}, "app-b/db", false},
		{KindVault, []string{"app"}, "apple", false},
		{KindVault, []string{"app"}, "ap", false},
		{KindVault, []string{"app/db"}, "app", false},
		{KindVault, []string{"app"}, "app/./db", false},
		{KindVault, []string{"app"}, "app/../b", false},
		{KindVault, []string{"app"}, "APP/db", false},
		{KindVault, []string{"app-a", "shared/smtp"}, "shared/smtp", true},
		{KindVault, []string{"app-a", "shared/smtp"}, "shared/smtpd", false},
		{KindVault, nil, "anything/at/all", true},

		// Infisical: a folder from the root, with or without its slash.
		{KindInfisical, []string{"/app"}, "/app/STRIPE_KEY", true},
		{KindInfisical, []string{"/app"}, "app/STRIPE_KEY", true},
		{KindInfisical, []string{"/app"}, "/app", true},
		{KindInfisical, []string{"/app"}, "/app-b/STRIPE_KEY", false},
		{KindInfisical, []string{"/app"}, "STRIPE_KEY", false},
		{KindInfisical, []string{"/app"}, "/application/KEY", false},

		// Doppler: a name, divided at _, compared in upper case.
		{KindDoppler, []string{"APP"}, "APP", true},
		{KindDoppler, []string{"APP"}, "APP_STRIPE_KEY", true},
		{KindDoppler, []string{"APP"}, "app_stripe_key", true},
		{KindDoppler, []string{"APP"}, "APPLE_KEY", false},
		{KindDoppler, []string{"APP"}, "APP2_KEY", false},
		{KindDoppler, []string{"APP_A"}, "APP_B_KEY", false},
		{KindDoppler, []string{"APP_A"}, "APP_AB_KEY", false},

		// AWS: a name or an ARN, as written, divided at /.
		{KindAWS, []string{"prod/app"}, "prod/app", true},
		{KindAWS, []string{"prod/app"}, "prod/app/db", true},
		{KindAWS, []string{"prod/app"}, "prod/app-b", false},
		{KindAWS, []string{"prod/app"}, "prod/app-AbCdEf", false},
		{KindAWS, []string{"prod/app"}, "prod", false},
		{KindAWS, []string{"prod/app"}, "arn:aws:secretsmanager:eu-central-1:123456789012:secret:prod/app/db-AbCdEf", false},
		{KindAWS, []string{"arn:aws:secretsmanager:eu-central-1:123456789012:secret:prod/app"},
			"arn:aws:secretsmanager:eu-central-1:123456789012:secret:prod/app/db-AbCdEf", true},
		{KindAWS, []string{"arn:aws:secretsmanager:eu-central-1:123456789012:secret:prod/app"},
			"arn:aws:secretsmanager:eu-central-1:999999999999:secret:prod/app/db-AbCdEf", false},
		{KindAWS, []string{"arn:aws:secretsmanager:eu-central-1:123456789012:secret:prod/app"},
			"arn:aws:secretsmanager:eu-central-1:123456789012:secret:prod/app-b", false},
	} {
		if got := PathAllowed(tc.kind, tc.prefixes, tc.path); got != tc.allowed {
			t.Errorf("%s limited to %v, %q: allowed %v, want %v", tc.kind, tc.prefixes, tc.path, got, tc.allowed)
		}
	}
}

func TestAPathLimitIsWrittenTheWayAReferenceIs(t *testing.T) {
	vault := map[string]string{"mount": "secret"}
	for _, tc := range []struct {
		kind    string
		written []string
		want    []string
	}{
		{KindVault, []string{"app"}, []string{"app"}},
		{KindVault, []string{"/app/"}, []string{"app"}},
		// As a Vault policy writes it: the mount's data path, and a star.
		{KindVault, []string{"secret/data/app"}, []string{"app"}},
		{KindVault, []string{"secret/data/app/*"}, []string{"app"}},
		{KindVault, []string{"app/*", "app", " ", "shared/smtp"}, []string{"app", "shared/smtp"}},
		{KindInfisical, []string{"app"}, []string{"/app"}},
		{KindInfisical, []string{"/app/"}, []string{"/app"}},
		{KindInfisical, []string{"/app/*"}, []string{"/app"}},
		{KindDoppler, []string{"app"}, []string{"APP"}},
		{KindDoppler, []string{"APP_"}, []string{"APP"}},
		{KindDoppler, []string{"APP_*"}, []string{"APP"}},
		{KindAWS, []string{"prod/app/"}, []string{"prod/app"}},
		{KindAWS, []string{"prod/app/*"}, []string{"prod/app"}},
		{KindAWS, nil, []string{}},
	} {
		got, err := NormalizeAllowedPaths(tc.kind, vault, tc.written)
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("%s %q: %q, %v; want %q", tc.kind, tc.written, got, err, tc.want)
		}
	}

	for _, tc := range []struct {
		kind    string
		written string
	}{
		// A star anywhere but the end is a string prefix, which a limit is not.
		{KindVault, "app*"},
		{KindVault, "a*p/db"},
		{KindAWS, "prod/app-*"},
		{KindDoppler, "APP*"},
		// Every path, which an empty list already says.
		{KindVault, "*"},
		{KindVault, "/"},
		{KindVault, "secret/data"},
		{KindVault, "secret/data/*"},
		{KindInfisical, "/"},
		{KindDoppler, "_"},
		// Not a path a secret of that kind can be at or under.
		{KindVault, "app/../other"},
		{KindVault, "app/./db"},
		{KindInfisical, "/app//db"},
		{KindDoppler, "APP-B"},
		{KindAWS, "prod app"},
	} {
		if got, err := NormalizeAllowedPaths(tc.kind, vault, []string{tc.written}); err == nil {
			t.Errorf("%s %q was accepted as %q", tc.kind, tc.written, got)
		}
	}
}

func TestAProjectLimitAllowsOnlyItsProjects(t *testing.T) {
	if !ProjectAllowed(nil, "prj_a") || !ProjectAllowed([]string{}, "") {
		t.Error("no project limit refused a project")
	}
	if !ProjectAllowed([]string{"prj_a", "prj_b"}, "prj_b") {
		t.Error("a listed project was refused")
	}
	if ProjectAllowed([]string{"prj_a"}, "prj_b") || ProjectAllowed([]string{"prj_a"}, "") {
		t.Error("a project not on the list, or none at all, was allowed")
	}
	c := store.SecretConnection{Kind: KindVault, AllowedPaths: []string{"app"}, AllowedProjectIDs: []string{"prj_a"}}
	for _, tc := range []struct {
		project, path string
		want          Limit
	}{
		{"prj_a", "app/db", ""},
		{"prj_b", "app/db", LimitProject},
		{"prj_a", "app-b/db", LimitPath},
		{"prj_b", "app-b/db", LimitProject},
	} {
		if got := Refuses(c, tc.project, tc.path); got != tc.want {
			t.Errorf("%s reading %s: refused by %q, want %q", tc.project, tc.path, got, tc.want)
		}
	}
}

// A reference set before its connection was narrowed stops when it is next
// read, with the problem that names it, and the manager is not asked.
func TestResolveRefusesAReferenceOutsideTheLimits(t *testing.T) {
	r, db, keyring, acme, _ := resolverFixture(t)
	f := newFakeVault(t)
	vault := connect(t, db, keyring, acme, "company-vault", KindVault, f.settings(), f.credentials())
	shop := store.Project{TeamID: acme, Name: "Shop", Slug: "shop"}
	blog := store.Project{TeamID: acme, Name: "Blog", Slug: "blog"}
	for _, p := range []*store.Project{&shop, &blog} {
		if err := db.CreateProject(t.Context(), p); err != nil {
			t.Fatal(err)
		}
	}
	wanted := []Wanted{{Variable: "STRIPE_KEY", Reference: store.SecretReference{ConnectionID: vault.ID, Path: "shop", Key: "stripe_key"}}}

	if _, err := r.Resolve(t.Context(), acme, blog.ID, wanted); err != nil {
		t.Fatalf("an unlimited connection: %v", err)
	}
	reads := f.reads.Load()

	narrow := func(paths, projects []string) {
		t.Helper()
		row, err := db.GetSecretConnection(t.Context(), vault.ID)
		if err != nil {
			t.Fatal(err)
		}
		row.AllowedPaths, row.AllowedProjectIDs = paths, projects
		if err := db.UpdateSecretConnection(t.Context(), &row.SecretConnection, ""); err != nil {
			t.Fatal(err)
		}
	}
	refused := func(projectID string, limit Limit, mentions ...string) {
		t.Helper()
		_, err := r.Resolve(t.Context(), acme, projectID, wanted)
		var problem *errdoc.Problem
		if !errors.As(err, &problem) || problem.Code != "secrets.reference_not_allowed" || problem.Status != 403 {
			t.Fatalf("answered %v", err)
		}
		if problem.Context["limit"] != string(limit) {
			t.Errorf("refused by %q, want %q", problem.Context["limit"], limit)
		}
		for _, want := range append([]string{"STRIPE_KEY", "company-vault"}, mentions...) {
			if !strings.Contains(problem.Cause, want) {
				t.Errorf("the cause does not name %s: %s", want, problem.Cause)
			}
		}
		if strings.Contains(problem.Text(), stripeKey) {
			t.Errorf("the problem carries a value: %s", problem.Text())
		}
	}

	narrow(nil, []string{shop.ID})
	refused(blog.ID, LimitProject, "Blog", "1 other project")
	// Other projects are counted, never named.
	if _, err := r.Resolve(t.Context(), acme, blog.ID, wanted); err != nil && strings.Contains(err.Error(), "Shop") {
		t.Errorf("the refusal names the project it is limited to: %v", err)
	}
	if values, err := r.Resolve(t.Context(), acme, shop.ID, wanted); err != nil || values["STRIPE_KEY"] != stripeKey {
		t.Errorf("the project it is limited to: %v, %v", values, err)
	}

	narrow([]string{"shop-admin", "billing"}, nil)
	before := f.reads.Load()
	refused(shop.ID, LimitPath, "shop-admin, billing")
	if f.reads.Load() != before {
		t.Error("the manager was asked for a path outside the limits")
	}

	narrow([]string{"shop"}, []string{shop.ID})
	if _, err := r.Resolve(t.Context(), acme, shop.ID, wanted); err != nil {
		t.Errorf("inside both limits: %v", err)
	}
	if f.reads.Load() <= reads {
		t.Error("an allowed reference was not read")
	}
}
