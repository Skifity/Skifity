package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"skifity/internal/store"
)

// The route walks fill every child id with one that does not exist, so the
// check that a deployment, a domain or a backup belongs to the app or team in
// the path was never tried with an object that does exist, in another team.
// This walks every route with a child id, with the caller's own parent and
// the other team's real child: an owner of one team asking, through their own
// app, for the other team's deployment.
func TestAnotherTeamsObjectIsNotReachedThroughYourOwn(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	acme := h.newTenant("acme")
	globex := h.newTenant("globex")

	ours := map[string]string{
		"teamID":     acme.team.ID,
		"projectID":  acme.project.ID,
		"envID":      acme.env.ID,
		"appID":      h.app(acme, "web").ID,
		"databaseID": h.database(acme, "shop-db").ID,
		"serverID":   h.node(acme, "node-1").ID,
	}

	// Globex's own objects, one of each kind a route names below a parent.
	theirApp := h.app(globex, "web")
	theirDatabase := h.database(globex, "shop-db")
	deployment := store.Deployment{AppID: theirApp.ID, Status: store.DeployQueued, Image: "registry.internal/globex/web:1"}
	domain := store.Domain{AppID: theirApp.ID, Hostname: "shop.globex.example.test", Status: "active"}
	job := store.AppJob{AppID: theirApp.ID, Name: "nightly", Schedule: "0 3 * * *", Command: "true", Enabled: true}
	volume := store.Volume{AppID: theirApp.ID, Name: "data", MountPath: "/data", SizeGB: 1}
	source := store.GitSource{TeamID: globex.team.ID, Kind: "github_pat", Name: "globex-github"}
	channel := store.NotificationChannel{TeamID: globex.team.ID, Kind: "webhook", Name: "ops", ConfigEnc: "sealed"}
	invitation := store.Invitation{TeamID: globex.team.ID, Email: "new@globex.example.test", Role: store.RoleMember,
		ExpiresAt: time.Now().Add(time.Hour)}
	operation := store.Operation{TeamID: globex.team.ID, Kind: "server.add", TargetType: "server", TargetID: "srv_x",
		Status: store.OpRunning}
	for what, err := range map[string]error{
		"deployment": h.db.CreateDeployment(ctx, &deployment),
		"domain":     h.db.CreateDomain(ctx, &domain),
		"job":        h.db.CreateAppJob(ctx, &job),
		"volume":     h.db.CreateVolume(ctx, &volume),
		"source":     h.db.CreateGitSource(ctx, &source),
		"channel":    h.db.CreateNotificationChannel(ctx, &channel),
		"invitation": h.db.CreateInvitation(ctx, &invitation, "hash-of-an-invitation"),
		"operation":  h.db.CreateOperation(ctx, &operation, nil),
	} {
		if err != nil {
			t.Fatalf("create globex's %s: %v", what, err)
		}
	}
	volumeBackup := store.Backup{TargetType: "volume", TargetID: volume.ID, Status: "succeeded", Location: "v/1"}
	databaseBackup := store.Backup{TargetType: "database", TargetID: theirDatabase.ID, Status: "succeeded", Location: "d/1"}
	for _, backup := range []*store.Backup{&volumeBackup, &databaseBackup} {
		if err := h.db.CreateBackup(ctx, backup); err != nil {
			t.Fatal(err)
		}
	}
	tokens, _ := h.db.ListAPITokens(ctx, globex.user.ID)
	if len(tokens) == 0 {
		t.Fatal("globex has no token to aim at")
	}

	theirs := map[string]string{
		"deploymentID": deployment.ID,
		"domainID":     domain.ID,
		"jobID":        job.ID,
		"volumeID":     volume.ID,
		"sourceID":     source.ID,
		"channelID":    channel.ID,
		"invitationID": invitation.ID,
		"userID":       globex.user.ID,
		"operationID":  operation.ID,
		"tokenID":      tokens[0].ID,
	}
	backupFor := func(pattern string) string {
		if strings.Contains(pattern, "/volumes/") {
			return volumeBackup.ID
		}
		return databaseBackup.ID
	}

	checked := 0
	err := chi.Walk(h.api.router, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(pattern, "/api/webhooks/") {
			return nil // authenticated by a signature, not by a team
		}
		aimed := false
		path := placeholder.ReplaceAllStringFunc(pattern, func(match string) string {
			name := strings.Trim(match, "{}")
			if id, ok := theirs[name]; ok {
				aimed = true
				return id
			}
			if name == "backupID" {
				aimed = true
				return backupFor(pattern)
			}
			if id, ok := ours[name]; ok {
				return id
			}
			return "id_00000000000000000000"
		})
		if !aimed {
			return nil
		}
		checked++
		status, body := h.do(acme, method, path, map[string]any{})
		if status < 300 || status == http.StatusNotModified {
			t.Errorf("%s %s reached globex's object: %d\n%s", method, pattern, status, truncate(body, 200))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 25 {
		t.Fatalf("only %d routes were aimed at another team's object", checked)
	}

	// And nothing of theirs went.
	if _, err := h.db.GetGitSource(ctx, source.ID); err != nil {
		t.Errorf("globex's Git connection is gone: %v", err)
	}
	if _, err := h.db.GetNotificationChannel(ctx, channel.ID); err != nil {
		t.Errorf("globex's channel is gone: %v", err)
	}
	if _, err := h.db.GetVolume(ctx, volume.ID); err != nil {
		t.Errorf("globex's volume is gone: %v", err)
	}
	if after, _ := h.db.ListAPITokens(ctx, globex.user.ID); len(after) != len(tokens) {
		t.Errorf("globex's token is gone")
	}
	if got, _ := h.db.GetDeployment(ctx, deployment.ID); got.Status != store.DeployQueued {
		t.Errorf("globex's deployment was touched: %s", got.Status)
	}
}

// The live stream says what happens to whatever topics are asked for, so each
// is checked against the caller. Nothing tested that it was.
func TestAnotherTeamsEventsAreNotStreamed(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	acme := h.newTenant("acme")
	globex := h.newTenant("globex")
	theirApp := h.app(globex, "web")
	deployment := store.Deployment{AppID: theirApp.ID, Status: store.DeployQueued}
	operation := store.Operation{TeamID: globex.team.ID, Kind: "server.add", TargetType: "server", TargetID: "srv_x", Status: store.OpRunning}
	if err := h.db.CreateDeployment(ctx, &deployment); err != nil {
		t.Fatal(err)
	}
	if err := h.db.CreateOperation(ctx, &operation, nil); err != nil {
		t.Fatal(err)
	}
	for _, topics := range []string{
		"team:" + globex.team.ID,
		"deployment:" + deployment.ID,
		"operation:" + operation.ID,
		"app-logs:" + theirApp.ID,
		// One of your own does not carry one of theirs along with it.
		"team:" + acme.team.ID + ",app-logs:" + theirApp.ID,
	} {
		status, body := h.do(acme, http.MethodGet, "/api/events?topics="+topics, nil)
		if status != http.StatusNotFound && status != http.StatusForbidden {
			t.Errorf("asking for %s answered %d: %s", topics, status, truncate(body, 200))
		}
	}
}
