package dbsvc

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"skifity/internal/api"
	"skifity/internal/cluster"
	"skifity/internal/crypto"
	"skifity/internal/dbsvc/engine"
	"skifity/internal/errdoc"
	"skifity/internal/events"
	"skifity/internal/kube"
	"skifity/internal/store"
)

// A database's life, against a cluster made of fakes: the typed client holds
// the StatefulSets, claims, storage classes and jobs, and the dynamic one the
// applies and CloudNativePG's Cluster.

// newPassword is the one a change gives, looked for wherever a password
// must not be, beside plantedPassword.
const newPassword = "planted-new-password-9c2e"

type syncRecorder struct {
	mu     sync.Mutex
	synced []string
	fail   map[string]bool
}

func (s *syncRecorder) Sync(_ context.Context, appID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail[appID] {
		return errors.New("the rollout did not finish")
	}
	s.synced = append(s.synced, appID)
	return nil
}

func (s *syncRecorder) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.synced...)
}

// lifeHarness is one database of an engine, running, with an app linked to
// it, in a cluster of fakes.
type lifeHarness struct {
	t         *testing.T
	m         *Manager
	db        *store.DB
	keyring   *crypto.Keyring
	env       store.Environment
	app       store.App
	record    store.Database
	spec      Spec
	clientset *fake.Clientset
	dynamic   *dynamicfake.FakeDynamicClient
	deployer  *syncRecorder

	mu      sync.Mutex
	applied []*unstructured.Unstructured
	merged  []string
	// failJob says whether a job of this name fails; nil, none does.
	failJob func(name string) bool
	// refuseApply refuses an apply; nil refuses none.
	refuseApply func(obj *unstructured.Unstructured) bool
}

func newLifeHarness(t *testing.T, engineName string, class *storagev1.StorageClass) *lifeHarness {
	t.Helper()
	ctx := t.Context()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	key, _ := crypto.GenerateKey()
	keyring, err := crypto.NewKeyring("k1", key)
	if err != nil {
		t.Fatal(err)
	}
	team := store.Team{Name: "Acme", Slug: "acme"}
	if err := db.CreateTeam(ctx, &team); err != nil {
		t.Fatal(err)
	}
	project := store.Project{TeamID: team.ID, Name: "Shop", Slug: "shop"}
	if err := db.CreateProject(ctx, &project); err != nil {
		t.Fatal(err)
	}
	env := store.Environment{ProjectID: project.ID, Name: "Production", Slug: "production", Kind: "standard",
		Namespace: "acme-shop-production"}
	if err := db.CreateEnvironment(ctx, &env); err != nil {
		t.Fatal(err)
	}
	app := store.App{EnvironmentID: env.ID, Name: "web", Slug: "web", Replicas: 1}
	if err := db.CreateApp(ctx, &app); err != nil {
		t.Fatal(err)
	}

	h := &lifeHarness{t: t, db: db, keyring: keyring, env: env, app: app, deployer: &syncRecorder{fail: map[string]bool{}}}
	record := store.Database{EnvironmentID: env.ID, Name: "orders", Slug: "orders", Engine: engineName, Status: "running",
		Instances: 1, StorageGB: 5}
	kind := mustEngine(t, engineName)
	record.CPURequestM, record.MemRequestMB, record.MemLimitMB = kind.CPURequestM, kind.MemRequestMB, kind.MemLimitMB
	if !kind.Storage {
		record.StorageGB = 0
	}
	if err := db.CreateDatabase(ctx, &record); err != nil {
		t.Fatal(err)
	}
	m := New(db, keyring, events.NewHub(16), nil, h.deployer, slog.New(slog.DiscardHandler))
	m.poll = 10 * time.Millisecond
	h.m = m
	spec := m.specFor(record, env)
	spec.Password = plantedPassword
	if !kind.Password {
		spec.Password = ""
	}
	sealed, err := m.sealCredentials(credentialsFor(spec), credentialsContext(record.ID))
	if err != nil {
		t.Fatal(err)
	}
	record.CredentialsEnc = sealed
	if err := db.UpdateDatabase(ctx, &record); err != nil {
		t.Fatal(err)
	}
	if err := db.LinkDatabase(ctx, record.ID, app.ID, kind.Variable); err != nil {
		t.Fatal(err)
	}
	h.record, h.spec = record, spec

	// The cluster as Create left it.
	var objects []runtime.Object
	var dynamicObjects []runtime.Object
	built, err := Build(spec)
	if err != nil {
		t.Fatal(err)
	}
	claimClass := ""
	if class != nil {
		objects = append(objects, class)
		claimClass = class.Name
	}
	for _, object := range built {
		switch typed := object.(type) {
		case *appsv1.StatefulSet:
			typed.Status.ReadyReplicas = 1
			typed.Status.Replicas = 1
			objects = append(objects, typed)
		case *unstructured.Unstructured:
			dynamicObjects = append(dynamicObjects, typed)
		}
	}
	if kind.Storage {
		labels := map[string]string{"app.kubernetes.io/name": "orders"}
		name := "data-orders-0"
		if engineName == EnginePostgres {
			labels = map[string]string{"cnpg.io/cluster": "orders"}
			name = "orders-1"
		}
		claim := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: env.Namespace, Labels: labels},
			Spec: corev1.PersistentVolumeClaimSpec{
				Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("5Gi"),
				}},
			},
		}
		if claimClass != "" {
			claim.Spec.StorageClassName = &claimClass
		}
		objects = append(objects, claim)
	}
	h.clientset = fake.NewSimpleClientset(objects...)
	h.clientset.PrependReactor("get", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		name := action.(k8stesting.GetAction).GetName()
		job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: env.Namespace}}
		if h.failJob != nil && h.failJob(name) {
			job.Status.Failed = 1
		} else {
			job.Status.Succeeded = 1
		}
		return true, job, nil
	})
	h.dynamic = dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		{Group: "postgresql.cnpg.io", Version: "v1", Resource: "clusters"}: "ClusterList",
	}, dynamicObjects...)
	h.dynamic.PrependReactor("patch", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patch := action.(k8stesting.PatchAction)
		if patch.GetPatchType() == types.MergePatchType {
			h.mu.Lock()
			h.merged = append(h.merged, string(patch.GetPatch()))
			h.mu.Unlock()
			return false, nil, nil
		}
		obj := &unstructured.Unstructured{}
		if err := obj.UnmarshalJSON(patch.GetPatch()); err != nil {
			return true, nil, err
		}
		if h.refuseApply != nil && h.refuseApply(obj) {
			return true, nil, errors.New("the API server refused")
		}
		h.mu.Lock()
		h.applied = append(h.applied, obj)
		h.mu.Unlock()
		return true, obj, nil
	})
	m.cluster = cluster.New(kube.NewClientWith(h.clientset, h.dynamic, ""), db, keyring, slog.New(slog.DiscardHandler))
	return h
}

func mustEngine(t *testing.T, name string) engine.Engine {
	t.Helper()
	kind, ok := engine.Lookup(name)
	if !ok {
		t.Fatalf("no engine %s", name)
	}
	return kind
}

func (h *lifeHarness) statefulSet() *appsv1.StatefulSet {
	h.t.Helper()
	set, err := h.clientset.AppsV1().StatefulSets(h.env.Namespace).Get(h.t.Context(), "orders", metav1.GetOptions{})
	if err != nil {
		h.t.Fatal(err)
	}
	return set
}

func (h *lifeHarness) reload() store.Database {
	h.t.Helper()
	record, err := h.db.GetDatabase(h.t.Context(), h.record.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	return record
}

func (h *lifeHarness) appliedOf(kind string) []*unstructured.Unstructured {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []*unstructured.Unstructured
	for _, obj := range h.applied {
		if obj.GetKind() == kind {
			out = append(out, obj)
		}
	}
	return out
}

func codeOf(err error) string {
	var problem *errdoc.Problem
	if errors.As(err, &problem) {
		return problem.Code
	}
	return ""
}

func expandable(allowed bool) *storagev1.StorageClass {
	return &storagev1.StorageClass{
		ObjectMeta:           metav1.ObjectMeta{Name: "fast", Annotations: map[string]string{"storageclass.kubernetes.io/is-default-class": "true"}},
		Provisioner:          "example.com/csi",
		AllowVolumeExpansion: &allowed,
	}
}

// Stopping scales to nothing and keeps the claim; the status says stopped,
// read back from the cluster as well as recorded; starting scales back and
// waits for it.
func TestAStoppedDatabaseKeepsItsDiskAndStartsAgain(t *testing.T) {
	h := newLifeHarness(t, EngineMySQL, expandable(true))
	ctx := t.Context()

	stopped, err := h.m.Stop(ctx, h.record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Status != StatusStopped {
		t.Errorf("a stopped database says %s", stopped.Status)
	}
	if set := h.statefulSet(); *set.Spec.Replicas != 0 || len(set.Spec.VolumeClaimTemplates) != 1 {
		t.Errorf("stopped, the StatefulSet has %d replicas and %d claim templates", *set.Spec.Replicas, len(set.Spec.VolumeClaimTemplates))
	}
	if _, err := h.clientset.CoreV1().PersistentVolumeClaims(h.env.Namespace).Get(ctx, "data-orders-0", metav1.GetOptions{}); err != nil {
		t.Errorf("stopping took the disk: %v", err)
	}
	if status, _, _ := h.m.Status(ctx, h.record.ID); status != StatusStopped {
		t.Errorf("read from the cluster, a stopped database is %s", status)
	}
	// Twice is the same as once.
	if _, err := h.m.Stop(ctx, h.record.ID); err != nil {
		t.Errorf("stopping a stopped database: %v", err)
	}

	started, err := h.m.Start(ctx, h.record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if started.Status != "starting" {
		t.Errorf("a starting database says %s", started.Status)
	}
	if set := h.statefulSet(); *set.Spec.Replicas != 1 {
		t.Errorf("started, the StatefulSet has %d replicas", *set.Spec.Replicas)
	}
	deadline := time.Now().Add(5 * time.Second)
	for h.reload().Status != "running" {
		if time.Now().After(deadline) {
			t.Fatalf("the started database is %s", h.reload().Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// PostgreSQL is hibernated the way CloudNativePG does it, by an annotation,
// and comes back by the same one.
func TestAStoppedPostgresIsHibernated(t *testing.T) {
	h := newLifeHarness(t, EnginePostgres, expandable(true))
	ctx := t.Context()
	if _, err := h.m.Stop(ctx, h.record.ID); err != nil {
		t.Fatal(err)
	}
	cluster, err := h.m.cluster.Client().Applier().Get(ctx, "postgresql.cnpg.io/v1", "Cluster", h.env.Namespace, "orders")
	if err != nil {
		t.Fatal(err)
	}
	if cluster.GetAnnotations()[hibernationAnnotation] != "on" {
		t.Fatalf("the Cluster's annotations are %v", cluster.GetAnnotations())
	}
	if status, _ := interpretCNPGStatus(cluster); status != StatusStopped {
		t.Errorf("a hibernated cluster reads as %s", status)
	}
	if _, err := h.m.Start(ctx, h.record.ID); err != nil {
		t.Fatal(err)
	}
	cluster, _ = h.m.cluster.Client().Applier().Get(ctx, "postgresql.cnpg.io/v1", "Cluster", h.env.Namespace, "orders")
	if cluster.GetAnnotations()[hibernationAnnotation] != "off" {
		t.Errorf("a started cluster's annotations are %v", cluster.GetAnnotations())
	}
}

func ptrInt(v int) *int { return &v }

// What a resize asks for is checked before anything is asked of a cluster.
func TestAResizeIsCheckedBeforeItIsMade(t *testing.T) {
	record := store.Database{Name: "orders", Engine: EngineMySQL, StorageGB: 10, CPURequestM: 100,
		MemRequestMB: 256, MemLimitMB: 1024}
	cases := []struct {
		name string
		req  api.ResizeDatabaseRequest
		code string
	}{
		{"a smaller disk", api.ResizeDatabaseRequest{StorageGB: ptrInt(5)}, "database.storage_shrink"},
		{"too little memory", api.ResizeDatabaseRequest{MemLimitMB: ptrInt(256)}, "database.memory_too_small"},
		{"a limit below the request", api.ResizeDatabaseRequest{MemRequestMB: ptrInt(2048)}, "request.invalid"},
		{"a CPU limit below its request", api.ResizeDatabaseRequest{CPULimitM: ptrInt(50)}, "request.invalid"},
		{"almost no CPU", api.ResizeDatabaseRequest{CPURequestM: ptrInt(1)}, "request.invalid"},
	}
	for _, c := range cases {
		if _, err := PlanResize(record, c.req); codeOf(err) != c.code {
			t.Errorf("%s: %v, want %s", c.name, err, c.code)
		}
	}
	cache := store.Database{Name: "cache", Engine: EngineMemcached, CPURequestM: 50, MemRequestMB: 64, MemLimitMB: 256}
	if _, err := PlanResize(cache, api.ResizeDatabaseRequest{StorageGB: ptrInt(5)}); codeOf(err) != "request.invalid" {
		t.Errorf("a cache was given a disk: %v", err)
	}
	// A row from before the limit was recorded has the engine's.
	old := record
	old.MemLimitMB = 0
	if want, err := PlanResize(old, api.ResizeDatabaseRequest{StorageGB: ptrInt(20)}); err != nil || want.MemLimitMB != 1024 || want.StorageGB != 20 {
		t.Errorf("an older row's resize plans %+v, %v", want, err)
	}
}

// The container's resources and the arguments sized from them change; the
// claim templates, the replica count and the rest do not; the claim grows.
func TestAResizeChangesTheContainerAndGrowsTheClaim(t *testing.T) {
	h := newLifeHarness(t, EngineDragonfly, expandable(true))
	ctx := t.Context()
	before := h.statefulSet()

	resized, err := h.m.Resize(ctx, h.record.ID, api.ResizeDatabaseRequest{
		CPURequestM: ptrInt(500), CPULimitM: ptrInt(2000), MemLimitMB: ptrInt(4096), StorageGB: ptrInt(20),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resized.CPURequestM != 500 || resized.CPULimitM != 2000 || resized.MemLimitMB != 4096 || resized.StorageGB != 20 {
		t.Errorf("the record says %+v", resized)
	}
	after := h.statefulSet()
	container := after.Spec.Template.Spec.Containers[0]
	if got := container.Resources.Limits[corev1.ResourceMemory]; got.Value() != 4096<<20 {
		t.Errorf("the memory limit is %s", got.String())
	}
	if got := container.Resources.Limits[corev1.ResourceCPU]; got.String() != "2" {
		t.Errorf("the CPU limit is %s", got.String())
	}
	if !strings.Contains(strings.Join(container.Args, " "), "--maxmemory=3221225472") {
		t.Errorf("Dragonfly's maxmemory did not follow its limit: %v", container.Args)
	}
	if *after.Spec.Replicas != *before.Spec.Replicas {
		t.Error("a resize changed the replica count")
	}
	beforeClaims, _ := json.Marshal(before.Spec.VolumeClaimTemplates)
	afterClaims, _ := json.Marshal(after.Spec.VolumeClaimTemplates)
	if string(beforeClaims) != string(afterClaims) {
		t.Error("a resize changed the claim templates, which the API server refuses")
	}
	claim, err := h.clientset.CoreV1().PersistentVolumeClaims(h.env.Namespace).Get(ctx, "data-orders-0", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := claim.Spec.Resources.Requests[corev1.ResourceStorage]; got.String() != "20Gi" {
		t.Errorf("the claim asks for %s", got.String())
	}
}

// A disk whose class cannot grow refuses the whole request — the CPU and
// memory asked for with it too — and says which class.
func TestADiskThatCannotGrowRefusesTheWholeResize(t *testing.T) {
	for _, class := range []*storagev1.StorageClass{
		expandable(false),
		{ObjectMeta: metav1.ObjectMeta{Name: "local-path",
			Annotations: map[string]string{"storageclass.kubernetes.io/is-default-class": "true"}},
			Provisioner: "rancher.io/local-path"},
	} {
		h := newLifeHarness(t, EngineMySQL, class)
		before := h.statefulSet()
		_, err := h.m.Resize(t.Context(), h.record.ID, api.ResizeDatabaseRequest{
			MemLimitMB: ptrInt(2048), StorageGB: ptrInt(20),
		})
		if codeOf(err) != "database.storage_not_expandable" || !strings.Contains(err.Error(), class.Name) {
			t.Fatalf("%s: %v", class.Name, err)
		}
		after := h.statefulSet()
		if after.Spec.Template.Spec.Containers[0].Resources.Limits.Memory().String() !=
			before.Spec.Template.Spec.Containers[0].Resources.Limits.Memory().String() {
			t.Errorf("%s: the memory changed although the disk was refused", class.Name)
		}
		if record := h.reload(); record.StorageGB != 5 || record.MemLimitMB != 1024 {
			t.Errorf("%s: the record changed: %+v", class.Name, record)
		}
	}
}

// A resize the environment's quota would refuse is refused here, with the
// limit named; what the database uses now is not counted twice.
func TestAResizeOverTheQuotaIsRefused(t *testing.T) {
	h := newLifeHarness(t, EngineMySQL, expandable(true))
	quota := &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{Name: "environment", Namespace: h.env.Namespace},
		Status: corev1.ResourceQuotaStatus{
			Hard: corev1.ResourceList{"limits.memory": resource.MustParse("4Gi"), "requests.memory": resource.MustParse("2Gi")},
			Used: corev1.ResourceList{"limits.memory": resource.MustParse("3Gi"), "requests.memory": resource.MustParse("1Gi")},
		},
	}
	if _, err := h.clientset.CoreV1().ResourceQuotas(h.env.Namespace).Create(t.Context(), quota, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	// 3 GB used, 1 of it this database's: 2 more fits, 3 more does not.
	if _, err := h.m.Resize(t.Context(), h.record.ID, api.ResizeDatabaseRequest{MemLimitMB: ptrInt(2048)}); err != nil {
		t.Fatalf("a resize within the quota: %v", err)
	}
	_, err := h.m.Resize(t.Context(), h.record.ID, api.ResizeDatabaseRequest{MemLimitMB: ptrInt(4096)})
	if codeOf(err) != "database.over_quota" || !strings.Contains(err.Error(), "limits.memory") {
		t.Fatalf("over the quota: %v", err)
	}
}

// PostgreSQL is resized through its Cluster, in one merge patch the operator
// rolls out, with the parameters sized from the memory and a CPU limit taken
// away by naming it null.
func TestAPostgresResizeIsOnePatchOfItsCluster(t *testing.T) {
	h := newLifeHarness(t, EnginePostgres, expandable(true))
	if _, err := h.m.Resize(t.Context(), h.record.ID, api.ResizeDatabaseRequest{
		MemRequestMB: ptrInt(1024), MemLimitMB: ptrInt(4096), StorageGB: ptrInt(50),
	}); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	merged := strings.Join(h.merged, "\n")
	h.mu.Unlock()
	for _, want := range []string{`"memory":"4096Mi"`, `"cpu":null`, `"shared_buffers":"256MB"`,
		`"effective_cache_size":"2048MB"`, `"size":"50Gi"`} {
		if !strings.Contains(merged, want) {
			t.Errorf("the patch does not say %s:\n%s", want, merged)
		}
	}
	cluster, _ := h.m.cluster.Client().Applier().Get(t.Context(), "postgresql.cnpg.io/v1", "Cluster", h.env.Namespace, "orders")
	if size, _, _ := unstructured.NestedString(cluster.Object, "spec", "storage", "size"); size != "50Gi" {
		t.Errorf("the Cluster's storage is %s", size)
	}
}
