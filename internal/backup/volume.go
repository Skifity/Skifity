package backup

import (
	"fmt"
	"strconv"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"skifity/internal/version"
)

// Backing up a volume.
//
// The panel refused rather than pretending, which was honest and still a gap:
// an app's uploads had nowhere to go, and "back up your database" is only half
// an answer to somebody who has just lost a disk.
//
// The same two-container shape as a database backup, for the same reasons: no
// image has both tar and curl, installing one inside the job needs root, and
// curl reading from a pipe has no length to declare so S3 refuses the presigned
// PUT. A tar on a shared volume has a size, and the upload is accepted.
//
// The one thing a volume backup has that a database backup does not is the
// volume itself. It is ReadWriteOnce, so this pod and the app's pod must be on
// the same node, and the scheduler only knows that if it is told: with the
// storage class k3s ships the PersistentVolume carries its own node affinity
// and this is free, but on a networked volume already attached elsewhere the
// pod would sit in a Multi-Attach error instead. CoLocateWith is set when the
// app is running so the two land together.

// VolumeJobSpec describes a volume backup or restore.
type VolumeJobSpec struct {
	Name      string
	Namespace string
	// ClaimName is the PersistentVolumeClaim holding the app's data.
	ClaimName string
	// URLSecret holds the presigned upload or download URL. A presigned URL is
	// a credential in its own right, so it is never an argument.
	URLSecret string
	// Restore inverts the direction.
	Restore bool
	// BackupID ties the job back to the panel's record.
	BackupID string
	// CoLocateWith are the labels of the app's own pods. Set when the app is
	// running, so this pod is scheduled onto the node already holding the
	// volume. Empty when nothing is running, and then the scheduler is free.
	CoLocateWith map[string]string
	// Image has tar and gzip. Alpine, which is four megabytes.
	Image string
	// TransferImage talks to the storage service.
	TransferImage string
	// TimeoutSeconds bounds the job.
	TimeoutSeconds int
	// WorkspaceGB is how much room the staged archive may take.
	WorkspaceGB int
}

// Defaults fills in the images and the bounds.
func (s *VolumeJobSpec) Defaults() {
	if s.Image == "" {
		s.Image = "alpine:3"
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

// Validate reports a job that could not work.
func (s VolumeJobSpec) Validate() error {
	if s.Name == "" || s.Namespace == "" {
		return fmt.Errorf("a volume backup needs a name and a namespace")
	}
	if s.ClaimName == "" {
		return fmt.Errorf("a volume backup needs the volume to read")
	}
	if s.URLSecret == "" {
		return fmt.Errorf("a volume backup needs somewhere to read or write the archive")
	}
	return nil
}

// dataMount is where the app's volume is mounted inside the job.
const dataMount = "/data"

// archiveFile is the staged tar, compressed.
const archiveFile = workspace + "/volume.tar.gz"

// BuildVolumeJob renders the Job that copies a volume to or from storage.
func BuildVolumeJob(s VolumeJobSpec) (*batchv1.Job, error) {
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

	// A backup archives then uploads; a restore downloads then unpacks. The
	// first step is an init container either way, so the second never starts on
	// a half-finished file.
	var first, second corev1.Container
	if s.Restore {
		first = s.transfer("download", downloadVolumeScript())
		second = s.files("unpack", unpackScript())
	} else {
		first = s.files("archive", archiveScript())
		second = s.transfer("upload", uploadVolumeScript())
	}

	backoff := int32(0)
	deadline := int64(s.TimeoutSeconds)
	ttl := int32(3600)
	workspaceSize := resource.MustParse(strconv.Itoa(s.WorkspaceGB) + "Gi")

	spec := corev1.PodSpec{
		RestartPolicy:                corev1.RestartPolicyNever,
		AutomountServiceAccountToken: ptr(false),
		SecurityContext: &corev1.PodSecurityContext{
			RunAsNonRoot: ptr(true),
			// The archive runs as the same user the app does, because that is
			// who owns the files on the volume. A different account would read
			// nothing and write a valid, empty archive.
			RunAsUser: ptr(int64(1000)),
			// The two containers run as different accounts and share the
			// workspace, so the group is what lets both write to it.
			FSGroup:        ptr(int64(65532)),
			SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
		InitContainers: []corev1.Container{first},
		Containers:     []corev1.Container{second},
		Volumes: []corev1.Volume{
			{
				Name: "workspace",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: &workspaceSize},
				},
			},
			{
				Name: "data",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: s.ClaimName,
						// Read-only for a backup. A backup that can write to
						// what it is backing up is one bug away from being the
						// thing that destroyed it.
						ReadOnly: !s.Restore,
					},
				},
			},
		},
	}

	if len(s.CoLocateWith) > 0 {
		spec.Affinity = &corev1.Affinity{
			PodAffinity: &corev1.PodAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
					TopologyKey:   "kubernetes.io/hostname",
					LabelSelector: &metav1.LabelSelector{MatchLabels: s.CoLocateWith},
				}},
			},
		}
	}

	return &batchv1.Job{
		TypeMeta:   metav1.TypeMeta{APIVersion: "batch/v1", Kind: "Job"},
		ObjectMeta: metav1.ObjectMeta{Name: s.Name, Namespace: s.Namespace, Labels: labels},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			ActiveDeadlineSeconds:   &deadline,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       spec,
			},
		},
	}, nil
}

// files runs tar against the volume.
func (s VolumeJobSpec) files(name, script string) corev1.Container {
	container := s.container(name, s.Image, script)
	container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
		Name: "data", MountPath: dataMount, ReadOnly: !s.Restore,
	})
	return container
}

// transfer moves the staged archive to or from the storage service.
func (s VolumeJobSpec) transfer(name, script string) corev1.Container {
	container := s.container(name, s.TransferImage, script)
	container.Env = []corev1.EnvVar{secretEnv("BACKUP_URL", s.URLSecret, "url")}
	container.SecurityContext.RunAsUser = ptr(int64(100)) // curl_user
	return container
}

func (s VolumeJobSpec) container(name, image, script string) corev1.Container {
	return corev1.Container{
		Name:         name,
		Image:        image,
		Command:      []string{"/bin/sh", "-c"},
		Args:         []string{script},
		VolumeMounts: []corev1.VolumeMount{{Name: "workspace", MountPath: workspace}},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
			Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("1Gi")},
		},
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr(false),
			RunAsNonRoot:             ptr(true),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			// Nothing but the two mounted volumes is written.
			ReadOnlyRootFilesystem: ptr(true),
		},
	}
}

func archiveScript() string {
	return fmt.Sprintf(`set -eu

echo "==> Archiving %s"

# An empty volume is a valid backup, and restoring one is how somebody empties
# a volume on purpose. A tar of nothing is a few bytes, not zero, so the upload
# step's emptiness check still catches a genuine failure.
tar czf %s -C %s .

echo "==> Archived $(wc -c < %s) compressed bytes"
`, dataMount, archiveFile, dataMount, archiveFile)
}

func uploadVolumeScript() string {
	return fmt.Sprintf(`set -eu

if [ ! -s %s ]; then
  echo "The archive is empty, so there is nothing worth uploading." >&2
  exit 1
fi

echo "==> Uploading $(wc -c < %s) bytes"

# --upload-file on a real file sends a Content-Length. Reading from a pipe
# would send Transfer-Encoding: chunked, which S3 refuses on a presigned PUT.
curl --fail --silent --show-error --retry 3 --retry-connrefused \
  --upload-file %s "$BACKUP_URL"

echo "==> Backup uploaded"
`, archiveFile, archiveFile, archiveFile)
}

func downloadVolumeScript() string {
	return fmt.Sprintf(`set -eu

echo "==> Downloading the archive"

curl --fail --silent --show-error --location --retry 3 --retry-connrefused \
  --output %s "$BACKUP_URL"

if [ ! -s %s ]; then
  echo "The downloaded archive is empty; it will not be restored." >&2
  exit 1
fi

echo "==> Downloaded $(wc -c < %s) bytes"
`, archiveFile, archiveFile, archiveFile)
}

func unpackScript() string {
	return fmt.Sprintf(`set -eu

echo "==> Checking the archive before touching anything"

# Read it through once first. Unpacking a truncated archive over live data
# would leave half the old files and half the new, which is worse than either.
gzip -t %s
tar tzf %s > /dev/null

echo "==> Replacing the contents of %s"

# Delete first, then unpack: a restore is "make it look like it did", not
# "merge this over whatever is there now". Dotfiles are included; the volume
# itself is kept, because it is the mount point.
find %s -mindepth 1 -delete
tar xzf %s -C %s

echo "==> Restored"
`, archiveFile, archiveFile, dataMount, dataMount, archiveFile, dataMount)
}
