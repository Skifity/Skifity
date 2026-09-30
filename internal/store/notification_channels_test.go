package store

import (
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
)

// The kind column named the four channels there were at the start, and every
// kind added after them — Slack, Mattermost, ntfy, Pushover, and each one a
// plugin provides — was refused by the database when somebody saved it.
// 0043 rebuilds the table without that list and adds the switch that limits a
// channel to some projects. This puts a channel in as it was before, runs the
// migration, and checks it is still there and that the rest now fit.
func TestChannelsSurviveTheRebuildAndEveryKindFits(t *testing.T) {
	ctx := t.Context()
	sqlDB, err := sql.Open("sqlite", fileDSN(filepath.Join(t.TempDir(), "panel.db")))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	db := &DB{DB: sqlDB, path: "panel.db"}
	t.Cleanup(func() { db.Close() })

	if err := db.migrateUpTo(ctx, 42); err != nil {
		t.Fatalf("migrate to 42: %v", err)
	}
	team := Team{Name: "Acme", Slug: "acme"}
	if err := db.CreateTeam(ctx, &team); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO notification_channels
		(id, team_id, kind, name, config_enc, events, enabled, created_at, updated_at)
		VALUES ('ntf_old', ?, 'discord', 'ops', 'SKF1.sealed', 'deploy.failed', 1, ?, ?)`,
		team.ID, Now(), Now()); err != nil {
		t.Fatalf("insert a channel as it was: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO notification_channels
		(id, team_id, kind, name, config_enc, created_at, updated_at)
		VALUES ('ntf_slack', ?, 'slack', 'chat', '', ?, ?)`, team.ID, Now(), Now()); err == nil {
		t.Fatal("the schema before 0043 already takes a Slack channel, so this test is not testing the rebuild")
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	old, err := db.GetNotificationChannel(ctx, "ntf_old")
	if err != nil || old.Kind != "discord" || old.Name != "ops" || old.ConfigEnc != "SKF1.sealed" ||
		old.Events != "deploy.failed" || !old.Enabled || old.Scoped {
		t.Fatalf("the channel did not survive the rebuild intact: %+v, %v", old, err)
	}
	for _, kind := range []string{"slack", "mattermost", "ntfy", "pushover", "teams", "gotify", "plugin:com.example.chat/rooms"} {
		channel := NotificationChannel{TeamID: team.ID, Kind: kind, Name: kind, ConfigEnc: "SKF1.sealed", Enabled: true}
		if err := db.CreateNotificationChannel(ctx, &channel); err != nil {
			t.Errorf("a %s channel cannot be stored: %v", kind, err)
		}
	}

	// The team's cascade still reaches the rebuilt table.
	if _, err := db.ExecContext(ctx, `DELETE FROM teams WHERE id = ?`, team.ID); err != nil {
		t.Fatal(err)
	}
	if left, err := db.ListNotificationChannels(ctx, team.ID); err != nil || len(left) != 0 {
		t.Fatalf("a deleted team left %d channels behind: %v", len(left), err)
	}
}

// A channel limited to projects keeps its limit when the projects go: with
// none left it hears only what belongs to no project, and never goes back to
// hearing every project.
func TestAChannelsProjectsAreItsTeamsAndOutliveNothing(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	team := Team{Name: "Acme", Slug: "acme"}
	other := Team{Name: "Globex", Slug: "globex"}
	for _, tm := range []*Team{&team, &other} {
		if err := db.CreateTeam(ctx, tm); err != nil {
			t.Fatal(err)
		}
	}
	shop := Project{TeamID: team.ID, Name: "Shop", Slug: "shop"}
	blog := Project{TeamID: team.ID, Name: "Blog", Slug: "blog"}
	theirs := Project{TeamID: other.ID, Name: "Theirs", Slug: "theirs"}
	for _, p := range []*Project{&shop, &blog, &theirs} {
		if err := db.CreateProject(ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	channel := NotificationChannel{
		TeamID: team.ID, Kind: "webhook", Name: "ops", ConfigEnc: "SKF1.sealed", Enabled: true,
		Scoped: true, Projects: []string{shop.ID, theirs.ID},
	}
	if err := db.CreateNotificationChannel(ctx, &channel); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetNotificationChannel(ctx, channel.ID)
	if err != nil || !got.Scoped || !slices.Equal(got.Projects, []string{shop.ID}) {
		t.Fatalf("stored %+v, %v; want the team's own project and not the other team's", got, err)
	}

	got.Projects = []string{blog.ID, shop.ID}
	if err := db.UpdateNotificationChannel(ctx, &got); err != nil {
		t.Fatal(err)
	}
	listed, err := db.ListNotificationChannels(ctx, team.ID)
	if err != nil || len(listed) != 1 || !slices.Equal(listed[0].Projects, []string{blog.ID, shop.ID}) {
		t.Fatalf("listed %+v, %v", listed, err)
	}

	for _, p := range []Project{shop, blog} {
		if _, err := db.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	got, err = db.GetNotificationChannel(ctx, channel.ID)
	if err != nil || !got.Scoped || len(got.Projects) != 0 {
		t.Fatalf("after its projects were deleted the channel is %+v, %v; want it still limited, to none", got, err)
	}

	// Lifting the limit forgets the list.
	got.Scoped, got.Projects = false, nil
	if err := db.UpdateNotificationChannel(ctx, &got); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.GetNotificationChannel(ctx, channel.ID); got.Scoped || len(got.Projects) != 0 {
		t.Fatalf("an unlimited channel is %+v", got)
	}
}
