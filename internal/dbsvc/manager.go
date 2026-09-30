package dbsvc

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"skifity/internal/api"
	"skifity/internal/cluster"
	"skifity/internal/crypto"
	"skifity/internal/errdoc"
	"skifity/internal/events"
	"skifity/internal/kube"
	"skifity/internal/runsafe"
	"skifity/internal/store"
)

// Manager implements api.DatabaseManager.
type Manager struct {
	db      *store.DB
	keyring *crypto.Keyring
	hub     *events.Hub
	cluster *cluster.Cluster
	log     *slog.Logger
	// deployer re-applies an app after a database is linked, so the new
	// variable actually reaches the running instances.
	deployer interface {
		Sync(ctx context.Context, appID string) error
	}
}

// New builds a Manager.
func New(db *store.DB, keyring *crypto.Keyring, hub *events.Hub, c *cluster.Cluster,
	deployer interface {
		Sync(ctx context.Context, appID string) error
	}, log *slog.Logger) *Manager {
	return &Manager{db: db, keyring: keyring, hub: hub, cluster: c, deployer: deployer, log: log}
}

// Create provisions a database, installing its operator on first use.
func (m *Manager) Create(ctx context.Context, env store.Environment, req api.CreateDatabaseRequest) (store.Database, error) {
	if m.cluster == nil {
		return store.Database{}, errdoc.ClusterUnreachable(nil)
	}

	password, err := crypto.RandomToken(24)
	if err != nil {
		return store.Database{}, err
	}
	// The generated password ends up in connection strings and shell commands,
	// so restrict it to characters that never need escaping.
	password = strings.NewReplacer("-", "x", "_", "y").Replace(password)

	// The same reason as on the app side: an app and a database in one
	// environment share a namespace and both render a Service under their slug,
	// so a second one under the same name takes the first one's address over.
	slug := kube.Slugify(req.Name)
	if owner, err := m.db.SlugOwnerInEnvironment(ctx, env.ID, slug); err != nil {
		return store.Database{}, err
	} else if owner != "" {
		return store.Database{}, errdoc.NameTaken(owner, req.Name)
	}

	record := store.Database{
		EnvironmentID: env.ID,
		Name:          req.Name,
		Slug:          slug,
		Engine:        req.Engine,
		EngineVersion: req.Version,
		Status:        "creating",
		Instances:     max(req.Instances, 1),
		StorageGB:     max(req.StorageGB, 5),
		CPURequestM:   100,
		MemRequestMB:  256,
	}
	if record.EngineVersion == "" {
		record.EngineVersion = DefaultVersions[req.Engine]
	}

	spec := Spec{
		Name:         record.Slug,
		Namespace:    env.Namespace,
		Engine:       record.Engine,
		Version:      record.EngineVersion,
		Instances:    record.Instances,
		StorageGB:    record.StorageGB,
		Username:     "app",
		Password:     password,
		DatabaseName: "app",
		CPURequestM:  record.CPURequestM,
		MemRequestMB: record.MemRequestMB,
		MemLimitMB:   1024,
	}
	spec.Defaults()
	if err := spec.Validate(); err != nil {
		return store.Database{}, errdoc.BadRequest(err.Error())
	}

	// Store the credentials encrypted before anything is created, so a failure
	// halfway through still leaves a record that can be cleaned up.
	credentials := api.DatabaseCredentials{
		Engine: spec.Engine, Host: spec.ServiceHost(), Port: spec.Port(),
		Database: spec.DatabaseName, Username: spec.Username, Password: spec.Password,
		URL: spec.ConnectionURL(),
	}
	encoded, err := json.Marshal(credentials)
	if err != nil {
		return store.Database{}, err
	}
	if err := m.db.CreateDatabase(ctx, &record); err != nil {
		return store.Database{}, err
	}
	sealed, err := m.keyring.Seal(encoded, credentialsContext(record.ID))
	if err != nil {
		return store.Database{}, err
	}
	record.CredentialsEnc = sealed
	spec.DatabaseID = record.ID
	if err := m.db.UpdateDatabase(ctx, &record); err != nil {
		return store.Database{}, err
	}

	// Provisioning takes minutes, so it runs in the background like a deploy.
	go m.provision(context.WithoutCancel(ctx), record, spec)
	return record, nil
}

func (m *Manager) provision(ctx context.Context, record store.Database, spec Spec) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()

	fail := func(err error) {
		problem := errdoc.From(err)
		m.log.Error("could not create the database",
			"database", record.ID, "engine", record.Engine, "error", err)
		_ = m.db.SetDatabaseStatus(ctx, record.ID, "failed", problem.Error())
		m.publish(ctx, record.ID)
	}
	// Twenty minutes of operator installs and cluster polling, in a goroutine
	// nobody is waiting on. A panic here used to be the end of the panel.
	defer runsafe.Recover(m.log, "database "+record.ID, fail)

	if record.Engine == EnginePostgres {
		// The operator is installed on first use, which is what keeps a fresh
		// install small enough for a 2 GB server.
		if err := m.cluster.EnsureComponent(ctx, "cloudnative-pg"); err != nil {
			fail(err)
			return
		}
	}

	objects := []any{BuildSecret(spec)}
	switch record.Engine {
	case EnginePostgres:
		objects = append(objects, BuildPostgres(spec))
	case EngineRedis:
		objects = append(objects, BuildRedis(spec)...)
	case EngineMySQL:
		objects = append(objects, BuildMySQL(spec)...)
	}

	if err := m.cluster.Client().Applier().ApplyAll(ctx, objects...); err != nil {
		fail(err)
		return
	}

	_ = m.db.SetDatabaseStatus(ctx, record.ID, "starting", "")
	m.publish(ctx, record.ID)

	if err := m.waitReady(ctx, record, spec); err != nil {
		fail(err)
		return
	}
	_ = m.db.SetDatabaseStatus(ctx, record.ID, "running", "")
	m.publish(ctx, record.ID)
	m.log.Info("database ready", "database", record.ID, "engine", record.Engine)
}

// waitReady blocks until a database is accepting connections.
func (m *Manager) waitReady(ctx context.Context, record store.Database, spec Spec) error {
	deadline := time.Now().Add(15 * time.Minute)
	for {
		status, detail, err := m.readStatus(ctx, record, spec)
		if err == nil && status == "running" {
			return nil
		}
		if err == nil && status == "failed" {
			return errdoc.New("database.failed", "The database could not start").
				WithCause("%s", detail).
				WithImpact("Nothing is using it yet, so nothing else is affected.").
				WithFix("Check that the cluster has enough free memory and disk for it, then delete it and try again.").
				Retry()
		}
		if time.Now().After(deadline) {
			return errdoc.New("database.timeout", "The database did not start in time").
				WithCause("%s did not become ready within fifteen minutes.", record.Name).
				WithImpact("The database exists but is not usable.").
				WithFix("Open the instance list to see why. The usual causes are no free disk for the volume, and not enough memory on any server.").
				Retry()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// Status refreshes a database's state from the cluster.
func (m *Manager) Status(ctx context.Context, databaseID string) (string, string, error) {
	record, err := m.db.GetDatabase(ctx, databaseID)
	if err != nil {
		return "", "", err
	}
	env, err := m.db.GetEnvironment(ctx, record.EnvironmentID)
	if err != nil {
		return "", "", err
	}
	return m.readStatus(ctx, record, Spec{
		Name: record.Slug, Namespace: env.Namespace, Engine: record.Engine,
	})
}

func (m *Manager) readStatus(ctx context.Context, record store.Database, spec Spec) (string, string, error) {
	if m.cluster == nil {
		return "unknown", "The panel is not connected to a cluster.", nil
	}
	if spec.Namespace == "" {
		env, err := m.db.GetEnvironment(ctx, record.EnvironmentID)
		if err != nil {
			return "", "", err
		}
		spec.Namespace = env.Namespace
		spec.Name = record.Slug
	}

	switch record.Engine {
	case EnginePostgres:
		return m.postgresStatus(ctx, spec)
	default:
		return m.statefulSetStatus(ctx, spec)
	}
}

func (m *Manager) postgresStatus(ctx context.Context, spec Spec) (string, string, error) {
	object, err := m.cluster.Client().Applier().Get(ctx,
		"postgresql.cnpg.io/v1", "Cluster", spec.Namespace, spec.Name)
	if err != nil {
		if kube.IsNotFound(err) {
			return "missing", "The database's Kubernetes object is gone.", nil
		}
		return "unknown", err.Error(), nil
	}
	status, detail := interpretCNPGStatus(object)
	return status, detail, nil
}

// interpretCNPGStatus turns a CloudNativePG Cluster's status into ours.
//
// The reason for a "degraded" at all: a three-instance cluster that has lost
// two of them still answers every query, because the primary is one of the
// three. Reporting that as "running" is reporting the state the operator asked
// for rather than the one they have — and the next failure is the outage they
// paid two extra instances to avoid. It has to be visibly different from
// healthy, and it is not "failed", because the database is up.
func interpretCNPGStatus(object *unstructured.Unstructured) (string, string) {
	phase, _, _ := unstructured.NestedString(object.Object, "status", "phase")
	ready, _, _ := unstructured.NestedInt64(object.Object, "status", "readyInstances")
	wanted, found, _ := unstructured.NestedInt64(object.Object, "spec", "instances")
	if !found || wanted < 1 {
		wanted = 1
	}

	switch {
	case ready > 0 && ready < wanted:
		return "degraded", fmt.Sprintf(
			"%d of %d instances are ready. The database is answering, but a failure of the one that is left is an outage.",
			ready, wanted)
	case ready > 0:
		return "running", ""
	case strings.Contains(strings.ToLower(phase), "fail"):
		return "failed", phase
	default:
		return "starting", ""
	}
}

func (m *Manager) statefulSetStatus(ctx context.Context, spec Spec) (string, string, error) {
	statefulSet, err := m.cluster.Client().Clientset().AppsV1().
		StatefulSets(spec.Namespace).Get(ctx, spec.Name, metav1.GetOptions{})
	if err != nil {
		if kube.IsNotFound(err) {
			return "missing", "The database's Kubernetes object is gone.", nil
		}
		return "unknown", err.Error(), nil
	}
	wanted := int32(1)
	if statefulSet.Spec.Replicas != nil && *statefulSet.Spec.Replicas > 0 {
		wanted = *statefulSet.Spec.Replicas
	}
	switch {
	case statefulSet.Status.ReadyReplicas >= wanted:
		return "running", "", nil
	case statefulSet.Status.ReadyReplicas > 0:
		return "degraded", fmt.Sprintf("%d of %d instances are ready.",
			statefulSet.Status.ReadyReplicas, wanted), nil
	default:
		return "starting", "Waiting for the database to accept connections.", nil
	}
}

// Credentials returns the connection details, decrypted.
func (m *Manager) Credentials(ctx context.Context, databaseID string) (api.DatabaseCredentials, error) {
	record, err := m.db.GetDatabase(ctx, databaseID)
	if err != nil {
		return api.DatabaseCredentials{}, err
	}
	if record.CredentialsEnc == "" {
		return api.DatabaseCredentials{}, errdoc.New("database.no_credentials", "This database has no stored credentials").
			WithCause("Its credentials are missing from the panel's database.").
			WithImpact("The connection details cannot be shown.").
			WithFix("Read them from the Kubernetes Secret %s, or recreate the database.", kube.ResourceName(record.Slug, "credentials"))
	}
	plaintext, err := m.keyring.Open(record.CredentialsEnc, credentialsContext(record.ID))
	if err != nil {
		return api.DatabaseCredentials{}, fmt.Errorf("read the database credentials: %w", err)
	}
	var credentials api.DatabaseCredentials
	if err := json.Unmarshal(plaintext, &credentials); err != nil {
		return api.DatabaseCredentials{}, fmt.Errorf("read the database credentials: %w", err)
	}
	return credentials, nil
}

// Link injects a database's connection string into an app as a variable.
func (m *Manager) Link(ctx context.Context, databaseID, appID, varName string) error {
	credentials, err := m.Credentials(ctx, databaseID)
	if err != nil {
		return err
	}
	sealed, err := m.keyring.Seal([]byte(credentials.URL), "variable:"+appID+":"+varName)
	if err != nil {
		return err
	}
	// The connection string is a secret: it carries the password.
	variable := store.Variable{AppID: appID, Key: varName, IsSecret: true}
	if err := m.db.SetVariable(ctx, &variable, sealed); err != nil {
		return err
	}
	if err := m.db.LinkDatabase(ctx, databaseID, appID, varName); err != nil {
		return err
	}
	// Roll the app so the new variable actually reaches its instances.
	if m.deployer != nil {
		if err := m.deployer.Sync(ctx, appID); err != nil {
			m.log.Warn("could not apply the database link", "app", appID, "error", err)
		}
	}
	return nil
}

// Unlink removes the injected variable.
func (m *Manager) Unlink(ctx context.Context, databaseID, appID string) error {
	links, err := m.db.ListLinksForApp(ctx, appID)
	if err != nil {
		return err
	}
	for _, link := range links {
		if link.DatabaseID != databaseID {
			continue
		}
		if err := m.db.DeleteVariable(ctx, appID, link.VarName); err != nil && !strings.Contains(err.Error(), "not found") {
			return err
		}
	}
	if err := m.db.UnlinkDatabase(ctx, databaseID, appID); err != nil {
		return err
	}
	if m.deployer != nil {
		if err := m.deployer.Sync(ctx, appID); err != nil {
			m.log.Warn("could not apply the database unlink", "app", appID, "error", err)
		}
	}
	return nil
}

// Delete removes a database and its storage.
func (m *Manager) Delete(ctx context.Context, databaseID string) error {
	record, err := m.db.GetDatabase(ctx, databaseID)
	if err != nil {
		return err
	}
	env, err := m.db.GetEnvironment(ctx, record.EnvironmentID)
	if err != nil {
		return err
	}

	if m.cluster != nil {
		applier := m.cluster.Client().Applier()
		switch record.Engine {
		case EnginePostgres:
			if err := applier.Delete(ctx, "postgresql.cnpg.io/v1", "Cluster", env.Namespace, record.Slug); err != nil {
				return err
			}
		default:
			if err := applier.Delete(ctx, "apps/v1", "StatefulSet", env.Namespace, record.Slug); err != nil {
				return err
			}
			if err := applier.Delete(ctx, "v1", "Service", env.Namespace, record.Slug); err != nil {
				return err
			}
		}
		if err := applier.Delete(ctx, "v1", "Secret", env.Namespace,
			kube.ResourceName(record.Slug, "credentials")); err != nil {
			m.log.Warn("could not delete the database's secret", "database", databaseID, "error", err)
		}
		// A StatefulSet's claims outlive it by design, so they are removed
		// explicitly. Deleting the database is an explicit act; leaving the
		// disks behind would be a surprise on the next bill.
		if err := m.deleteClaims(ctx, env.Namespace, record.Slug); err != nil {
			m.log.Warn("could not delete the database's volumes", "database", databaseID, "error", err)
		}
	}

	// The apps that used it lose their variable, so they stop referring to
	// something that no longer exists.
	//
	// A failure here is not fatal — the database still goes — but it leaves
	// apps holding a connection string to nothing, so it is said out loud
	// rather than swallowed.
	links, err := m.db.ListLinksForDatabase(ctx, databaseID)
	if err != nil {
		m.log.Warn("could not find the apps linked to this database; they keep a variable pointing at it",
			"database", databaseID, "error", err)
	}
	for _, link := range links {
		if err := m.db.DeleteVariable(ctx, link.AppID, link.VarName); err != nil {
			m.log.Warn("could not remove a linked app's database variable",
				"database", databaseID, "app", link.AppID, "error", err)
		}
		if m.deployer != nil {
			if err := m.deployer.Sync(ctx, link.AppID); err != nil {
				m.log.Warn("could not roll out the removal of a database variable",
					"database", databaseID, "app", link.AppID, "error", err)
			}
		}
	}
	return m.db.DeleteDatabase(ctx, databaseID)
}

func (m *Manager) deleteClaims(ctx context.Context, namespace, name string) error {
	claims, err := m.cluster.Client().Clientset().CoreV1().
		PersistentVolumeClaims(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=" + name,
	})
	if err != nil {
		return err
	}
	for _, claim := range claims.Items {
		if err := m.cluster.Client().Clientset().CoreV1().
			PersistentVolumeClaims(namespace).Delete(ctx, claim.Name, metav1.DeleteOptions{}); err != nil &&
			!kube.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (m *Manager) publish(ctx context.Context, databaseID string) {
	record, err := m.db.GetDatabase(ctx, databaseID)
	if err != nil {
		return
	}
	if teamID, err := m.db.TeamIDForDatabase(ctx, databaseID); err == nil {
		m.hub.Publish(events.TeamTopic(teamID), "database", record)
	}
}

func credentialsContext(databaseID string) string { return "database_credentials:" + databaseID }
