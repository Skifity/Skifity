package provision

import (
	"context"
	"strings"
	"testing"

	"skifity/internal/api"
	"skifity/internal/settings"
	"skifity/internal/sshx"
	"skifity/internal/store"
)

// slowLink is what EtcdFitnessScript prints on a server 80 ms from the
// control plane server at 203.0.113.10, with a disk that syncs in 30 ms.
func slowLink() string {
	return strings.Repeat("etcd_peer=203.0.113.10 connect_us=80000\n", 20) +
		"disk_writes=200 disk_us=6000000\n"
}

func stepNamed(t *testing.T, op store.Operation, key string) store.OperationStep {
	t.Helper()
	for _, step := range op.Steps {
		if step.Key == key {
			return step
		}
	}
	t.Fatalf("the operation has no %q step: %+v", key, op.Steps)
	return store.OperationStep{}
}

func noteKeys(step store.OperationStep) []string {
	var keys []string
	for _, note := range step.Notes {
		keys = append(keys, note.Key)
	}
	return keys
}

// TestASlowControlPlaneServerIsWarnedAboutAndStillAdded: a control plane server
// is timed against the ones already there once it can reach them, the numbers
// over the thresholds are said under the network check, and the server is
// added all the same.
func TestASlowControlPlaneServerIsWarnedAboutAndStillAdded(t *testing.T) {
	p, db, _, teamID := testHarness(t)
	p.clusterVersion = func(context.Context) (string, error) { return "v1.33.4+k3s1", nil }
	first := store.Server{
		TeamID: teamID, Name: "node-1", Host: "203.0.113.10", SSHPort: 22, SSHUser: "root",
		Role: "control-plane", Status: store.ServerReady, ExternalIP: "203.0.113.10",
	}
	if err := db.CreateServer(t.Context(), &first); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}

	sshServer := healthyServer(t)
	sshServer.Respond("etcd_peer", slowLink(), 0)
	host, port := sshServer.Addr()
	op, err := p.AddServer(t.Context(), api.AddServerRequest{
		TeamID: teamID, Name: "node-2", Host: host, SSHPort: port, SSHUser: "root", Password: "hunter2",
		ControlPlane: true,
	})
	if err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	finished := waitForOperation(t, db, op.ID)
	if finished.Status != store.OpSucceeded {
		t.Fatalf("a warning stopped the server being added: %s %s", finished.ErrorCode, finished.ErrorMsg)
	}

	network := stepNamed(t, finished, StepConnectivity)
	keys := strings.Join(noteKeys(network), " ")
	for _, want := range []string{"preflight.etcd_round_trip.detail", "preflight.etcd_round_trip.fix",
		"preflight.etcd_disk_sync.detail", "preflight.etcd_disk_sync.fix"} {
		if !strings.Contains(keys, want) {
			t.Errorf("the network check does not say %s; it says %v", want, keys)
		}
	}
	for _, note := range network.Notes {
		if note.Key == "preflight.etcd_round_trip.detail" && strings.Join(note.Args, "|") != "203.0.113.10|80.0|50" {
			t.Errorf("the round trip warning carries %v", note.Args)
		}
	}

	// It was timed against the server that is there, and only after the
	// network check had reached it.
	var order []string
	for _, command := range sshServer.Commands() {
		switch {
		case strings.Contains(command, "reachable="):
			order = append(order, "network")
		case strings.Contains(command, "etcd_peer"):
			order = append(order, "etcd")
			if !strings.Contains(command, "'203.0.113.10'") {
				t.Errorf("the control plane server was not one of the peers timed:\n%s", command)
			}
		}
	}
	if strings.Join(order, ",") != "network,etcd" {
		t.Errorf("ran %v, want the network check and then the measurement", order)
	}
}

// A worker is not timed, and a panel whose thresholds are all zero does not
// time a control plane server either.
func TestNothingIsTimedWhenNothingWouldBeSaid(t *testing.T) {
	t.Run("a worker", func(t *testing.T) {
		p, db, _, teamID := testHarness(t)
		sshServer := healthyServer(t)
		host, port := sshServer.Addr()
		op, err := p.AddServer(t.Context(), api.AddServerRequest{
			TeamID: teamID, Name: "node-1", Host: host, SSHPort: port, SSHUser: "root", Password: "hunter2",
		})
		if err != nil {
			t.Fatalf("AddServer: %v", err)
		}
		if finished := waitForOperation(t, db, op.ID); finished.Status != store.OpSucceeded {
			t.Fatalf("adding failed: %s", finished.ErrorCode)
		}
		if sshServer.Ran("etcd_peer") {
			t.Fatal("a worker was timed for etcd, which it does not run")
		}
	})

	t.Run("every threshold at zero", func(t *testing.T) {
		p, db, _, teamID := testHarness(t)
		for _, key := range []string{settings.KeyEtcdRoundTripMs, settings.KeyEtcdJitterMs,
			settings.KeyEtcdLossPercent, settings.KeyEtcdDiskSyncMs} {
			if err := db.SetSetting(t.Context(), key, "0", false, "test"); err != nil {
				t.Fatal(err)
			}
		}
		sshServer := healthyServer(t)
		host, port := sshServer.Addr()
		op, err := p.AddServer(t.Context(), api.AddServerRequest{
			TeamID: teamID, Name: "node-1", Host: host, SSHPort: port, SSHUser: "root", Password: "hunter2",
			ControlPlane: true,
		})
		if err != nil {
			t.Fatalf("AddServer: %v", err)
		}
		if finished := waitForOperation(t, db, op.ID); finished.Status != store.OpSucceeded {
			t.Fatalf("adding failed: %s", finished.ErrorCode)
		}
		if sshServer.Ran("etcd_peer") {
			t.Fatal("the server was timed with every warning turned off")
		}
	})
}

// TestPromotingWarnsAndCarriesOn: a worker being promoted is in the cluster
// already, so it is timed right after its preflight, and what is found is
// said there. The promotion goes on to drain the server: a warning is not a
// refusal.
func TestPromotingWarnsAndCarriesOn(t *testing.T) {
	p, db, keyring, teamID := testHarness(t)
	p.clusterVersion = func(context.Context) (string, error) { return "v1.33.4+k3s1", nil }
	first := store.Server{
		TeamID: teamID, Name: "node-1", Host: "203.0.113.10", SSHPort: 22, SSHUser: "root",
		Role: "control-plane", Status: store.ServerReady, ExternalIP: "203.0.113.10",
	}
	if err := db.CreateServer(t.Context(), &first); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}

	pair, err := sshx.GenerateKeyPair("skifity-test")
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	sshServer, err := sshx.NewTestServer(sshx.TestServerOptions{AuthorizedKey: pair.PublicKey})
	if err != nil {
		t.Fatalf("NewTestServer: %v", err)
	}
	t.Cleanup(func() { sshServer.Close() })
	sshServer.Respond("/etc/os-release", realPreflightOutput, 0)
	sshServer.Respond("etcd_peer", slowLink(), 0)
	host, port := sshServer.Addr()

	worker := store.Server{
		TeamID: teamID, Name: "node-2", Host: host, SSHPort: port, SSHUser: "root",
		Role: "worker", Status: store.ServerReady,
	}
	if err := db.CreateServer(t.Context(), &worker); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	sealed, err := keyring.Seal([]byte(pair.PrivateKey), serverKeyContext(worker.ID))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	worker.SSHKeyEnc = sealed
	if err := db.UpdateServer(t.Context(), &worker); err != nil {
		t.Fatalf("UpdateServer: %v", err)
	}

	op, err := p.PromoteServer(t.Context(), worker.ID)
	if err != nil {
		t.Fatalf("PromoteServer: %v", err)
	}
	finished := waitForOperation(t, db, op.ID)

	preflight := stepNamed(t, finished, StepPreflight)
	if preflight.Status != store.StepSucceeded {
		t.Fatalf("the preflight of a promotion ended %s: %s", preflight.Status, preflight.Detail)
	}
	keys := strings.Join(noteKeys(preflight), " ")
	if !strings.Contains(keys, "preflight.etcd_round_trip.detail") || !strings.Contains(keys, "preflight.etcd_disk_sync.detail") {
		t.Fatalf("the promotion's preflight does not carry the warnings: %v", keys)
	}
	if drain := stepNamed(t, finished, "drain"); drain.Status == store.StepPending {
		t.Fatal("the promotion stopped at a warning")
	}
}
