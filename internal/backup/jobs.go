package backup

import (
	"fmt"
	"strconv"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"skifity/internal/dbsvc"
	"skifity/internal/dbsvc/engine"
	"skifity/internal/kube"
	"skifity/internal/version"
)

// JobSpec describes a backup or restore job.
type JobSpec struct {
	Name      string
	Namespace string
	// Engine decides which dump tool runs.
	Engine string
	// Version is the database's own, which picks the client's image: a
	// dump tool older than its server may not read it, and one newer may
	// write what the server cannot load back.
	Version string
	// CredentialsSecret is the database's own Secret, which the job reads the
	// host, user and password from.
	CredentialsSecret string
	// URL is the presigned upload or download URL. It is passed through a
	// Secret rather than an argument, because a presigned URL carries a
	// signature that grants access to the bucket for its lifetime.
	URLSecret string
	// Restore inverts the direction.
	Restore bool
	// Format, on a restore, is the format of a dump somebody imported (one
	// of engine.Format*), which is loaded with the tool that format needs.
	// Empty is a backup the panel took itself.
	Format string
	// BackupID ties the job back to the panel's record.
	BackupID string
	// Image runs the database's own client tools.
	Image string
	// TransferImage talks to the storage service. It is a second image because
	// no database image ships curl, and installing one inside the job would
	// need root.
	TransferImage string
	// TimeoutSeconds bounds the job.
	TimeoutSeconds int
	// WorkspaceGB is how much room the staged dump may take.
	WorkspaceGB int
	// SealImage, when set, is the panel's own image, which seals the dump
	// before it is uploaded and opens it after it is downloaded. The
	// passphrase is in URLSecret, under "passphrase". See seal.go.
	SealImage string
}

// Defaults fills in the images and the timeout.
func (s *JobSpec) Defaults() {
	if s.Image == "" {
		s.Image = ClientImage(s.Engine, s.Version)
	}
	if s.TransferImage == "" {
		s.TransferImage = DefaultTransferImage
	}
	if s.TimeoutSeconds == 0 {
		s.TimeoutSeconds = 2 * 60 * 60
	}
	if s.WorkspaceGB == 0 {
		s.WorkspaceGB = 20
	}
}

// ClientImage is the image a backup of this engine and version runs its
// client tools from. dbsvc.ClientImage says which and why; the password change
// runs the same one.
func ClientImage(name, version string) string {
	return dbsvc.ClientImage(name, version)
}

// DefaultTransferImage is the image that talks to the storage service.
//
// It is a separate image on purpose: no database image ships curl, and
// installing it inside the job needs root, which the namespace does not allow.
// It holds a presigned URL to the team's bucket while it runs, so it is pinned
// by digest as well as version: whoever could re-push the tag would otherwise
// choose what reads that URL. Read from Docker Hub on 2026-10-02.
const DefaultTransferImage = "curlimages/curl:8.11.1@sha256:c1fe1679c34d9784c1b0d1e5f62ac0a79fca01fb6377cdd33e90473c6f9f9a69"

// runAsUser is the account each image expects to run as.
//
// The namespace enforces the restricted Pod Security profile, which refuses a
// pod that could run as root. These images all default to root and drop
// privileges in their entrypoint, which a backup job never reaches, so the uid
// is named here instead.
func runAsUserFor(image, name string) int64 {
	if strings.HasPrefix(image, "curlimages/curl") {
		return 100 // curl_user
	}
	return dbsvc.ClientUser(name)
}

// Validate reports a job that could not work.
func (s JobSpec) Validate() error {
	if s.Name == "" || s.Namespace == "" {
		return fmt.Errorf("a backup job needs a name and a namespace")
	}
	if s.CredentialsSecret == "" {
		return fmt.Errorf("a backup job needs the database's credentials")
	}
	if s.URLSecret == "" {
		return fmt.Errorf("a backup job needs somewhere to read or write the backup")
	}
	if e, ok := engine.Lookup(s.Engine); !ok || !e.Backups {
		return fmt.Errorf("%q is not an engine Skifity can back up", s.Engine)
	}
	return nil
}

// workspace is where the dump is staged between the two containers.
const workspace = "/work"

// dumpFile is the staged dump, compressed.
const dumpFile = workspace + "/dump.gz"

// BuildJob renders the Kubernetes Job that does the work.
//
// Two containers, not one, and that is the whole shape of this file.
//
// The dump needs the database's own client tools; the transfer needs curl. No
// image has both, and installing curl inside the job needs root — which the
// namespace's restricted Pod Security profile refuses, so the pod was rejected
// before it ran a single line.
//
// The dump is staged on a shared volume rather than streamed into curl,
// because curl reading from a pipe has no length to declare and sends
// Transfer-Encoding: chunked, which S3 answers with 501 on a presigned PUT.
// A file on disk has a size, and the upload is accepted.
func BuildJob(s JobSpec) (*batchv1.Job, error) {
	s.Defaults()
	if err := s.Validate(); err != nil {
		return nil, err
	}

	labels := map[string]string{
		"app.kubernetes.io/name":       s.Name,
		"app.kubernetes.io/managed-by": version.Binary,
		"app.kubernetes.io/component":  "backup",
		version.LabelKey("backup-id"):  s.BackupID,
	}

	// A backup dumps then uploads; a restore downloads then loads. Either way
	// the first step is an init container, so the second never starts on a
	// half-finished file.
	var first []corev1.Container
	var second corev1.Container
	if s.Restore {
		first = []corev1.Container{s.transferContainer("download", downloadScript())}
		if s.SealImage != "" {
			first = append(first, sealContainer("open", s.SealImage, s.URLSecret, "backup-open", dumpFile))
		}
		second = s.databaseContainer("load", restoreScript(s))
		if redisFamily(s.Engine) {
			second = s.replicationSource(second)
		}
	} else {
		first = []corev1.Container{s.databaseContainer("dump", backupScript(s))}
		if s.SealImage != "" {
			first = append(first, sealContainer("seal", s.SealImage, s.URLSecret, "backup-seal", dumpFile))
		}
		second = s.transferContainer("upload", uploadScript())
	}

	backoff := int32(0)
	deadline := int64(s.TimeoutSeconds)
	ttl := int32(3600)
	workspaceSize := resource.MustParse(strconv.Itoa(s.WorkspaceGB) + "Gi")

	return &batchv1.Job{
		TypeMeta: metav1.TypeMeta{APIVersion: "batch/v1", Kind: "Job"},
		ObjectMeta: metav1.ObjectMeta{
			Name: s.Name, Namespace: s.Namespace, Labels: labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			ActiveDeadlineSeconds:   &deadline,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyNever,
					AutomountServiceAccountToken: ptr(false),
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: ptr(true),
						// The two containers run as different accounts and
						// share one volume, so the group is what lets both
						// write to it.
						FSGroup:        ptr(int64(65532)),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					InitContainers: first,
					Containers:     []corev1.Container{second},
					Volumes: []corev1.Volume{{
						Name: "workspace",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: &workspaceSize},
						},
					}},
				},
			},
		},
	}, nil
}

// databaseContainer runs a dump or a load with the database's own client.
func (s JobSpec) databaseContainer(name, script string) corev1.Container {
	container := s.container(name, s.Image, script)
	container.Env = []corev1.EnvVar{
		secretEnv("DB_HOST", s.CredentialsSecret, "host"),
		secretEnv("DB_PORT", s.CredentialsSecret, "port"),
		secretEnv("DB_USER", s.CredentialsSecret, "username"),
		secretEnv("DB_PASSWORD", s.CredentialsSecret, "password"),
		secretEnv("DB_NAME", s.CredentialsSecret, "database"),
	}
	container.SecurityContext.RunAsUser = ptr(runAsUserFor(s.Image, s.Engine))
	return container
}

// replicationSource is what a Redis-family restore needs beyond a load: the
// pod's own address, which the database replicates from, and room for the
// whole dataset in memory, which is where the temporary server holds it.
func (s JobSpec) replicationSource(container corev1.Container) corev1.Container {
	container.Env = append(container.Env, corev1.EnvVar{
		Name: "POD_IP",
		ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{FieldPath: "status.podIP"},
		},
	})
	container.Resources.Limits[corev1.ResourceMemory] = resource.MustParse("2Gi")
	return container
}

// transferContainer moves the staged file to or from the storage service.
func (s JobSpec) transferContainer(name, script string) corev1.Container {
	container := s.container(name, s.TransferImage, script)
	container.Env = []corev1.EnvVar{
		// The presigned URL is a credential in its own right: it grants access
		// to the bucket until it expires, so it is never an argument.
		secretEnv("BACKUP_URL", s.URLSecret, "url"),
	}
	container.SecurityContext.RunAsUser = ptr(runAsUserFor(s.TransferImage, s.Engine))
	// Nothing but the workspace is written, and the storage service's
	// certificates are read from the image.
	container.Resources.Requests[corev1.ResourceCPU] = resource.MustParse("50m")
	container.Resources.Requests[corev1.ResourceMemory] = resource.MustParse("64Mi")
	return container
}

func (s JobSpec) container(name, image, script string) corev1.Container {
	return corev1.Container{
		Name:    name,
		Image:   image,
		Command: []string{"/bin/sh", "-c"},
		Args:    []string{script},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "workspace", MountPath: workspace},
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
			Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("1Gi")},
		},
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr(false),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
	}
}

// redisFamily is Redis and Valkey: one protocol, one snapshot format, and
// one way to put a snapshot back.
func redisFamily(name string) bool {
	return name == dbsvc.EngineRedis || name == dbsvc.EngineValkey
}

// redisTools names a Redis-family engine's server and client, and the field
// of INFO server that carries its version. Valkey reports redis_version as
// 7.2.4 whatever it is, for clients that check; its own is valkey_version.
func redisTools(name string) (server, client, versionField string) {
	if name == dbsvc.EngineValkey {
		return "valkey-server", "valkey-cli", "valkey_version"
	}
	return "redis-server", "redis-cli", "redis_version"
}

// mongoConfig writes the password where mongodump and mongorestore read it
// with --config: a YAML file, since the password option is the one thing the
// tools will not take from the environment, and the command line is where
// anything that lists processes reads it. printf is the shell's own, so the
// password is never an argument either, and the file is readable by nobody
// else and outside the workspace the other containers share.
//
// The umask is the subshell's alone. Set for the whole script it made the
// dump itself unreadable to the containers that seal and upload it, which
// run as other users.
const mongoConfig = `mongo_config="${TMPDIR:-/tmp}/.mongodb.yaml"
(umask 077 && printf 'password: "%s"\n' "$DB_PASSWORD" > "$mongo_config")
`

// mongoFlags are the connection flags both MongoDB tools take. The user is
// the root user, which lives in the admin database.
const mongoFlags = `--host="$DB_HOST" --port="$DB_PORT" --username="$DB_USER" ` +
	`--authenticationDatabase=admin --config="$mongo_config"`

// backupScript dumps a database to the shared workspace.
func backupScript(s JobSpec) string {
	var prepare, dump string
	switch s.Engine {
	case dbsvc.EnginePostgres:
		// --clean --if-exists makes the dump restorable over an existing
		// database, which is what a restore actually does.
		dump = `PGPASSWORD="$DB_PASSWORD" pg_dump --host="$DB_HOST" --port="$DB_PORT" ` +
			`--username="$DB_USER" --dbname="$DB_NAME" --no-owner --no-privileges --clean --if-exists`
	case dbsvc.EngineMySQL:
		// The password goes in the client's own variable, not on its command
		// line, where anything that can list the pod's processes reads it.
		//
		// --no-tablespaces because since 8.0.21 listing them needs the
		// PROCESS privilege, which the app's user does not have and should
		// not; --set-gtid-purged=OFF so the dump does not try to set the
		// server's GTID history on the way back in, which needs more still.
		dump = `MYSQL_PWD="$DB_PASSWORD" mysqldump --host="$DB_HOST" --port="$DB_PORT" --user="$DB_USER" ` +
			`--single-transaction --quick --routines --events --no-tablespaces --set-gtid-purged=OFF "$DB_NAME"`
	case dbsvc.EngineMariaDB:
		dump = `MYSQL_PWD="$DB_PASSWORD" mariadb-dump --host="$DB_HOST" --port="$DB_PORT" --user="$DB_USER" ` +
			`--single-transaction --quick --routines --events "$DB_NAME"`
	case dbsvc.EngineMongoDB:
		// An archive to standard output, uncompressed: the script gzips
		// every dump the same way below, and --gzip here would compress it
		// twice for nothing.
		prepare = mongoConfig
		dump = `mongodump ` + mongoFlags + ` --db="$DB_NAME" --archive`
	case dbsvc.EngineRedis, dbsvc.EngineValkey:
		// --rdb writes a point-in-time snapshot without stopping the server:
		// it asks for one the way a replica does. valkey-cli reads
		// REDISCLI_AUTH when VALKEYCLI_AUTH is not set.
		_, client, _ := redisTools(s.Engine)
		dump = `REDISCLI_AUTH="$DB_PASSWORD" ` + client + ` -h "$DB_HOST" -p "$DB_PORT" --rdb /dev/stdout`
	}

	return fmt.Sprintf(`set -eu

echo "==> Backing up $DB_NAME"
%s
# A pipeline hides a failing dump behind a successful gzip, and a dump that
# failed halfway still compresses cleanly. Without one, the dump's own exit
# status is the command's, and set -e stops here.
%s > %s.raw

# An empty dump is not a small backup, it is no backup.
#
# Every engine here writes a header even for a database with nothing in it, so
# zero bytes means the dump produced nothing while still reporting success.
# Checking after gzip cannot see this: compressing an empty file gives about
# twenty bytes, which every "is the file non-empty" test happily accepts, and
# the result is uploaded, recorded, and found to be worthless on the day
# somebody needs it.
if [ ! -s %s.raw ]; then
  echo "The dump came back empty, so there is nothing to back up. The database may be unreachable or the credentials may be wrong." >&2
  exit 1
fi

gzip -c %s.raw > %s
rm -f %s.raw

echo "==> Dumped $(wc -c < %s) compressed bytes"
`, prepare, dump, dumpFile, dumpFile, dumpFile, dumpFile, dumpFile, dumpFile)
}

// uploadScript sends the staged dump to the storage service.
func uploadScript() string {
	return fmt.Sprintf(`set -eu

if [ ! -s %s ]; then
  echo "The dump is empty, so there is nothing worth uploading." >&2
  exit 1
fi

echo "==> Uploading $(wc -c < %s) bytes"

# --upload-file on a real file sends a Content-Length. Reading from a pipe
# would send Transfer-Encoding: chunked, which S3 refuses on a presigned PUT.
curl --fail --silent --show-error --retry 3 --retry-connrefused \
  --upload-file %s "$BACKUP_URL"

echo "==> Backup uploaded"
`, dumpFile, dumpFile, dumpFile)
}

// downloadScript fetches a backup into the shared workspace.
func downloadScript() string {
	return fmt.Sprintf(`set -eu

echo "==> Downloading the backup"

curl --fail --silent --show-error --location --retry 3 --retry-connrefused \
  --output %s "$BACKUP_URL"

if [ ! -s %s ]; then
  echo "The downloaded backup is empty; it will not be restored." >&2
  exit 1
fi

echo "==> Downloaded $(wc -c < %s) bytes"
`, dumpFile, dumpFile, dumpFile)
}

// restoreScript loads a downloaded backup.
func restoreScript(s JobSpec) string {
	unpacked := dumpFile + ".sql"
	var load string
	switch {
	case s.Format != "":
		load = importLoad(s, unpacked)
	case s.Engine == dbsvc.EnginePostgres:
		load = `PGPASSWORD="$DB_PASSWORD" psql --host="$DB_HOST" --port="$DB_PORT" ` +
			`--username="$DB_USER" --dbname="$DB_NAME" --quiet --set ON_ERROR_STOP=on < ` + unpacked
	case s.Engine == dbsvc.EngineMySQL:
		load = `MYSQL_PWD="$DB_PASSWORD" mysql --host="$DB_HOST" --port="$DB_PORT" --user="$DB_USER" "$DB_NAME" < ` + unpacked
	case s.Engine == dbsvc.EngineMariaDB:
		load = `MYSQL_PWD="$DB_PASSWORD" mariadb --host="$DB_HOST" --port="$DB_PORT" --user="$DB_USER" "$DB_NAME" < ` + unpacked
	case s.Engine == dbsvc.EngineMongoDB:
		// --drop replaces each collection in the backup rather than adding
		// its documents to what is there, and --nsInclude keeps the restore
		// to the app's database, whatever else the archive might hold.
		load = mongoConfig + `mongorestore ` + mongoFlags + ` --nsInclude="$DB_NAME.*" --drop --archive < ` + unpacked
	case redisFamily(s.Engine):
		load = redisRestore(s.Engine, unpacked)
	}

	// No pipeline here, for the reason the backup side spells out and this side
	// did not follow: `set -e` only sees the exit status of the last command in
	// a pipeline. `gzip -dc backup.gz | psql` reported success whenever psql
	// succeeded, however little it was given — so a truncated download or a
	// corrupt archive restored part of a database and said it was finished.
	// With a dump taken `--clean --if-exists`, part of a restore means tables
	// dropped and not put back: data lost, reported as a success, by the one
	// operation somebody runs precisely because they cannot afford to lose any.
	//
	// Decompressed to a file first, exactly as the backup stages its dump, so
	// the archive's own exit status is the one `set -e` reads.
	return fmt.Sprintf(`set -eu

echo "==> Restoring $DB_NAME"

gzip -dc %s > %s

if [ ! -s %s ]; then
  echo "The backup unpacked to nothing, so there is nothing to restore. The archive may be truncated." >&2
  exit 1
fi

%s

rm -f %s

echo "==> Restore finished"
`, dumpFile, unpacked, unpacked, load, unpacked)
}

// redisRestorePort is where the temporary server listens, inside the job's
// own pod.
const redisRestorePort = "6380"

// redisRestore puts an RDB snapshot back into a running Redis or Valkey.
//
// It used to hand the snapshot to redis-cli --pipe, which sends its input to
// the server as commands; a snapshot is not commands, and the server answered
// every line of it with an error. There is no command that loads a snapshot
// into a running server — the server reads one only as it starts, and the
// database's own server cannot be restarted onto a file from here.
//
// A server can, though, be made the replica of another, and a replica's first
// synchronisation replaces everything it holds with the other's dataset. So a
// server of the database's own image starts here, on the snapshot, and the
// database replicates from it until it has all of it; then it is made a
// primary again and carries on with what it was given. Its append-only file
// is rewritten as part of the synchronisation, so the restore survives a
// restart. While it runs the database is a replica, and refuses writes.
//
// The version is checked first. A snapshot is written in the format of the
// server that sends it, and one newer than the database's cannot be read by
// it; that is refused before the database is touched.
//
// Whatever happens after the database becomes a replica, it is made a
// primary again before the script ends — on an error, and on the TERM the job
// is stopped with at its deadline. A job killed outright cannot do that, and a
// restart of the database's pod clears it, since REPLICAOF is not written to
// any configuration.
func redisRestore(name, snapshot string) string {
	server, client, versionField := redisTools(name)
	directory, file := snapshot[:strings.LastIndexByte(snapshot, '/')], snapshot[strings.LastIndexByte(snapshot, '/')+1:]
	return fmt.Sprintf(`# Every client call below reads the password from here, and so does the
# temporary server's own requirepass, below.
export REDISCLI_AUTH="$DB_PASSWORD"
live() { %[2]s -h "$DB_HOST" -p "$DB_PORT" "$@"; }
here() { %[2]s -p %[3]s "$@"; }
keys() { tr -d '\r' | sed -n 's/^db[0-9]*:keys=\([0-9]*\).*/\1/p' | awk '{ n += $1 } END { print n + 0 }'; }
version_of() { tr -d '\r' | sed -n 's/^%[4]s://p' | awk -F. '{ printf "%%d%%03d", $1, $2 }'; }

# A server that has exited and not yet been reaped still answers kill -0.
running() { kill -0 "$source_pid" 2>/dev/null && ! grep -qs '^State:[[:space:]]*Z' "/proc/$source_pid/status"; }

config="${TMPDIR:-/tmp}/restore.conf"
(umask 077 && printf 'port %[3]s\ndir %[5]s\ndbfilename %[6]s\nappendonly no\nsave ""\nrequirepass "%%s"\n' "$DB_PASSWORD" > "$config")
%[1]s "$config" &
source_pid=$!
# The server goes when the script does, however it ends; the job's deadline
# ends it with a TERM, which the shell turns into an ordinary exit.
stop() { kill "$source_pid" 2>/dev/null || true; }
trap stop EXIT
trap 'exit 1' TERM INT

tries=0
until [ "$(here ping 2>/dev/null)" = PONG ]; do
  if ! running; then
    echo "The backup could not be loaded: the snapshot is damaged, or newer than this version of %[7]s reads." >&2
    exit 1
  fi
  tries=$((tries + 1))
  if [ "$tries" -gt 900 ]; then
    echo "The backup did not finish loading within fifteen minutes." >&2
    exit 1
  fi
  sleep 1
done

have="$(live info server | version_of)"
want="$(here info server | version_of)"
if [ -z "$have" ]; then
  echo "The database did not answer, so nothing was changed." >&2
  exit 1
fi
if [ -z "$want" ] || [ "$want" -gt "$have" ]; then
  echo "The database runs an older %[7]s than the one this restore loads the backup with, and could not read the snapshot it would be sent. Nothing was changed." >&2
  exit 1
fi

# From here the database may be a replica, and a replica refuses writes. It
# is made a primary again before the script ends, whichever way it ends.
release() {
  echo 'REPLICAOF NO ONE' | live > /dev/null 2>&1 || true
  echo 'CONFIG SET masterauth ""' | live > /dev/null 2>&1 || true
}
trap 'release; stop' EXIT

if [ "$(printf 'CONFIG SET masterauth "%%s"\n' "$DB_PASSWORD" | live)" != OK ]; then
  echo "The database refused to take the restore's password for replication." >&2
  exit 1
fi
if [ "$(echo "REPLICAOF $POD_IP %[3]s" | live)" != OK ]; then
  echo "The database refused to replicate from the restore." >&2
  exit 1
fi

echo "==> Copying the backup into the database"
while :; do
  state="$(live info replication | tr -d '\r')"
  case "$state" in
    *master_link_status:up*)
      case "$state" in *master_sync_in_progress:0*) break ;; esac ;;
  esac
  if ! running; then
    echo "The restore's own server stopped before the database had copied the backup." >&2
    exit 1
  fi
  sleep 2
done

expected="$(here info keyspace | keys)"
tries=0
until [ "$(live info keyspace | keys)" = "$expected" ]; do
  tries=$((tries + 1))
  if [ "$tries" -gt 30 ]; then
    echo "The database holds $(live info keyspace | keys) keys after the restore, and the backup has $expected." >&2
    exit 1
  fi
  sleep 1
done

trap stop EXIT
release
case "$(live info replication | tr -d '\r')" in
  *role:master*) ;;
  *)
    echo "The database is still replicating from the restore. Restart it to make it a primary again." >&2
    exit 1
    ;;
esac
echo "==> Restored $expected keys"`, server, client, redisRestorePort, versionField, directory, file, engineTitle(name))
}

// engineTitle is an engine's own name, for a message.
func engineTitle(name string) string {
	if e, ok := engine.Lookup(name); ok {
		return e.Title
	}
	return name
}

// URLSecret renders the Secret carrying a presigned URL.
func URLSecret(name, namespace, presigned string) *corev1.Secret {
	return JobSecret(name, namespace, presigned, "")
}

// JobSecret is URLSecret with the backup passphrase beside the URL, for a job
// that seals or opens what it moves. It lives exactly as long as the job.
func JobSecret(name, namespace, presigned, passphrase string) *corev1.Secret {
	data := map[string]string{"url": presigned}
	if passphrase != "" {
		data["passphrase"] = passphrase
	}
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: namespace,
			Labels: map[string]string{"app.kubernetes.io/managed-by": version.Binary},
		},
		Type:       corev1.SecretTypeOpaque,
		StringData: data,
	}
}

// sealContainer seals a staged file before it is uploaded, or opens one after
// it is downloaded, with the panel's own binary: no database or tar image has
// anything that does this, and the panel's image already has it.
func sealContainer(name, image, secret, command, file string) corev1.Container {
	return corev1.Container{
		Name:  name,
		Image: image,
		// The image's entrypoint is the binary; distroless has no shell.
		Args:         []string{command, file},
		Env:          []corev1.EnvVar{secretEnv("SKIFITY_BACKUP_PASSPHRASE", secret, "passphrase")},
		VolumeMounts: []corev1.VolumeMount{{Name: "workspace", MountPath: workspace}},
		Resources: corev1.ResourceRequirements{
			// Argon2id takes 64 MiB, once, to derive the key.
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
			Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("512Mi")},
		},
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr(false),
			RunAsNonRoot:             ptr(true),
			RunAsUser:                ptr(int64(65532)),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			ReadOnlyRootFilesystem:   ptr(true),
		},
	}
}

// JobName builds a unique, valid name for a backup job.
func JobName(prefix, backupID string) string {
	id := backupID
	if idx := strings.IndexByte(id, '_'); idx >= 0 {
		id = id[idx+1:]
	}
	if len(id) > 10 {
		id = id[:10]
	}
	return kube.ResourceName(prefix, id)
}

func secretEnv(name, secret, key string) corev1.EnvVar {
	return corev1.EnvVar{
		Name: name,
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: secret},
				Key:                  key,
			},
		},
	}
}

func ptr[T any](v T) *T { return &v }
