package deploy

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"skifity/internal/notify"
	"skifity/internal/store"
)

type projectNotifier struct {
	team, event string
	msg         notify.Message
}

func (p *projectNotifier) Notify(_ context.Context, teamID, event string, msg notify.Message) {
	p.team, p.event, p.msg = teamID, event, msg
}

// A deployment's notification names the app's project, so a channel limited
// to other projects is left out of it.
func TestADeploymentsNotificationNamesItsProject(t *testing.T) {
	ctx := t.Context()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	team := store.Team{Name: "Acme", Slug: "acme"}
	if err := db.CreateTeam(ctx, &team); err != nil {
		t.Fatal(err)
	}
	project := store.Project{TeamID: team.ID, Name: "Shop", Slug: "shop"}
	if err := db.CreateProject(ctx, &project); err != nil {
		t.Fatal(err)
	}
	env := store.Environment{ProjectID: project.ID, Name: "production", Slug: "production", Namespace: "acme-shop-production"}
	if err := db.CreateEnvironment(ctx, &env); err != nil {
		t.Fatal(err)
	}
	app := store.App{EnvironmentID: env.ID, Name: "web", Slug: "web", Replicas: 1}
	if err := db.CreateApp(ctx, &app); err != nil {
		t.Fatal(err)
	}

	notifier := &projectNotifier{}
	d := New(db, nil, nil, nil, notifier, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.notify(ctx, app, store.Deployment{CommitSHA: "abc1234def"}, notify.EventDeployFailed,
		notify.Message{Title: "Deploying web failed"})

	if notifier.team != team.ID || notifier.event != notify.EventDeployFailed || notifier.msg.ProjectID != project.ID {
		t.Fatalf("sent %q to %q about project %q; want %q about %q",
			notifier.event, notifier.team, notifier.msg.ProjectID, team.ID, project.ID)
	}
}
