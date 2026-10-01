package dbsvc

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"skifity/internal/api"
	"skifity/internal/dbsvc/engine"
	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/runsafe"
	"skifity/internal/store"
)

// A database's life after it is created: stopped and started, resized, and
// (in password.go) given a new password.
//
// Nothing here rebuilds a database. A StatefulSet's volume claim templates
// cannot be changed once it exists — the API server refuses the update — so a
// resize changes the one container's resources and arguments in place and
// grows the claims themselves, and a stop or start changes the replica count
// and nothing else. PostgreSQL is CloudNativePG's, and is changed through its
// Cluster, which the operator then rolls out one instance at a time.

// hibernationAnnotation is CloudNativePG's declarative hibernation: "on" and
// the operator removes the cluster's instances and keeps their volumes; "off"
// and it brings them back on the same volumes.
const hibernationAnnotation = "cnpg.io/hibernation"

// StatusStopped is a database with no instances and its disk kept.
const StatusStopped = "stopped"

// StartingAgain is what a database being started after a stop says while it
// starts. A restart of the panel in that minute is not a database half
// created, which is what "starting" otherwise means to the work it marks as
// interrupted (serverapp): its data is all there, and the next look at it
// reads its state from the cluster.
const StartingAgain = "Starting again on the disk it kept."

// Stop scales a database to nothing and keeps its volume. It is idempotent:
// stopping a stopped database changes nothing.
func (m *Manager) Stop(ctx context.Context, databaseID string) (store.Database, error) {
	record, env, err := m.locate(ctx, databaseID)
	if err != nil {
		return store.Database{}, err
	}
	if record.Status == "creating" {
		return store.Database{}, errdoc.DatabaseBusy(record.Name)
	}
	if record.Engine == EnginePostgres {
		err = m.patchCluster(ctx, env.Namespace, record.Slug, map[string]any{
			"metadata": map[string]any{"annotations": map[string]any{hibernationAnnotation: "on"}},
		})
	} else {
		err = m.scaleStatefulSet(ctx, env.Namespace, record.Slug, 0)
	}
	if err != nil {
		return store.Database{}, err
	}
	if err := m.db.SetDatabaseStatus(ctx, record.ID, StatusStopped, ""); err != nil {
		return store.Database{}, err
	}
	m.publish(ctx, record.ID)
	m.log.Info("database stopped", "database", record.ID)
	return m.db.GetDatabase(ctx, record.ID)
}

// Start brings a stopped database back on the volume it kept, and waits for
// it to accept connections in the background, with the same check a new one
// is waited for with.
func (m *Manager) Start(ctx context.Context, databaseID string) (store.Database, error) {
	record, env, err := m.locate(ctx, databaseID)
	if err != nil {
		return store.Database{}, err
	}
	if record.Status == "creating" {
		return store.Database{}, errdoc.DatabaseBusy(record.Name)
	}
	if record.Engine == EnginePostgres {
		err = m.patchCluster(ctx, env.Namespace, record.Slug, map[string]any{
			"metadata": map[string]any{"annotations": map[string]any{hibernationAnnotation: "off"}},
		})
	} else {
		// One, whatever the record says: every engine but PostgreSQL runs a
		// single instance (Spec.Validate).
		err = m.scaleStatefulSet(ctx, env.Namespace, record.Slug, 1)
	}
	if err != nil {
		return store.Database{}, err
	}
	if err := m.db.SetDatabaseStatus(ctx, record.ID, "starting", StartingAgain); err != nil {
		return store.Database{}, err
	}
	m.publish(ctx, record.ID)

	background := context.WithoutCancel(ctx)
	spec := Spec{Name: record.Slug, Namespace: env.Namespace, Engine: record.Engine}
	runsafe.Go(m.log, "starting database "+record.ID, func() {
		ctx, cancel := context.WithTimeout(background, 20*time.Minute)
		defer cancel()
		if err := m.waitReady(ctx, record, spec); err != nil {
			problem := errdoc.From(err)
			m.log.Warn("a started database did not become ready", "database", record.ID, "error", err)
			_ = m.db.SetDatabaseStatus(ctx, record.ID, "failed", problem.Error())
		} else {
			_ = m.db.SetDatabaseStatus(ctx, record.ID, "running", "")
			m.log.Info("database started", "database", record.ID)
		}
		m.publish(ctx, record.ID)
	})
	return m.db.GetDatabase(ctx, record.ID)
}

// scaleStatefulSet sets a database's StatefulSet to a number of instances.
// Only the count changes: its volume claims, and so its data, stay.
func (m *Manager) scaleStatefulSet(ctx context.Context, namespace, name string, replicas int32) error {
	sets := m.cluster.Client().Clientset().AppsV1().StatefulSets(namespace)
	set, err := sets.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if kube.IsNotFound(err) {
			return errdoc.DatabaseNotRunning(name, "missing")
		}
		return errdoc.ClusterUnreachable(err)
	}
	if set.Spec.Replicas != nil && *set.Spec.Replicas == replicas {
		return nil
	}
	set.Spec.Replicas = &replicas
	if _, err := sets.Update(ctx, set, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("scale %s to %d: %w", name, replicas, err)
	}
	return nil
}

// patchCluster merges a change into a PostgreSQL database's Cluster.
//
// A merge patch rather than an apply: the panel's field manager owns every
// field it applied when the database was made, and an apply that names only
// the ones changing would hand the rest back — removing them.
func (m *Manager) patchCluster(ctx context.Context, namespace, name string, patch map[string]any) error {
	body, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	if err := m.cluster.Client().Applier().MergePatch(ctx, "postgresql.cnpg.io/v1", "Cluster", namespace, name, body); err != nil {
		if kube.IsNotFound(err) {
			return errdoc.DatabaseNotRunning(name, "missing")
		}
		return err
	}
	return nil
}

// Resources is what a database reserves, may use and has on its disk.
type Resources struct {
	CPURequestM  int
	CPULimitM    int
	MemRequestMB int
	MemLimitMB   int
	StorageGB    int
}

// resourcesOf reads a record's, with the engine's own memory limit for a row
// written before the limit was recorded.
func resourcesOf(record store.Database) Resources {
	out := Resources{
		CPURequestM: record.CPURequestM, CPULimitM: record.CPULimitM,
		MemRequestMB: record.MemRequestMB, MemLimitMB: record.MemLimitMB, StorageGB: record.StorageGB,
	}
	if out.MemLimitMB == 0 {
		if e, ok := engine.Lookup(record.Engine); ok {
			out.MemLimitMB = e.MemLimitMB
		}
	}
	return out
}

// PlanResize applies a request to what a database has now and says what it
// would have, or why that cannot be. It reads nothing but its arguments, so
// every refusal that does not need the cluster is decided, and tested, here.
func PlanResize(record store.Database, req api.ResizeDatabaseRequest) (Resources, error) {
	kind, ok := engine.Lookup(record.Engine)
	if !ok {
		return Resources{}, errdoc.BadRequest(fmt.Sprintf("%q is not an engine Skifity runs.", record.Engine))
	}
	have := resourcesOf(record)
	want := have
	set := func(field **int, into *int) {
		if *field != nil {
			*into = **field
		}
	}
	set(&req.CPURequestM, &want.CPURequestM)
	set(&req.CPULimitM, &want.CPULimitM)
	set(&req.MemRequestMB, &want.MemRequestMB)
	set(&req.MemLimitMB, &want.MemLimitMB)
	set(&req.StorageGB, &want.StorageGB)

	switch {
	case want.CPURequestM < 10 || want.CPURequestM > 64_000:
		return Resources{}, errdoc.BadRequest("Reserve between 10 and 64000 millicores of CPU.")
	case want.CPULimitM < 0 || want.CPULimitM > 64_000:
		return Resources{}, errdoc.BadRequest("A CPU limit is between 0, for none, and 64000 millicores.")
	case want.CPULimitM > 0 && want.CPULimitM < want.CPURequestM:
		// Kubernetes refuses it with words about the pod template nobody can
		// act on, after the database's instance has already been replaced.
		return Resources{}, errdoc.BadRequest("The CPU limit cannot be lower than the CPU reservation.")
	case want.MemRequestMB < 32 || want.MemRequestMB > 262_144:
		return Resources{}, errdoc.BadRequest("Reserve between 32 MB and 256 GB of memory.")
	case want.MemLimitMB > 262_144:
		return Resources{}, errdoc.BadRequest("A memory limit is at most 256 GB.")
	case want.MemLimitMB < want.MemRequestMB:
		return Resources{}, errdoc.BadRequest("The memory limit cannot be lower than the memory reservation.")
	case want.MemLimitMB < kind.MinMemoryMB:
		return Resources{}, errdoc.DatabaseMemoryTooSmall(kind.Title, kind.MinMemoryMB)
	}
	if req.StorageGB != nil {
		switch {
		case !kind.Storage:
			return Resources{}, errdoc.BadRequest(fmt.Sprintf("%s keeps nothing on a disk, so it has none to resize.", kind.Title))
		case want.StorageGB < have.StorageGB:
			return Resources{}, errdoc.DatabaseStorageShrink(record.Name, have.StorageGB, want.StorageGB)
		case want.StorageGB > 16_384:
			return Resources{}, errdoc.BadRequest("A database's disk is at most 16384 GB.")
		}
	}
	return want, nil
}

// Resize changes what a database reserves and may use, and grows its disk.
//
// The order is the one in which a refusal costs nothing: what the request
// asks for, then whether the environment has room for it, then whether the
// disk can grow at all — and only then does anything change. A disk that
// cannot grow refuses the whole request, CPU and memory with it, rather than
// changing half of what was asked and reporting the rest as an error.
func (m *Manager) Resize(ctx context.Context, databaseID string, req api.ResizeDatabaseRequest) (store.Database, error) {
	record, env, err := m.locate(ctx, databaseID)
	if err != nil {
		return store.Database{}, err
	}
	switch record.Status {
	case "creating":
		return store.Database{}, errdoc.DatabaseBusy(record.Name)
	case "missing":
		return store.Database{}, errdoc.DatabaseNotRunning(record.Name, record.Status)
	}
	have := resourcesOf(record)
	want, err := PlanResize(record, req)
	if err != nil {
		return store.Database{}, err
	}
	if want == have {
		return record, nil
	}
	if err := m.checkQuota(ctx, env.Namespace, record, have, want); err != nil {
		return store.Database{}, err
	}
	grow := want.StorageGB > have.StorageGB
	var claims []corev1.PersistentVolumeClaim
	if grow {
		if claims, err = m.databaseClaims(ctx, env.Namespace, record); err != nil {
			return store.Database{}, err
		}
		if err := m.checkExpansion(ctx, record, claims); err != nil {
			return store.Database{}, err
		}
	}

	spec := m.specFor(record, env)
	spec.CPURequestM, spec.CPULimitM = want.CPURequestM, want.CPULimitM
	spec.MemRequestMB, spec.MemLimitMB = want.MemRequestMB, want.MemLimitMB
	spec.StorageGB = want.StorageGB

	if record.Engine == EnginePostgres {
		// One patch, and CloudNativePG does the rest: it replaces the
		// instances one at a time, the primary last, and grows each
		// instance's volume itself, which the storage class has just been
		// checked to allow.
		limits := postgresResources(spec)
		if spec.CPULimitM == 0 {
			// A merge patch deletes only what it names as null.
			limits["limits"].(map[string]any)["cpu"] = nil
		}
		patch := map[string]any{"spec": map[string]any{
			"resources":  limits,
			"postgresql": map[string]any{"parameters": postgresParameters(spec)},
		}}
		if grow {
			patch["spec"].(map[string]any)["storage"] = map[string]any{"size": fmt.Sprintf("%dGi", spec.StorageGB)}
		}
		if err := m.patchCluster(ctx, env.Namespace, record.Slug, patch); err != nil {
			return store.Database{}, err
		}
	} else {
		if err := m.resizeStatefulSet(ctx, spec); err != nil {
			return store.Database{}, err
		}
		if grow {
			// Recorded now, before the disk: the CPU and memory have changed
			// whatever happens to the claims next.
			if err := m.db.SetDatabaseResources(ctx, record.ID, want.CPURequestM, want.CPULimitM,
				want.MemRequestMB, want.MemLimitMB, have.StorageGB); err != nil {
				return store.Database{}, err
			}
			if err := m.growClaims(ctx, claims, want.StorageGB); err != nil {
				return store.Database{}, err
			}
		}
	}
	if err := m.db.SetDatabaseResources(ctx, record.ID, want.CPURequestM, want.CPULimitM,
		want.MemRequestMB, want.MemLimitMB, want.StorageGB); err != nil {
		return store.Database{}, err
	}
	m.publish(ctx, record.ID)
	m.log.Info("database resized", "database", record.ID)
	return m.db.GetDatabase(ctx, record.ID)
}

// resizeStatefulSet puts a database's new resources on its container, and
// the arguments that follow from them: Dragonfly's, MongoDB's and
// Memcached's are sized to the memory limit (see their builders).
//
// The container, and not the whole template or the whole StatefulSet: the
// claim templates cannot change, the replica count is whatever Stop or Start
// left it at, and the rest of the template is what it was when it was made.
func (m *Manager) resizeStatefulSet(ctx context.Context, spec Spec) error {
	built, err := Build(spec)
	if err != nil {
		return err
	}
	rendered, ok := statefulSetIn(built)
	if !ok {
		return fmt.Errorf("%s renders no StatefulSet", spec.Engine)
	}
	fresh := rendered.Spec.Template.Spec.Containers[0]
	sets := m.cluster.Client().Clientset().AppsV1().StatefulSets(spec.Namespace)
	set, err := sets.Get(ctx, spec.Name, metav1.GetOptions{})
	if err != nil {
		if kube.IsNotFound(err) {
			return errdoc.DatabaseNotRunning(spec.Name, "missing")
		}
		return errdoc.ClusterUnreachable(err)
	}
	for i := range set.Spec.Template.Spec.Containers {
		container := &set.Spec.Template.Spec.Containers[i]
		if container.Name != fresh.Name {
			continue
		}
		container.Resources = fresh.Resources
		container.Args = fresh.Args
		container.Command = fresh.Command
	}
	if _, err := sets.Update(ctx, set, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("resize %s: %w", spec.Name, err)
	}
	return nil
}

// databaseClaims are the volumes a database keeps its data on.
func (m *Manager) databaseClaims(ctx context.Context, namespace string, record store.Database) ([]corev1.PersistentVolumeClaim, error) {
	selector := "app.kubernetes.io/name=" + record.Slug
	if record.Engine == EnginePostgres {
		selector = "cnpg.io/cluster=" + record.Slug
	}
	list, err := m.cluster.Client().Clientset().CoreV1().PersistentVolumeClaims(namespace).
		List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, errdoc.ClusterUnreachable(err)
	}
	if len(list.Items) == 0 {
		return nil, errdoc.DatabaseNotRunning(record.Name, "missing")
	}
	return list.Items, nil
}

// checkExpansion refuses a disk that cannot grow, naming the storage class
// that says so. A claim that names no class has the cluster's default.
func (m *Manager) checkExpansion(ctx context.Context, record store.Database, claims []corev1.PersistentVolumeClaim) error {
	classes := m.cluster.Client().Clientset().StorageV1().StorageClasses()
	for _, claim := range claims {
		name := ""
		if claim.Spec.StorageClassName != nil {
			name = *claim.Spec.StorageClassName
		}
		if name == "" {
			all, err := classes.List(ctx, metav1.ListOptions{})
			if err != nil {
				return errdoc.ClusterUnreachable(err)
			}
			for _, class := range all.Items {
				if class.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
					name = class.Name
				}
			}
			if name == "" {
				return errdoc.DatabaseStorageNotExpandable(record.Name, "(none)")
			}
		}
		class, err := classes.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if kube.IsNotFound(err) {
				return errdoc.DatabaseStorageNotExpandable(record.Name, name)
			}
			return errdoc.ClusterUnreachable(err)
		}
		if class.AllowVolumeExpansion == nil || !*class.AllowVolumeExpansion {
			return errdoc.DatabaseStorageNotExpandable(record.Name, name)
		}
	}
	return nil
}

// growClaims asks for more room on each of a database's volumes. The driver
// grows the volume and the filesystem on it, while the database runs where it
// can and at its next start where it cannot.
func (m *Manager) growClaims(ctx context.Context, claims []corev1.PersistentVolumeClaim, sizeGB int) error {
	size := resource.MustParse(strconv.Itoa(sizeGB) + "Gi")
	for _, claim := range claims {
		current := claim.Spec.Resources.Requests[corev1.ResourceStorage]
		if current.Cmp(size) >= 0 {
			continue
		}
		patch := fmt.Sprintf(`{"spec":{"resources":{"requests":{"storage":%q}}}}`, size.String())
		if _, err := m.cluster.Client().Clientset().CoreV1().PersistentVolumeClaims(claim.Namespace).
			Patch(ctx, claim.Name, "application/merge-patch+json", []byte(patch), metav1.PatchOptions{}); err != nil {
			return fmt.Errorf("grow the volume %s: %w", claim.Name, err)
		}
	}
	return nil
}

// checkQuota refuses a resize that would take the environment past its
// ResourceQuota.
//
// Kubernetes would refuse it anyway, and later and worse: the StatefulSet's
// old instance is removed before the new one is created, and the new one is
// then refused, so the database would simply stop. What the database uses now
// is taken off what the environment uses before the new amount is added —
// except while it is stopped, when its instances use nothing.
func (m *Manager) checkQuota(ctx context.Context, namespace string, record store.Database, have, want Resources) error {
	usage, err := m.cluster.Client().QuotaUsage(ctx, namespace)
	if err != nil {
		return errdoc.ClusterUnreachable(err)
	}
	if !usage.Found {
		return nil
	}
	instances := int64(max(record.Instances, 1))
	running := int64(1)
	if record.Status == StatusStopped {
		running = 0
	}
	type change struct {
		resource  string
		was, will int64
		live      int64
		unit      string
	}
	changes := []change{
		{"requests.cpu", int64(have.CPURequestM), int64(want.CPURequestM), running, "m"},
		{"requests.memory", int64(have.MemRequestMB), int64(want.MemRequestMB), running, "Mi"},
		{"limits.memory", int64(have.MemLimitMB), int64(want.MemLimitMB), running, "Mi"},
		// Volumes count whether or not anything is running on them.
		{"requests.storage", int64(have.StorageGB) * 1024, int64(want.StorageGB) * 1024, 1, "Mi"},
	}
	if have.CPULimitM > 0 && want.CPULimitM > 0 {
		changes = append(changes, change{"limits.cpu", int64(have.CPULimitM), int64(want.CPULimitM), running, "m"})
	}
	for _, c := range changes {
		if c.will <= c.was {
			continue
		}
		for _, item := range usage.Items {
			if item.Resource != c.resource || item.HardValue <= 0 {
				continue
			}
			wouldBe := item.UsedValue - c.was*instances*c.live + c.will*instances
			if wouldBe > item.HardValue {
				return errdoc.DatabaseOverQuota(c.resource,
					strconv.FormatInt(wouldBe, 10)+c.unit, strconv.FormatInt(item.HardValue, 10)+c.unit)
			}
		}
	}
	return nil
}

// locate reads a database and the environment it is in, and refuses when
// there is no cluster to change it in.
func (m *Manager) locate(ctx context.Context, databaseID string) (store.Database, store.Environment, error) {
	if m.cluster == nil {
		return store.Database{}, store.Environment{}, errdoc.ClusterUnreachable(nil)
	}
	record, err := m.db.GetDatabase(ctx, databaseID)
	if err != nil {
		return store.Database{}, store.Environment{}, err
	}
	env, err := m.db.GetEnvironment(ctx, record.EnvironmentID)
	if err != nil {
		return store.Database{}, store.Environment{}, err
	}
	return record, env, nil
}

// specFor is the Spec a database was created from, as the record now has it:
// what Create rendered, with its current size. The password is left out;
// password.go adds it where a Secret is rendered.
func (m *Manager) specFor(record store.Database, env store.Environment) Spec {
	r := resourcesOf(record)
	spec := Spec{
		Name: record.Slug, Namespace: env.Namespace, DatabaseID: record.ID,
		Engine: record.Engine, Version: record.EngineVersion, Instances: record.Instances,
		StorageGB: r.StorageGB, CPURequestM: r.CPURequestM, CPULimitM: r.CPULimitM,
		MemRequestMB: r.MemRequestMB, MemLimitMB: r.MemLimitMB,
		Username: "app", DatabaseName: "app",
	}
	spec.Defaults()
	return spec
}

// statefulSetIn finds the StatefulSet among a database's rendered objects.
func statefulSetIn(objects []any) (*appsv1.StatefulSet, bool) {
	for _, object := range objects {
		if set, ok := object.(*appsv1.StatefulSet); ok {
			return set, true
		}
	}
	return nil, false
}
