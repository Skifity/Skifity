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
	"skifity/internal/kube"
	"skifity/internal/version"
)

// JobSpec describes a backup or restore job.
type JobSpec struct {
	Name      string
	Namespace string
	// Engine decides which dump tool runs.
	Engine string
	// CredentialsSecret is the database's own Secret, which the job reads the
	// host, user and password from.
	CredentialsSecret string
	// URL is the presigned upload or download URL. It is passed through a
	// Secret rather than an argument, because a presigned URL carries a
	// signature that grants access to the bucket for its lifetime.
	URLSecret string
	// Restore inverts the direction.
	Restore bool
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
}

// Defaults fills in the images and the timeout.
func (s *JobSpec) Defaults() {
	if s.Image == "" {
		switch s.Engine {
		case dbsvc.EnginePostgres:
			// The client version must be at least the server's, so a recent
			// image is used rather than one pinned to the server version.
			s.Image = "postgres:17-alpine"
		case dbsvc.EngineMySQL:
			s.Image = "mariadb:11.4"
		case dbsvc.EngineRedis:
			s.Image = "redis:7-alpine"
		default:
			s.Image = "alpine:3"
		}
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

// DefaultTransferImage is the image that talks to the storage service.
//
// It is a separate image on purpose: no database image ships curl, and
// installing it inside the job needs root, which the namespace does not allow.
const DefaultTransferImage = "curlimages/curl:8.11.1"

// runAsUser is the account each image expects to run as.
//
// The namespace enforces the restricted Pod Security profile, which refuses a
// pod that could run as root. These images all default to root and drop
// privileges in their entrypoint, which a backup job never reaches, so the uid
// is named here instead.
func runAsUserFor(image, engine string) int64 {
	switch {
	case strings.HasPrefix(image, "curlimages/curl"):
		return 100 // curl_user
	case engine == dbsvc.EnginePostgres:
		return 70 // postgres, in the Alpine image
	case engine == dbsvc.EngineMySQL:
		return 999 // mysql
	case engine == dbsvc.EngineRedis:
		return 999 // redis
	default:
		return 65532
	}
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
	switch s.Engine {
	case dbsvc.EnginePostgres, dbsvc.EngineMySQL, dbsvc.EngineRedis:
		return nil
	default:
		return fmt.Errorf("%q is not an engine Skifity can back up", s.Engine)
	}
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
	var first, second corev1.Container
	if s.Restore {
		first = s.transferContainer("download", downloadScript())
		second = s.databaseContainer("load", restoreScript(s))
	} else {
		first = s.databaseContainer("dump", backupScript(s))
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
					InitContainers: []corev1.Container{first},
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

// backupScript dumps a database to the shared workspace.
func backupScript(s JobSpec) string {
	var dump string
	switch s.Engine {
	case dbsvc.EnginePostgres:
		// --clean --if-exists makes the dump restorable over an existing
		// database, which is what a restore actually does.
		dump = `PGPASSWORD="$DB_PASSWORD" pg_dump --host="$DB_HOST" --port="$DB_PORT" ` +
			`--username="$DB_USER" --dbname="$DB_NAME" --no-owner --no-privileges --clean --if-exists`
	case dbsvc.EngineMySQL:
		dump = `mariadb-dump --host="$DB_HOST" --port="$DB_PORT" --user="$DB_USER" ` +
			`--password="$DB_PASSWORD" --single-transaction --quick --routines --events "$DB_NAME"`
	case dbsvc.EngineRedis:
		// --rdb writes a point-in-time snapshot without stopping the server.
		dump = `redis-cli -h "$DB_HOST" -p "$DB_PORT" -a "$DB_PASSWORD" --no-auth-warning --rdb /dev/stdout`
	}

	return fmt.Sprintf(`set -eu

echo "==> Backing up $DB_NAME"

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
`, dump, dumpFile, dumpFile, dumpFile, dumpFile, dumpFile, dumpFile)
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
	var load string
	switch s.Engine {
	case dbsvc.EnginePostgres:
		load = `PGPASSWORD="$DB_PASSWORD" psql --host="$DB_HOST" --port="$DB_PORT" ` +
			`--username="$DB_USER" --dbname="$DB_NAME" --quiet --set ON_ERROR_STOP=on`
	case dbsvc.EngineMySQL:
		load = `mariadb --host="$DB_HOST" --port="$DB_PORT" --user="$DB_USER" ` +
			`--password="$DB_PASSWORD" "$DB_NAME"`
	case dbsvc.EngineRedis:
		// Redis cannot load an RDB over a running server, so the restore
		// replays the keys instead. This is slower but does not need the pod
		// to be stopped and the volume swapped.
		load = `redis-cli -h "$DB_HOST" -p "$DB_PORT" -a "$DB_PASSWORD" --no-auth-warning --pipe`
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

gzip -dc %s > %s.sql

if [ ! -s %s.sql ]; then
  echo "The backup unpacked to nothing, so there is nothing to restore. The archive may be truncated." >&2
  exit 1
fi

%s < %s.sql

rm -f %s.sql

echo "==> Restore finished"
`, dumpFile, dumpFile, dumpFile, load, dumpFile, dumpFile)
}

// URLSecret renders the Secret carrying a presigned URL.
func URLSecret(name, namespace, presigned string) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: namespace,
			Labels: map[string]string{"app.kubernetes.io/managed-by": version.Binary},
		},
		Type:       corev1.SecretTypeOpaque,
		StringData: map[string]string{"url": presigned},
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
