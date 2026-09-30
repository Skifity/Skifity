package dbsvc

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func postgresSpec() Spec {
	s := Spec{
		Name: "main", Namespace: "acme-shop-production", DatabaseID: "db_1", TeamID: "team_1",
		Engine: EnginePostgres, Instances: 1, StorageGB: 10,
		Username: "app", Password: "s3cret", DatabaseName: "app",
	}
	s.Defaults()
	return s
}

func TestConnectionURLs(t *testing.T) {
	cases := []struct {
		engine string
		prefix string
		host   string
	}{
		// PostgreSQL must point at the primary: writing to a replica fails, and
		// CloudNativePG exposes the primary as <cluster>-rw.
		{EnginePostgres, "postgresql://app:s3cret@main-rw.acme-shop-production.svc.cluster.local:5432/app", "main-rw"},
		{EngineRedis, "redis://default:s3cret@main.acme-shop-production.svc.cluster.local:6379", "main"},
		{EngineMySQL, "mysql://app:s3cret@main.acme-shop-production.svc.cluster.local:3306/app", "main"},
	}
	for _, tc := range cases {
		s := postgresSpec()
		s.Engine = tc.engine
		s.Defaults()
		url := s.ConnectionURL()
		if !strings.HasPrefix(url, tc.prefix) {
			t.Errorf("%s URL is %q, want it to start with %q", tc.engine, url, tc.prefix)
		}
		if !strings.Contains(s.ServiceHost(), tc.host+".") {
			t.Errorf("%s host is %q, want %q", tc.engine, s.ServiceHost(), tc.host)
		}
	}
}

func TestValidate(t *testing.T) {
	if err := postgresSpec().Validate(); err != nil {
		t.Fatalf("a valid spec was rejected: %v", err)
	}
	cases := map[string]func(*Spec){
		"bad name":       func(s *Spec) { s.Name = "Not Valid" },
		"unknown engine": func(s *Spec) { s.Engine = "mongo" },
		"no password":    func(s *Spec) { s.Password = "" },
		"even instances": func(s *Spec) { s.Instances = 2 },
	}
	for name, mutate := range cases {
		s := postgresSpec()
		mutate(&s)
		if err := s.Validate(); err == nil {
			t.Errorf("%s: an invalid spec was accepted", name)
		}
	}
	// Three is fine: a failover keeps a majority.
	s := postgresSpec()
	s.Instances = 3
	if err := s.Validate(); err != nil {
		t.Fatalf("three instances were rejected: %v", err)
	}
}

func TestPostgresManifest(t *testing.T) {
	object := BuildPostgres(postgresSpec())
	if object.GetKind() != "Cluster" || object.GetAPIVersion() != "postgresql.cnpg.io/v1" {
		t.Fatalf("rendered %s/%s", object.GetAPIVersion(), object.GetKind())
	}

	instances, found, _ := unstructured.NestedInt64(object.Object, "spec", "instances")
	if !found || instances != 1 {
		t.Fatalf("instances is %d", instances)
	}
	size, _, _ := unstructured.NestedString(object.Object, "spec", "storage", "size")
	if size != "10Gi" {
		t.Fatalf("storage is %q, want 10Gi", size)
	}
	// Bootstrapping from the panel's own Secret is what ties the stored
	// credentials to the ones the database actually uses.
	secret, _, _ := unstructured.NestedString(object.Object, "spec", "bootstrap", "initdb", "secret", "name")
	if secret != "main-credentials" {
		t.Fatalf("bootstrap secret is %q", secret)
	}
	// The superuser is disabled: an app has no reason to be able to drop the
	// cluster.
	superuser, found, _ := unstructured.NestedBool(object.Object, "spec", "enableSuperuserAccess")
	if !found || superuser {
		t.Fatal("superuser access is enabled")
	}
	// PostgreSQL's defaults assume a dedicated machine; in a container they
	// have to be sized to the memory limit.
	buffers, _, _ := unstructured.NestedString(object.Object, "spec", "postgresql", "parameters", "shared_buffers")
	if buffers == "" {
		t.Fatal("shared_buffers was not sized for the container")
	}
}

func TestRedisManifest(t *testing.T) {
	s := postgresSpec()
	s.Engine = EngineRedis
	s.Defaults()

	objects := BuildRedis(s)
	if len(objects) != 2 {
		t.Fatalf("got %d objects, want a StatefulSet and a Service", len(objects))
	}
	statefulSet, ok := objects[0].(*appsv1.StatefulSet)
	if !ok {
		t.Fatalf("the first object is %T", objects[0])
	}

	container := statefulSet.Spec.Template.Spec.Containers[0]
	// The password must reach Redis through the environment, not the command
	// line, where it would be visible to anything that can read /proc.
	command := strings.Join(container.Command, " ")
	if strings.Contains(command, "s3cret") {
		t.Fatalf("the password is on the command line: %s", command)
	}
	if !strings.Contains(command, "$REDIS_PASSWORD") {
		t.Fatalf("the password is not read from the environment: %s", command)
	}
	if len(container.Env) == 0 || container.Env[0].ValueFrom == nil {
		t.Fatal("the password does not come from a Secret")
	}
	// Persistence: without appendonly, a restart loses everything.
	if !strings.Contains(command, "--appendonly yes") {
		t.Fatal("Redis is not persisting to disk, so a restart would lose the data")
	}
	if len(statefulSet.Spec.VolumeClaimTemplates) != 1 {
		t.Fatal("no volume was requested")
	}
	if container.SecurityContext == nil || *container.SecurityContext.AllowPrivilegeEscalation {
		t.Fatal("the container can gain privileges")
	}
}

func TestMySQLManifest(t *testing.T) {
	s := postgresSpec()
	s.Engine = EngineMySQL
	s.Defaults()

	objects := BuildMySQL(s)
	statefulSet := objects[0].(*appsv1.StatefulSet)
	container := statefulSet.Spec.Template.Spec.Containers[0]

	// Every credential must come from the Secret.
	for _, env := range container.Env {
		if env.Value != "" {
			t.Errorf("%s is set to a literal value in the manifest", env.Name)
		}
		if env.ValueFrom == nil || env.ValueFrom.SecretKeyRef == nil {
			t.Errorf("%s does not come from a Secret", env.Name)
		}
	}
	if container.ReadinessProbe == nil {
		t.Fatal("there is no readiness probe, so an app would connect before the database is up")
	}
}

func TestSecretCarriesEverythingAnAppNeeds(t *testing.T) {
	secret := BuildSecret(postgresSpec())
	if secret.Name != "main-credentials" {
		t.Fatalf("secret name is %q", secret.Name)
	}
	for _, key := range []string{"username", "password", "database", "host", "port", "url"} {
		if secret.StringData[key] == "" {
			t.Errorf("the secret has no %s", key)
		}
	}
	if secret.Type != corev1.SecretTypeOpaque {
		t.Fatalf("secret type is %q", secret.Type)
	}
}

func TestSelectorIsStable(t *testing.T) {
	// A StatefulSet's selector is immutable. Anything in it that could change
	// would make the next update fail with an error nobody can act on.
	labels := postgresSpec().Labels()
	selected := selector(labels)
	for key := range selected {
		switch key {
		case "app.kubernetes.io/name", "app.kubernetes.io/component":
		default:
			t.Errorf("the selector contains %q, which may change over a database's life", key)
		}
	}
}

func TestDefaultsAreSensible(t *testing.T) {
	s := Spec{Name: "main", Engine: EnginePostgres, Password: "x"}
	s.Defaults()
	if s.Version == "" {
		t.Fatal("no default version")
	}
	if s.StorageGB < 1 || s.Instances < 1 {
		t.Fatalf("defaults left %d GB and %d instances", s.StorageGB, s.Instances)
	}
	if s.MemLimitMB <= s.MemRequestMB {
		t.Fatalf("the memory limit (%d) is not above the reservation (%d)", s.MemLimitMB, s.MemRequestMB)
	}
}

func TestInterpretCNPGStatus(t *testing.T) {
	cases := []struct {
		phase string
		ready int64
		want  string
	}{
		{"Cluster in healthy state", 1, "running"},
		{"Setting up primary", 0, "starting"},
		{"Failed to create cluster", 0, "failed"},
		{"", 0, "starting"},
	}
	for _, tc := range cases {
		object := &unstructured.Unstructured{Object: map[string]any{
			"status": map[string]any{"phase": tc.phase, "readyInstances": tc.ready},
		}}
		if got, _ := interpretCNPGStatus(object); got != tc.want {
			t.Errorf("phase %q with %d ready = %q, want %q", tc.phase, tc.ready, got, tc.want)
		}
	}
}

// TestDatabasePodsSatisfyRestrictedPodSecurity: every environment namespace
// enforces the restricted profile. A database that does not satisfy it is not
// scheduled at all, and the failure looks like a cluster problem rather than a
// manifest that was never going to work.
func TestDatabasePodsSatisfyRestrictedPodSecurity(t *testing.T) {
	for _, engine := range []string{EngineRedis, EngineMySQL} {
		spec := Spec{
			Name: "main", Namespace: "acme-shop-production", Engine: engine,
			DatabaseID: "db_1", TeamID: "team_1", Password: "not-a-real-password",
		}
		spec.Defaults()

		var objects []any
		if engine == EngineRedis {
			objects = BuildRedis(spec)
		} else {
			objects = BuildMySQL(spec)
		}

		set, ok := objects[0].(*appsv1.StatefulSet)
		if !ok {
			t.Fatalf("%s: the first object is %T, want a StatefulSet", engine, objects[0])
		}
		pod := set.Spec.Template.Spec

		if pod.SecurityContext == nil || pod.SecurityContext.SeccompProfile == nil ||
			pod.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
			t.Errorf("%s: the pod does not set the default seccomp profile, so it is refused", engine)
		}
		if pod.SecurityContext == nil || pod.SecurityContext.RunAsNonRoot == nil ||
			!*pod.SecurityContext.RunAsNonRoot {
			t.Errorf("%s: the pod does not declare runAsNonRoot", engine)
		}
		if pod.SecurityContext.RunAsUser == nil || *pod.SecurityContext.RunAsUser == 0 {
			t.Errorf("%s: the pod does not name a non-root user", engine)
		}
		for _, container := range pod.Containers {
			sc := container.SecurityContext
			if sc == nil || sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
				t.Errorf("%s: %s may escalate privileges", engine, container.Name)
			}
			if sc == nil || sc.Capabilities == nil || len(sc.Capabilities.Drop) == 0 ||
				sc.Capabilities.Drop[0] != "ALL" {
				t.Errorf("%s: %s does not drop every capability", engine, container.Name)
			}
		}
	}
}

// Asking for three Redis instances must be refused, not quietly turned into
// one. BuildRedis and BuildMySQL render a single replica on purpose — three
// StatefulSet replicas are three separate disks behind one Service, which is a
// split brain and not a replica set. The danger is the silence: the panel's own
// record would say three while one ran.
func TestOnlyPostgresIsReplicated(t *testing.T) {
	for _, engine := range []string{EngineRedis, EngineMySQL} {
		t.Run(engine, func(t *testing.T) {
			spec := Spec{
				Name: "cache", Namespace: "acme-shop-production", Engine: engine,
				Password: "not-a-real-password", Instances: 3,
			}
			spec.Defaults()
			if err := spec.Validate(); err == nil {
				t.Fatal("three instances were accepted for an engine that runs one")
			}

			spec.Instances = 1
			if err := spec.Validate(); err != nil {
				t.Fatalf("one instance was refused: %v", err)
			}
		})
	}

	// PostgreSQL is replicated, by CloudNativePG, so an odd count above one is
	// the shape that works and an even one is the waste it refuses.
	postgres := Spec{
		Name: "db", Namespace: "acme-shop-production", Engine: EnginePostgres,
		Password: "not-a-real-password", Instances: 3,
	}
	postgres.Defaults()
	if err := postgres.Validate(); err != nil {
		t.Fatalf("three PostgreSQL instances were refused: %v", err)
	}
	postgres.Instances = 2
	if err := postgres.Validate(); err == nil {
		t.Fatal("an even number of PostgreSQL instances was accepted")
	}
}

// A three-instance PostgreSQL cluster with one instance left is up, and is one
// failure from an outage. Reporting that as "running" reports what was asked
// for rather than what is there.
func TestADatabaseMissingItsReplicasIsNotSimplyRunning(t *testing.T) {
	cluster := func(wanted, ready int64) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"spec":   map[string]any{"instances": wanted},
			"status": map[string]any{"phase": "Cluster in healthy state", "readyInstances": ready},
		}}
	}

	if status, detail := interpretCNPGStatus(cluster(3, 1)); status != "degraded" {
		t.Errorf("one of three ready reported %q (%q), want degraded", status, detail)
	} else if detail == "" {
		t.Error("a degraded database says nothing about what is missing")
	}
	if status, _ := interpretCNPGStatus(cluster(3, 3)); status != "running" {
		t.Errorf("a whole cluster reported %q, want running", status)
	}
	if status, _ := interpretCNPGStatus(cluster(1, 1)); status != "running" {
		t.Errorf("a single healthy instance reported %q, want running", status)
	}
	if status, _ := interpretCNPGStatus(cluster(3, 0)); status == "running" {
		t.Error("a cluster with nothing ready reported running")
	}
}
