package dbsvc

import (
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"skifity/internal/api"
	"skifity/internal/errdoc"
)

// On a cluster where a server runs a kernel MongoDB 8 stops on, a new MongoDB
// gets 7.0, and one asked for at 8.0 is refused with the reason, before
// anything is made. The two-server crosscheck's first runner on Ubuntu's 7.0
// kernel left a MongoDB 8 in a crash loop that said nothing to the panel.
func TestAMongoDBIsMadeAtAVersionTheServersStart(t *testing.T) {
	h := newLifeHarness(t, EngineRedis, nil)
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "second"},
		Status:     corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{KernelVersion: "7.0.0-1017-azure"}},
	}
	if _, err := h.clientset.CoreV1().Nodes().Create(t.Context(), node, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	_, err := h.m.Create(t.Context(), h.env, api.CreateDatabaseRequest{Name: "documents", Engine: EngineMongoDB, Version: "8.0"})
	var problem *errdoc.Problem
	if !errors.As(err, &problem) || problem.Code != "database.kernel_refused" {
		t.Fatalf("asking for MongoDB 8 on a 7.0 kernel answered %v, want database.kernel_refused", err)
	}
	if databases, _ := h.db.ListDatabases(t.Context(), h.env.ID); len(databases) != 1 {
		t.Errorf("there are %d databases after a refusal, want only the one there was", len(databases))
	}

	record, err := h.m.Create(t.Context(), h.env, api.CreateDatabaseRequest{Name: "documents", Engine: EngineMongoDB})
	if err != nil {
		t.Fatalf("a MongoDB with no version asked for was refused: %v", err)
	}
	if record.EngineVersion != "7.0" {
		t.Errorf("a new MongoDB got %s on a 7.0 kernel, want 7.0", record.EngineVersion)
	}
}
