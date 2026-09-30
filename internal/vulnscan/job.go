package vulnscan

import (
	"fmt"
	"net"
	"regexp"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"skifity/internal/version"
)

// The scanner, pinned by version and by digest, like every image a build runs
// (internal/builder/images.go): a scanner that moves under a tag is one whose
// answer changes with nothing in the app changed, and whoever can move the tag
// chooses what runs next to every team's registry credentials.
//
// The digest is the multi-architecture index of aquasec/trivy:0.74.0, read
// from Docker Hub's registry API on 2026-09-30 and the same on
// ghcr.io/aquasecurity/trivy. To move it, read the new tag's index digest the
// same way and change the version and the digest together.
const (
	TrivyVersion = "0.74.0"
	TrivyImage   = "aquasec/trivy:" + TrivyVersion +
		"@sha256:62b1e65e8869bc4b4c6aa4fa2b21595256c7c2f6018a9d9ad61caf87187c1969"
)

// CacheClaim is the volume Trivy keeps its vulnerability database on, in the
// build namespace. The database is tens of megabytes to download and is
// refreshed by Trivy when it is a day old; downloading it for every scan of
// every app, every night, is what gets a cluster rate-limited by the registry
// that serves it.
const CacheClaim = "skifity-scan-cache"

// cacheSize is what the claim asks for: the vulnerability database, the Java
// one when a scanned image has jars in it, and room to spare.
const cacheSize = "5Gi"

// The two containers of a scan. Trivy runs first and writes its report to a
// file; the second prints the file, so the log the panel reads is the report
// and nothing else, and what Trivy said when it failed is in a log of its own.
const (
	ScanContainer   = "scan"
	ReportContainer = "report"
)

const (
	cacheDir   = "/cache"
	reportDir  = "/report"
	reportFile = reportDir + "/report.json"
	tmpDir     = "/tmp"
	// dockerConfigDir is where registry credentials are mounted, as
	// config.json: the file Trivy's registry client reads for a login.
	dockerConfigDir = "/docker"
	// scanUID is who the scan runs as. Nobody in particular: Trivy needs no
	// user of its own, only somewhere to write.
	scanUID = 65532
)

// DefaultTimeoutSeconds bounds a whole scan, the database download included.
const DefaultTimeoutSeconds = 15 * 60

// trivyTimeout is Trivy's own limit, inside the Job's, so a scan that takes
// too long ends with Trivy saying so rather than with its pod killed.
const trivyTimeout = "10m"

// JobSpec is one scan.
type JobSpec struct {
	Name      string
	Namespace string
	AppID     string
	ScanID    string
	// Image is the reference Trivy pulls.
	Image string
	// RegistrySecret is a dockerconfigjson Secret, in the same namespace,
	// with the logins the image needs. Empty for a public image, or one in
	// the registry inside the cluster.
	RegistrySecret string
	// TrivyImage overrides the scanner's image, for an air-gapped install.
	TrivyImage string
	// TimeoutSeconds bounds the Job. Zero is DefaultTimeoutSeconds.
	TimeoutSeconds int
}

// imageReference is what an image reference may be: a registry, a path, a
// tag and a digest, in the characters those are made of. It reaches Trivy as
// one argument, never through a shell, so this is about Trivy reading it as
// an image and not as a flag.
var imageReference = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@+-]*$`)

// Validate reports a spec that could not produce a scan.
func (s JobSpec) Validate() error {
	if s.Name == "" || s.Namespace == "" {
		return fmt.Errorf("a scan needs a name and a namespace")
	}
	if s.Image == "" {
		return fmt.Errorf("a scan needs an image")
	}
	if len(s.Image) > 512 || !imageReference.MatchString(s.Image) {
		return fmt.Errorf("%q is not an image reference the scanner can read", s.Image)
	}
	return nil
}

// Args is Trivy's command line for one image.
//
// Every severity is asked for, UNKNOWN included: the panel shows the counts
// and lets people decide, and an advisory nobody has rated yet is still one.
// Only the vulnerability scanner runs — secrets and misconfigurations in an
// image are different questions with different answers — and nothing is sent
// anywhere but the registries: no version check, no telemetry, which is what
// the panel promises for itself.
func Args(image string) []string {
	return []string{
		"image",
		"--format", "json",
		"--output", reportFile,
		"--quiet",
		"--scanners", "vuln",
		"--severity", strings.Join(Severities, ","),
		"--image-src", "remote",
		"--cache-dir", cacheDir,
		"--timeout", trivyTimeout,
		"--skip-version-check",
		"--disable-telemetry",
		image,
	}
}

// BuildJob renders the Kubernetes Job for a scan.
//
// It is confined the way the panel's own work in the build namespace is, and
// more: the namespace allows privileged pods because BuildKit needs one, and
// nothing about a scan does, so it runs at the restricted level anyway — not
// root, no privilege escalation, no capabilities, the runtime's seccomp
// profile, a read-only root filesystem, and no service account token.
func BuildJob(s JobSpec) (*batchv1.Job, error) {
	if s.TrivyImage == "" {
		s.TrivyImage = TrivyImage
	}
	if s.TimeoutSeconds == 0 {
		s.TimeoutSeconds = DefaultTimeoutSeconds
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}

	labels := map[string]string{
		"app.kubernetes.io/name":       s.Name,
		"app.kubernetes.io/managed-by": version.Binary,
		"app.kubernetes.io/component":  "scan",
		version.LabelKey("app-id"):     s.AppID,
		version.LabelKey("scan-id"):    s.ScanID,
	}

	mounts := []corev1.VolumeMount{
		{Name: "cache", MountPath: cacheDir},
		{Name: "report", MountPath: reportDir},
		{Name: "tmp", MountPath: tmpDir},
	}
	volumes := []corev1.Volume{
		{Name: "cache", VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: CacheClaim},
		}},
		{Name: "report", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{
			SizeLimit: ptr(resource.MustParse("256Mi")),
		}}},
		// Layers are unpacked here while they are read. Bounded, so an image
		// of many gigabytes ends this scan rather than filling the server's
		// disk under every app on it.
		{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{
			SizeLimit: ptr(resource.MustParse("8Gi")),
		}}},
	}
	env := []corev1.EnvVar{
		{Name: "HOME", Value: tmpDir},
		{Name: "TMPDIR", Value: tmpDir},
	}
	if s.RegistrySecret != "" {
		// The logins arrive as a file, never as an argument or a variable: a
		// Job's spec is readable by anybody who can list Jobs here.
		volumes = append(volumes, corev1.Volume{
			Name: "registry-auth",
			VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
				SecretName: s.RegistrySecret,
				Items:      []corev1.KeyToPath{{Key: corev1.DockerConfigJsonKey, Path: "config.json"}},
			}},
		})
		mounts = append(mounts, corev1.VolumeMount{Name: "registry-auth", MountPath: dockerConfigDir, ReadOnly: true})
		env = append(env, corev1.EnvVar{Name: "DOCKER_CONFIG", Value: dockerConfigDir})
	}

	scan := corev1.Container{
		Name:            ScanContainer,
		Image:           s.TrivyImage,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Args:            Args(s.Image),
		Env:             env,
		VolumeMounts:    mounts,
		SecurityContext: confined(),
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("256Mi"),
			},
			// Trivy holds a layer's file list and the database's index in
			// memory; a large image with jars in it needs the room.
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			},
		},
	}
	report := corev1.Container{
		Name:            ReportContainer,
		Image:           s.TrivyImage,
		ImagePullPolicy: corev1.PullIfNotPresent,
		// cat, from the image's own Alpine: printing the file is all this
		// container does.
		Command:         []string{"cat", reportFile},
		VolumeMounts:    []corev1.VolumeMount{{Name: "report", MountPath: reportDir, ReadOnly: true}},
		SecurityContext: confined(),
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("16Mi"),
			},
			Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("64Mi")},
		},
	}

	backoff := int32(0) // a failed scan is reported, not retried against a registry that said no
	deadline := int64(s.TimeoutSeconds)
	ttl := int32(3600) // an hour to read what it said, as a build is kept

	return &batchv1.Job{
		TypeMeta:   metav1.TypeMeta{APIVersion: "batch/v1", Kind: "Job"},
		ObjectMeta: metav1.ObjectMeta{Name: s.Name, Namespace: s.Namespace, Labels: labels},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			ActiveDeadlineSeconds:   &deadline,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyNever,
					AutomountServiceAccountToken: ptr(false),
					EnableServiceLinks:           ptr(false),
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: ptr(true),
						RunAsUser:    ptr(int64(scanUID)),
						RunAsGroup:   ptr(int64(scanUID)),
						// The cache volume is written by whoever scanned
						// first; the group makes it every scan's.
						FSGroup:        ptr(int64(scanUID)),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					InitContainers: []corev1.Container{scan},
					Containers:     []corev1.Container{report},
					Volumes:        volumes,
				},
			},
		},
	}, nil
}

// confined is the container half of the restricted profile.
func confined() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: ptr(false),
		RunAsNonRoot:             ptr(true),
		ReadOnlyRootFilesystem:   ptr(true),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}
}

// CacheClaimObject renders the volume the scanner keeps its database on.
//
// ReadWriteOnce, and so on one server: scans run one at a time (Trivy locks
// its cache, and two scans sharing one would wait on each other anyway), and
// each lands where the volume is.
func CacheClaimObject(namespace string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      CacheClaim,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       CacheClaim,
				"app.kubernetes.io/managed-by": version.Binary,
				"app.kubernetes.io/component":  "scan",
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(cacheSize)},
			},
		},
	}
}

// InClusterReference is the reference the scanner pulls an image in the
// registry inside the cluster by: the registry's cluster address in place of
// its name.
//
// That registry speaks plain HTTP. Trivy's registry client uses plain HTTP by
// itself for a private address, and for a name only when told --insecure —
// which also switches off certificate checks on the download of the
// vulnerability database, and a scan whose database anybody on the path could
// have replaced is not worth running. So the scanner is given the address.
//
// ok is false for an image somewhere else, and for a cluster whose services
// are not on a private IPv4 range, which cannot be scanned from its own
// registry this way.
func InClusterReference(image, registryHost, clusterIP string, port int) (string, bool) {
	rest, found := strings.CutPrefix(image, registryHost+"/")
	if !found {
		return image, false
	}
	ip := net.ParseIP(clusterIP)
	if ip == nil || ip.To4() == nil || !ip.IsPrivate() {
		return image, false
	}
	return fmt.Sprintf("%s:%d/%s", ip.To4(), port, rest), true
}

func ptr[T any](v T) *T { return &v }
