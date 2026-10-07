package dbsvc

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"skifity/internal/api"
	"skifity/internal/dbsvc/engine"
	"skifity/internal/kube"
	"skifity/internal/store"
)

// plantedPassword is looked for everywhere a password must not be.
const plantedPassword = "planted-password-4b1d"

// statefulEngines is every engine that runs as a StatefulSet, which is every
// one but PostgreSQL.
func statefulEngines() []string {
	var out []string
	for _, name := range engine.Names() {
		if name != EnginePostgres {
			out = append(out, name)
		}
	}
	return out
}

func specFor(name string) Spec {
	s := Spec{
		Name: "main", Namespace: "acme-shop-production", DatabaseID: "db_1", TeamID: "team_1",
		Engine: name, Password: plantedPassword,
	}
	s.Defaults()
	return s
}

func statefulSetOf(t *testing.T, objects []any) *appsv1.StatefulSet {
	t.Helper()
	for _, object := range objects {
		if set, ok := object.(*appsv1.StatefulSet); ok {
			return set
		}
	}
	t.Fatalf("no StatefulSet among %d objects", len(objects))
	return nil
}

func serviceOf(t *testing.T, objects []any) *corev1.Service {
	t.Helper()
	for _, object := range objects {
		if service, ok := object.(*corev1.Service); ok {
			return service
		}
	}
	t.Fatalf("no Service among %d objects", len(objects))
	return nil
}

// Every engine renders, every one validates at its default, and every one
// runs the image its version is pinned to.
func TestEveryEngineRendersItsPinnedImage(t *testing.T) {
	for _, e := range engine.All() {
		for _, version := range e.Versions {
			s := specFor(e.Name)
			s.Version = version
			if err := s.Validate(); err != nil {
				t.Errorf("%s %s does not validate: %v", e.Name, version, err)
				continue
			}
			objects, err := Build(s)
			if err != nil {
				t.Fatalf("%s: %v", e.Name, err)
			}
			want, _ := e.Image(version)
			var got string
			if e.Name == EnginePostgres {
				cluster := objects[0].(*unstructured.Unstructured)
				got, _, _ = unstructured.NestedString(cluster.Object, "spec", "imageName")
			} else {
				got = statefulSetOf(t, objects).Spec.Template.Spec.Containers[0].Image
			}
			if got != want || got == "" {
				t.Errorf("%s %s runs %q, want %q", e.Name, version, got, want)
			}
		}
	}
}

// No password in any container's command, arguments or probes, in anything
// the panel renders for any engine: /proc/<pid>/cmdline is readable by
// everything that can list the pod's processes, and a pod's spec by anybody
// who can read the namespace. It reaches every container from the Secret.
func TestNoEngineRendersAPasswordIntoAContainer(t *testing.T) {
	for _, name := range engine.Names() {
		objects, err := Build(specFor(name))
		if err != nil {
			t.Fatal(err)
		}
		for _, object := range objects {
			raw, err := json.Marshal(object)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), plantedPassword) {
				t.Errorf("%s: the password is written into a %T", name, object)
			}
		}
		if name == EnginePostgres {
			continue // CloudNativePG's Cluster names the Secret and nothing else
		}
		pod := statefulSetOf(t, objects).Spec.Template.Spec
		for _, container := range append(append([]corev1.Container{}, pod.InitContainers...), pod.Containers...) {
			for _, env := range container.Env {
				if strings.Contains(strings.ToLower(env.Name), "password") || env.Name == "DFLY_PASSWORD" {
					if env.ValueFrom == nil || env.ValueFrom.SecretKeyRef == nil || env.ValueFrom.SecretKeyRef.Key != "password" {
						t.Errorf("%s: %s does not come from the Secret's password", name, env.Name)
					}
				}
			}
		}
	}
	// The Secret is where it is, and the only place.
	for _, name := range engine.Names() {
		s := specFor(name)
		secret := BuildSecret(s)
		e, _ := engine.Lookup(name)
		if e.Password && secret.StringData["password"] != plantedPassword {
			t.Errorf("%s: the Secret does not carry the password", name)
		}
	}
}

// The Redis-family servers are started by a shell, and the shell expands
// whatever it is given before the server starts. So the command is run here,
// with the server replaced by a stub that writes down its arguments: the
// password must reach the server in a file, not among them.
func TestRedisFamilyServersAreNotGivenThePasswordAsAnArgument(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no POSIX shell here")
	}
	for _, tc := range []struct{ engine, server, variable string }{
		{EngineRedis, "redis-server", "REDIS_PASSWORD"},
		{EngineValkey, "valkey-server", "VALKEY_PASSWORD"},
	} {
		container := statefulSetOf(t, mustBuild(t, specFor(tc.engine))).Spec.Template.Spec.Containers[0]
		if len(container.Command) != 3 || container.Command[0] != "sh" {
			t.Fatalf("%s starts with %q", tc.engine, container.Command)
		}
		dir := t.TempDir()
		seen := filepath.Join(dir, "seen")
		stub := "#!/bin/sh\nprintf 'ARGS %s\\n' \"$*\" > " + seen + "\ncat \"$1\" >> " + seen + "\n"
		if err := os.WriteFile(filepath.Join(dir, tc.server), []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
		// The container writes the file to /tmp; the test, to its own.
		script := strings.ReplaceAll(container.Command[2], "/tmp/", dir+"/")
		cmd := exec.Command("sh", "-c", script)
		cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), tc.variable+"="+plantedPassword)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: the command failed: %v\n%s", tc.engine, err, output)
		}
		recorded, err := os.ReadFile(seen)
		if err != nil {
			t.Fatalf("%s: %s never started", tc.engine, tc.server)
		}
		args, config, _ := strings.Cut(string(recorded), "\n")
		if strings.Contains(args, plantedPassword) {
			t.Errorf("%s: the server was given the password as an argument: %s", tc.engine, args)
		}
		if !strings.Contains(config, `requirepass "`+plantedPassword+`"`) {
			t.Errorf("%s: the server's configuration does not set the password:\n%s", tc.engine, config)
		}
		for _, want := range []string{"--appendonly yes", "--dir /data"} {
			if !strings.Contains(args, want) {
				t.Errorf("%s: the server was not started with %s: %s", tc.engine, want, args)
			}
		}
	}
}

func mustBuild(t *testing.T, s Spec) []any {
	t.Helper()
	objects, err := Build(s)
	if err != nil {
		t.Fatal(err)
	}
	return objects
}

func envNames(container corev1.Container) map[string]corev1.EnvVar {
	out := map[string]corev1.EnvVar{}
	for _, env := range container.Env {
		out[env.Name] = env
	}
	return out
}

func TestMongoDBManifest(t *testing.T) {
	objects := mustBuild(t, specFor(EngineMongoDB))
	set := statefulSetOf(t, objects)
	pod := set.Spec.Template.Spec
	container := pod.Containers[0]
	env := envNames(container)
	for variable, key := range map[string]string{
		"MONGO_INITDB_ROOT_USERNAME": "username", "MONGO_INITDB_ROOT_PASSWORD": "password", "MONGO_INITDB_DATABASE": "database",
	} {
		if env[variable].ValueFrom == nil || env[variable].ValueFrom.SecretKeyRef.Key != key {
			t.Errorf("%s does not come from the Secret's %s", variable, key)
		}
	}
	// The image's entrypoint turns on --auth when it creates the root user;
	// the cache is sized to the container, not to the node.
	if strings.Join(container.Args, " ") != "mongod --wiredTigerCacheSizeGB 0.25" {
		t.Errorf("mongod is started with %q", container.Args)
	}
	if container.VolumeMounts[0].MountPath != "/data/db" || *pod.SecurityContext.RunAsUser != 999 {
		t.Errorf("MongoDB keeps its data at %s as %d", container.VolumeMounts[0].MountPath, *pod.SecurityContext.RunAsUser)
	}
	if container.ReadinessProbe == nil || container.ReadinessProbe.TCPSocket == nil {
		t.Error("MongoDB is ready before it listens beyond loopback")
	}
	if port := serviceOf(t, objects).Spec.Ports[0]; port.Port != 27017 {
		t.Errorf("MongoDB's Service answers on %d", port.Port)
	}
}

func TestClickHouseManifest(t *testing.T) {
	objects := mustBuild(t, specFor(EngineClickHouse))
	pod := statefulSetOf(t, objects).Spec.Template.Spec
	container := pod.Containers[0]
	env := envNames(container)
	// With CLICKHOUSE_DB set, the image's entrypoint runs clickhouse-client
	// with --password "$CLICKHOUSE_PASSWORD" to create it.
	if _, set := env["CLICKHOUSE_DB"]; set {
		t.Error("CLICKHOUSE_DB is set, so the image's entrypoint puts the password on a command line")
	}
	if env["CLICKHOUSE_PASSWORD"].ValueFrom == nil || env["CLICKHOUSE_USER"].ValueFrom == nil {
		t.Error("ClickHouse's user and password do not come from the Secret")
	}
	if env["CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT"].Value != "1" {
		t.Error("the user cannot manage access")
	}
	// The database is made by the startup probe, with clickhouse-client
	// reading the password from the environment.
	probe := container.StartupProbe
	if probe == nil || probe.Exec == nil || !strings.Contains(strings.Join(probe.Exec.Command, " "), "CREATE DATABASE IF NOT EXISTS `app`") {
		t.Fatalf("nothing creates the app's database: %+v", probe)
	}
	if container.ReadinessProbe == nil || container.ReadinessProbe.HTTPGet == nil || container.ReadinessProbe.HTTPGet.Path != "/ping" {
		t.Error("ClickHouse is not asked /ping")
	}
	if *pod.SecurityContext.RunAsUser != 101 {
		t.Errorf("ClickHouse runs as %d, not its image's clickhouse user", *pod.SecurityContext.RunAsUser)
	}
	ports := map[string]int32{}
	for _, port := range serviceOf(t, objects).Spec.Ports {
		ports[port.Name] = port.Port
	}
	if ports["http"] != 8123 || ports["native"] != 9000 {
		t.Errorf("ClickHouse's Service answers on %v", ports)
	}
	s := specFor(EngineClickHouse)
	if got := s.NativeURL(); got != "clickhouse://app:"+plantedPassword+"@main.acme-shop-production.svc.cluster.local:9000/app" {
		t.Errorf("the native address is %q", got)
	}
	if BuildSecret(s).StringData["native_url"] != s.NativeURL() {
		t.Error("the Secret does not carry the native address")
	}
	if specFor(EnginePostgres).NativeURL() != "" {
		t.Error("an engine with one protocol has a second address")
	}
}

func TestDragonflyManifest(t *testing.T) {
	objects := mustBuild(t, specFor(EngineDragonfly))
	pod := statefulSetOf(t, objects).Spec.Template.Spec
	container := pod.Containers[0]
	if env := envNames(container)["DFLY_PASSWORD"]; env.ValueFrom == nil || env.ValueFrom.SecretKeyRef.Key != "password" {
		t.Error("Dragonfly's password does not come from the Secret, through its own variable")
	}
	args := strings.Join(container.Args, " ")
	for _, want := range []string{"--dir=/data", "--dbfilename=dump", "--snapshot_cron=*/5 * * * *"} {
		if !strings.Contains(args, want) {
			t.Errorf("Dragonfly is started without %s: %s", want, args)
		}
	}
	// Dragonfly refuses to start with less than 256 MB for each thread.
	var maxMemory, threads int
	for _, arg := range container.Args {
		if value, ok := strings.CutPrefix(arg, "--maxmemory="); ok {
			maxMemory, _ = strconv.Atoi(value)
		}
		if value, ok := strings.CutPrefix(arg, "--proactor_threads="); ok {
			threads, _ = strconv.Atoi(value)
		}
	}
	if threads < 1 || maxMemory < threads*256<<20 {
		t.Errorf("%d bytes for %d threads, and Dragonfly wants 256 MB for each", maxMemory, threads)
	}
	limit := container.Resources.Limits[corev1.ResourceMemory]
	if int64(maxMemory) >= limit.Value() {
		t.Errorf("Dragonfly may use %d bytes in a container of %s", maxMemory, limit.String())
	}
	if pod.TerminationGracePeriodSeconds == nil || *pod.TerminationGracePeriodSeconds < 30 {
		t.Error("Dragonfly is not given time to write its snapshot as it stops")
	}
}

// Memcached has no disk and no password, so what keeps it private is the
// environment's network policy, and a Service nothing outside can reach.
func TestMemcachedIsACacheOnlyItsEnvironmentReaches(t *testing.T) {
	s := specFor(EngineMemcached)
	if s.StorageGB != 0 || s.Password != "" || s.Username != "" {
		t.Errorf("memcached was given %d GB, a user %q and a password", s.StorageGB, s.Username)
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("memcached without a password was refused: %v", err)
	}
	objects := mustBuild(t, s)
	set := statefulSetOf(t, objects)
	if len(set.Spec.VolumeClaimTemplates) != 0 || len(set.Spec.Template.Spec.Containers[0].VolumeMounts) != 0 {
		t.Error("memcached asks for a disk")
	}
	container := set.Spec.Template.Spec.Containers[0]
	if len(container.Env) != 0 {
		t.Errorf("memcached is given %v", container.Env)
	}
	if !strings.Contains(strings.Join(container.Args, " "), "-U 0") {
		t.Error("memcached listens on UDP")
	}
	service := serviceOf(t, objects)
	if service.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Errorf("memcached's Service is a %s", service.Spec.Type)
	}

	// The environment's own policy: every peer that may come in names a
	// namespace, or is the environment's own pods. A peer with an empty
	// namespace selector would be every environment on the cluster.
	for _, policy := range kube.BuildNetworkPolicies("acme-shop-production", "skifity-system", nil) {
		for _, rule := range policy.Spec.Ingress {
			for _, peer := range rule.From {
				checkPeerIsNamed(t, peer)
			}
		}
	}
}

func checkPeerIsNamed(t *testing.T, peer networkingv1.NetworkPolicyPeer) {
	t.Helper()
	switch {
	case peer.IPBlock != nil:
		t.Errorf("the environment lets in addresses %s", peer.IPBlock.CIDR)
	case peer.NamespaceSelector != nil:
		if peer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] == "" {
			t.Errorf("the environment lets in namespaces matching %+v, not one named", peer.NamespaceSelector)
		}
	case peer.PodSelector == nil:
		t.Errorf("the environment lets in %+v", peer)
	}
}

// Each engine is sized for what it is: a cache small, an analytical engine
// with room for a query.
func TestEachEngineIsSizedForWhatItIs(t *testing.T) {
	for _, name := range engine.Names() {
		s := specFor(name)
		if s.MemLimitMB <= s.MemRequestMB {
			t.Errorf("%s: the memory limit (%d) is not above the reservation (%d)", name, s.MemLimitMB, s.MemRequestMB)
		}
	}
	if clickhouse, postgres := specFor(EngineClickHouse), specFor(EnginePostgres); clickhouse.MemLimitMB <= postgres.MemLimitMB {
		t.Errorf("ClickHouse gets %d MB, no more than PostgreSQL", clickhouse.MemLimitMB)
	}
}

// Linking hands an app the URL the database was made with, for every engine.
func TestLinkingDeliversEachEnginesURL(t *testing.T) {
	m, db, app, _, _ := linkHarness(t)
	ctx := t.Context()
	record, err := db.GetApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range engine.Names() {
		s := specFor(name)
		database := store.Database{EnvironmentID: record.EnvironmentID, Name: name, Slug: name + "-db", Engine: name, Status: "running"}
		if err := db.CreateDatabase(ctx, &database); err != nil {
			t.Fatalf("%s: the panel's own database refuses it: %v", name, err)
		}
		raw, _ := json.Marshal(api.DatabaseCredentials{Engine: name, URL: s.ConnectionURL(), NativeURL: s.NativeURL()})
		database.CredentialsEnc, err = m.keyring.Seal(raw, credentialsContext(database.ID))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.UpdateDatabase(ctx, &database); err != nil {
			t.Fatal(err)
		}
		variable := engine.DefaultVariable(name) + "_" + strings.ToUpper(name)
		if err := m.Link(ctx, database.ID, app.ID, variable); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := linkedValue(t, m, db, app.ID, variable); got != s.ConnectionURL() || got == "" {
			t.Errorf("%s: the app was given %q, want %q", name, got, s.ConnectionURL())
		}
	}
}

func linkedValue(t *testing.T, m *Manager, db *store.DB, appID, key string) string {
	t.Helper()
	rows, err := db.ListVariables(context.Background(), appID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Key != key {
			continue
		}
		plain, err := m.keyring.Open(row.Sealed, "variable:"+appID+":"+key)
		if err != nil {
			t.Fatal(err)
		}
		return string(plain)
	}
	return ""
}

// A MariaDB made when the panel called it mysql was sealed saying mysql, and
// migration 0047 renamed its record. The record is what is reported.
func TestCredentialsNameTheRecordsEngine(t *testing.T) {
	m, db, _, orders, _ := linkHarness(t)
	ctx := t.Context()
	raw, _ := json.Marshal(api.DatabaseCredentials{Engine: "mysql", URL: "mysql://app:pw@orders:3306/app"})
	sealed, err := m.keyring.Seal(raw, credentialsContext(orders.ID))
	if err != nil {
		t.Fatal(err)
	}
	orders.Engine, orders.CredentialsEnc = EngineMariaDB, sealed
	if err := db.UpdateDatabase(ctx, &orders); err != nil {
		t.Fatal(err)
	}
	// UpdateDatabase does not change an engine, so the row is set directly,
	// as the migration did.
	if _, err := db.Exec(ctx, `UPDATE databases SET engine = 'mariadb' WHERE id = ?`, orders.ID); err != nil {
		t.Fatal(err)
	}
	credentials, err := m.Credentials(ctx, orders.ID)
	if err != nil {
		t.Fatal(err)
	}
	if credentials.Engine != EngineMariaDB {
		t.Errorf("the credentials say %q", credentials.Engine)
	}
}
