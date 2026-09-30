package cluster

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"skifity/internal/crypto"
	"skifity/internal/kube"
	"skifity/internal/store"
)

// An app's events are its own — its instances, its worker's, its volume's —
// and a secret the app holds never comes back in one.
func TestAnAppsEventsAreItsOwnAndKeepItsSecrets(t *testing.T) {
	_, db, app, env, _ := autoDomainFixture(t)
	key, _ := crypto.GenerateKey()
	keyring, err := crypto.NewKeyring("k1", key)
	if err != nil {
		t.Fatal(err)
	}
	const secret = "fake-database-password-4242"
	sealed, err := keyring.Seal([]byte(secret), "variable:"+app.ID+":DATABASE_PASSWORD")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetVariable(t.Context(), &store.Variable{AppID: app.ID, Key: "DATABASE_PASSWORD", IsSecret: true}, sealed); err != nil {
		t.Fatal(err)
	}
	if err := db.SetProcess(t.Context(), &store.AppProcess{AppID: app.ID, Name: "worker", Command: "bin/work", Instances: 1}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	event := func(name, kind, object, reason, message string) *corev1.Event {
		return &corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: name, Namespace: env.Namespace},
			InvolvedObject: corev1.ObjectReference{Kind: kind, Name: object},
			Type:           corev1.EventTypeWarning, Reason: reason, Message: message, Count: 1,
			LastTimestamp: metav1.NewTime(now),
		}
	}
	clientset := fake.NewSimpleClientset(
		event("a", "Pod", "web-7d4f8b9c5-x2x9q", "Unhealthy",
			"Readiness probe failed: postgres://shop:"+secret+"@db:5432/shop refused, password "+secret),
		event("b", "Pod", kube.ProcessDeploymentName("web", "worker")+"-5c8d7f6b4-q8w2e", "FailedMount",
			`MountVolume.SetUp failed for volume "files" : secret "web-files" not found`),
		event("c", "Pod", "api-6b9f7c8d4-zzzzz", "BackOff", "another app's"),
	)
	c := New(kube.NewClientWith(clientset, nil, ""), db, keyring, slog.New(slog.NewTextHandler(io.Discard, nil)))

	events, err := c.AppEvents(t.Context(), app, env)
	if err != nil {
		t.Fatalf("AppEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("want the app's and its worker's events, got %+v", events)
	}
	for _, e := range events {
		if strings.Contains(e.Message, secret) || strings.Contains(e.Explanation, secret) {
			t.Errorf("a secret came back in an event: %+v", e)
		}
		if strings.Contains(e.Message, "another app") {
			t.Errorf("another app's event was listed: %+v", e)
		}
	}
	var mount *struct{ code, text string }
	for _, e := range events {
		if e.Reason == "FailedMount" {
			mount = &struct{ code, text string }{e.ExplanationCode, e.Explanation}
		}
	}
	if mount == nil || mount.code != "mount_config_missing" || !strings.Contains(mount.text, "web-files") {
		t.Fatalf("the missing secret is explained as %+v", mount)
	}
}

func TestADatabasesEventsAreItsOwn(t *testing.T) {
	_, db, _, env, _ := autoDomainFixture(t)
	record := store.Database{EnvironmentID: env.ID, Name: "shop-db", Slug: "shop-db", Engine: "mysql", Status: "running"}
	if err := db.CreateDatabase(t.Context(), &record); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	event := func(name, kind, object string) *corev1.Event {
		return &corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: name, Namespace: env.Namespace},
			InvolvedObject: corev1.ObjectReference{Kind: kind, Name: object},
			Type:           corev1.EventTypeWarning, Reason: "FailedScheduling",
			Message: "0/1 nodes are available: 1 Insufficient memory.", Count: 1, LastTimestamp: metav1.NewTime(now),
		}
	}
	clientset := fake.NewSimpleClientset(
		event("a", "Pod", "shop-db-0"),
		event("b", "PersistentVolumeClaim", "data-shop-db-0"),
		event("c", "StatefulSet", "shop-db"),
		event("d", "Pod", "web-7d4f8b9c5-x2x9q"),
	)
	c := New(kube.NewClientWith(clientset, nil, ""), db, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	events, err := c.DatabaseEvents(t.Context(), record, env)
	if err != nil {
		t.Fatalf("DatabaseEvents: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("want the database's three events, got %+v", events)
	}
	for _, e := range events {
		if e.Name == "web-7d4f8b9c5-x2x9q" {
			t.Errorf("an app's event was listed as the database's")
		}
		if e.ExplanationCode != "scheduling_memory" {
			t.Errorf("%s/%s is explained as %q", e.Kind, e.Name, e.ExplanationCode)
		}
	}
}
