package api

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"skifity/internal/store"
)

// claimRecorder is a cluster that remembers which volume claims it was told to
// delete.
type claimRecorder struct {
	Cluster
	deleted *[]string
}

func (c claimRecorder) DeleteVolumeClaim(_ context.Context, namespace, name string) error {
	*c.deleted = append(*c.deleted, namespace+"/"+name)
	return nil
}

// Deleting a volume takes it off the app and deletes the disk, which is what
// the dialog asking for it says happens.
//
// Only the panel's row was deleted: the app went on mounting the disk until
// its next deploy, and the disk itself stayed for good, taking room and tying
// the app to the server it was on.
func TestDeletingAVolumeTakesItOffTheAppAndDeletesTheDisk(t *testing.T) {
	h, deployer := withCountingDeployer(t)
	var deleted []string
	h.api.cluster = claimRecorder{deleted: &deleted}
	acme := h.newTenant("acme")

	app := h.app(acme, "shop")
	volume := store.Volume{AppID: app.ID, Name: "uploads", MountPath: "/data", SizeGB: 1}
	if err := h.db.CreateVolume(t.Context(), &volume); err != nil {
		t.Fatal(err)
	}

	status, body := h.do(acme, http.MethodDelete, "/api/apps/"+app.ID+"/volumes/"+volume.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("deleting the volume answered %d: %s", status, body)
	}
	if deployer.syncs.Load() != 1 {
		t.Errorf("the app was applied %d times after its volume went, want once, so it stops mounting it", deployer.syncs.Load())
	}
	if want := []string{acme.env.Namespace + "/shop-uploads"}; !slices.Equal(deleted, want) {
		t.Errorf("deleted the claims %v, want %v", deleted, want)
	}
	if _, err := h.db.GetVolume(t.Context(), volume.ID); err == nil {
		t.Error("the volume is still listed")
	}
}

// An app that could not be applied without its volume keeps naming it, and its
// disk is kept rather than deleted from under it: an instance started from the
// old template would wait for a claim that is going.
func TestADiskIsKeptWhileTheAppStillNamesIt(t *testing.T) {
	h := newHarness(t)
	h.api.deployer = &failingSyncDeployer{fakeDeployer: fakeDeployer{log: &recorder{}}}
	var deleted []string
	h.api.cluster = claimRecorder{deleted: &deleted}
	acme := h.newTenant("acme")

	app := h.app(acme, "shop")
	volume := store.Volume{AppID: app.ID, Name: "uploads", MountPath: "/data", SizeGB: 1}
	if err := h.db.CreateVolume(t.Context(), &volume); err != nil {
		t.Fatal(err)
	}
	status, body := h.do(acme, http.MethodDelete, "/api/apps/"+app.ID+"/volumes/"+volume.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("deleting the volume answered %d: %s", status, body)
	}
	if len(deleted) != 0 {
		t.Errorf("deleted %v from under an app that still names it", deleted)
	}
}

// Another team's volume id deletes nothing, here or in the cluster.
func TestAVolumeOfAnotherTeamIsNotDeleted(t *testing.T) {
	h, deployer := withCountingDeployer(t)
	var deleted []string
	h.api.cluster = claimRecorder{deleted: &deleted}
	acme, globex := h.newTenant("acme"), h.newTenant("globex")

	theirs := h.app(globex, "theirs")
	volume := store.Volume{AppID: theirs.ID, Name: "uploads", MountPath: "/data", SizeGB: 1}
	if err := h.db.CreateVolume(t.Context(), &volume); err != nil {
		t.Fatal(err)
	}
	mine := h.app(acme, "mine")
	status, _ := h.do(acme, http.MethodDelete, "/api/apps/"+mine.ID+"/volumes/"+volume.ID, nil)
	if status != http.StatusNotFound {
		t.Errorf("deleting another team's volume through one's own app answered %d, want 404", status)
	}
	if len(deleted) != 0 || deployer.syncs.Load() != 0 {
		t.Errorf("deleted %v and applied %d times for a volume that is not this team's", deleted, deployer.syncs.Load())
	}
	if _, err := h.db.GetVolume(t.Context(), volume.ID); err != nil {
		t.Error("another team's volume was deleted")
	}
}
