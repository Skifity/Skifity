package deploy

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"skifity/internal/errdoc"
	"skifity/internal/notify"
	"skifity/internal/panelpath"
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
	d.notify(ctx, app, store.Deployment{ID: "dep_1", CommitSHA: "abc1234def"}, notify.EventDeployFailed,
		notify.Message{Title: "Deploying web failed"})

	if notifier.team != team.ID || notifier.event != notify.EventDeployFailed || notifier.msg.ProjectID != project.ID {
		t.Fatalf("sent %q to %q about project %q; want %q about %q",
			notifier.event, notifier.team, notifier.msg.ProjectID, team.ID, project.ID)
	}
	if want := "/apps/" + app.ID + "?tab=deployments&deployment=dep_1"; notifier.msg.Path != want {
		t.Errorf("the notification links to %q, want %q", notifier.msg.Path, want)
	}
}

// A failed deploy's notification opens that deployment's build, not the list
// it is in — where the one that opens is whichever is running — and says what
// to do apart from what went wrong, for the channels that show it on its own.
func TestAFailedDeploysNotificationOpensItsBuildAndSaysWhatToDo(t *testing.T) {
	d, db, app, _ := testDeployer(t)
	notifier := &recordingNotifier{}
	d.notifier = notifier

	bad := store.Deployment{AppID: app.ID, Status: store.DeployBuilding, Trigger: "manual"}
	if err := db.CreateDeployment(t.Context(), &bad); err != nil {
		t.Fatal(err)
	}
	problem := errdoc.BadRequest("the build failed")
	d.fail(t.Context(), bad, problem)

	sent := notifier.all()
	if len(sent) != 1 || sent[0].event != notify.EventDeployFailed {
		t.Fatalf("sent %+v, want one deploy.failed", sent)
	}
	msg := sent[0].msg
	if want := panelpath.AppDeployment(app.ID, bad.ID); msg.Path != want {
		t.Errorf("the notification links to %q, want %q", msg.Path, want)
	}
	if msg.Fix == "" || msg.Fix != problem.Fix || strings.Contains(msg.Body, problem.Fix) {
		t.Errorf("the fix is %q and the body %q; want the problem's fix, once, on its own", msg.Fix, msg.Body)
	}
}
