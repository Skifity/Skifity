package vulnscan

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"skifity/internal/version"
)

func testSpec() JobSpec {
	return JobSpec{
		Name: "scan-web-0123456789", Namespace: "skifity-builds", AppID: "app_1", ScanID: "scan_1",
		Image: "10.43.0.12:5000/acme-shop-production/web:abc123def456-1a2b3c4d",
	}
}

// The scanner that runs is the one this file names, by digest: a tag that
// moves is a scanner whose answers change with nothing in the app changed.
func TestTheScanRunsThePinnedTrivy(t *testing.T) {
	if !strings.HasPrefix(TrivyImage, "aquasec/trivy:"+TrivyVersion+"@sha256:") || len(TrivyImage) != len("aquasec/trivy:"+TrivyVersion+"@sha256:")+64 {
		t.Fatalf("the scanner is %q, which is not a version and a digest", TrivyImage)
	}
	job, err := BuildJob(testSpec())
	if err != nil {
		t.Fatal(err)
	}
	pod := job.Spec.Template.Spec
	for _, c := range append(append([]corev1.Container{}, pod.InitContainers...), pod.Containers...) {
		if c.Image != TrivyImage {
			t.Errorf("%s runs %q", c.Name, c.Image)
		}
	}
	if len(pod.InitContainers) != 1 || pod.InitContainers[0].Name != ScanContainer ||
		len(pod.Containers) != 1 || pod.Containers[0].Name != ReportContainer {
		t.Fatalf("the scan is not a scan followed by its report: %+v / %+v", pod.InitContainers, pod.Containers)
	}
}

// The command line asks for what the panel reads, and never for --insecure,
// which would also stop checking the certificate of the database download.
func TestTheScanAsksForAJSONReportOfVulnerabilitiesOnly(t *testing.T) {
	job, err := BuildJob(testSpec())
	if err != nil {
		t.Fatal(err)
	}
	args := job.Spec.Template.Spec.InitContainers[0].Args
	line := strings.Join(args, " ")
	for _, want := range []string{
		"image ", "--format json", "--scanners vuln", "--severity CRITICAL,HIGH,MEDIUM,LOW,UNKNOWN",
		"--quiet", "--image-src remote", "--cache-dir /cache", "--output /report/report.json",
		"--skip-version-check", "--disable-telemetry",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("the command line has no %q: %s", want, line)
		}
	}
	if slices.Contains(args, "--insecure") {
		t.Error("the scan runs with --insecure")
	}
	if args[len(args)-1] != testSpec().Image {
		t.Errorf("the image is not the last argument: %s", line)
	}
	report := job.Spec.Template.Spec.Containers[0]
	if strings.Join(report.Command, " ") != "cat /report/report.json" {
		t.Errorf("the report container runs %q", report.Command)
	}
}

// The scan runs at the restricted level in a namespace that allows more,
// bounded in time, memory and disk.
func TestTheScanIsConfined(t *testing.T) {
	job, err := BuildJob(testSpec())
	if err != nil {
		t.Fatal(err)
	}
	spec := job.Spec
	if spec.BackoffLimit == nil || *spec.BackoffLimit != 0 {
		t.Error("a failed scan is retried")
	}
	if spec.ActiveDeadlineSeconds == nil || *spec.ActiveDeadlineSeconds != DefaultTimeoutSeconds {
		t.Errorf("the deadline is %v", spec.ActiveDeadlineSeconds)
	}
	if spec.TTLSecondsAfterFinished == nil || *spec.TTLSecondsAfterFinished <= 0 {
		t.Error("a finished scan is never collected")
	}
	pod := spec.Template.Spec
	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		t.Error("the scan is given a service account token")
	}
	security := pod.SecurityContext
	if security == nil || security.RunAsNonRoot == nil || !*security.RunAsNonRoot ||
		security.RunAsUser == nil || *security.RunAsUser == 0 ||
		security.SeccompProfile == nil || security.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("the pod is not confined: %+v", security)
	}
	for _, c := range append(append([]corev1.Container{}, pod.InitContainers...), pod.Containers...) {
		s := c.SecurityContext
		if s == nil || s.AllowPrivilegeEscalation == nil || *s.AllowPrivilegeEscalation ||
			s.RunAsNonRoot == nil || !*s.RunAsNonRoot ||
			s.ReadOnlyRootFilesystem == nil || !*s.ReadOnlyRootFilesystem ||
			s.Capabilities == nil || !slices.Equal(s.Capabilities.Drop, []corev1.Capability{"ALL"}) ||
			s.SeccompProfile == nil || s.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
			t.Errorf("%s is not confined: %+v", c.Name, s)
		}
		if c.Resources.Limits.Memory().IsZero() || c.Resources.Requests.Cpu().IsZero() {
			t.Errorf("%s has no memory limit or CPU request: %+v", c.Name, c.Resources)
		}
	}
	for _, v := range pod.Volumes {
		if v.EmptyDir != nil && v.EmptyDir.SizeLimit == nil {
			t.Errorf("the %s scratch space has no size limit", v.Name)
		}
		if v.HostPath != nil {
			t.Errorf("the scan mounts a directory of the server: %s", v.Name)
		}
	}
	if job.Labels["app.kubernetes.io/component"] != "scan" || job.Labels[version.LabelKey("app-id")] != "app_1" {
		t.Errorf("the scan is labelled %v", job.Labels)
	}
}

// A private registry's login reaches the scan as a mounted file, and nothing
// in the Job's spec — which anybody who can list Jobs reads — holds it.
func TestRegistryCredentialsAreAFileAndNotAnArgument(t *testing.T) {
	spec := testSpec()
	spec.Image = "ghcr.io/acme/private-app:1.2.3"
	spec.RegistrySecret = "scan-web-0123456789-auth"
	job, err := BuildJob(spec)
	if err != nil {
		t.Fatal(err)
	}
	scan := job.Spec.Template.Spec.InitContainers[0]

	mounted := false
	for _, m := range scan.VolumeMounts {
		if m.Name == "registry-auth" && m.MountPath == "/docker" && m.ReadOnly {
			mounted = true
		}
	}
	if !mounted {
		t.Errorf("the login is not mounted read-only where DOCKER_CONFIG points: %+v", scan.VolumeMounts)
	}
	env := map[string]string{}
	for _, e := range scan.Env {
		if e.ValueFrom != nil {
			t.Errorf("%s is read from a secret into the environment", e.Name)
		}
		env[e.Name] = e.Value
	}
	if env["DOCKER_CONFIG"] != "/docker" || len(env) != 3 {
		t.Errorf("the environment is %v; it should say where the login is and nothing more", env)
	}

	encoded, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{"password", "username", `"auth"`} {
		if strings.Contains(strings.ToLower(string(encoded)), word) {
			t.Errorf("the Job's spec mentions %q:\n%s", word, encoded)
		}
	}

	// Without a login, there is nothing mounted and nothing said.
	job, err = BuildJob(testSpec())
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range job.Spec.Template.Spec.Volumes {
		if v.Secret != nil {
			t.Errorf("a scan with no login mounts the secret %s", v.Secret.SecretName)
		}
	}
}

// An image reference is an argument to Trivy, never a shell word, and still
// has to be read as an image rather than as one of Trivy's flags.
func TestAnImageThatIsNotAnImageIsRefused(t *testing.T) {
	for _, image := range []string{
		"", "--insecure", "-oops", "nginx latest", "nginx;reboot", "nginx`id`", "$(id)",
		"nginx\n--insecure", strings.Repeat("a", 600),
	} {
		spec := testSpec()
		spec.Image = image
		if _, err := BuildJob(spec); err == nil {
			t.Errorf("%q was accepted as an image", image)
		}
	}
	for _, image := range []string{
		"nginx", "nginx:1.27", "docker.io/library/nginx:1.27-alpine",
		"ghcr.io/acme/app@sha256:5b0bcabd1ed22e9fb1310cf6c2dec7cdef19f0ad69efa1f392e94a4333501270",
		"registry.example.com:5000/team/app:v1.2.3_rc+build",
	} {
		spec := testSpec()
		spec.Image = image
		if _, err := BuildJob(spec); err != nil {
			t.Errorf("%q was refused: %v", image, err)
		}
	}
}

// The database volume is one volume, on one server, asked for once.
func TestTheCacheIsOneVolume(t *testing.T) {
	claim := CacheClaimObject("skifity-builds")
	if claim.Name != CacheClaim || claim.Namespace != "skifity-builds" ||
		len(claim.Spec.AccessModes) != 1 || claim.Spec.AccessModes[0] != corev1.ReadWriteOnce {
		t.Errorf("the cache claim is %+v", claim)
	}
	if claim.Spec.Resources.Requests.Storage().IsZero() {
		t.Error("the cache claim asks for no storage")
	}
	job, _ := BuildJob(testSpec())
	for _, v := range job.Spec.Template.Spec.Volumes {
		if v.Name == "cache" && (v.PersistentVolumeClaim == nil || v.PersistentVolumeClaim.ClaimName != CacheClaim) {
			t.Errorf("the scan does not use the cache claim: %+v", v)
		}
	}
}

// The registry inside the cluster is reached by its private address, which
// Trivy speaks plain HTTP to by itself.
func TestTheRegistryInsideTheClusterIsReachedByAddress(t *testing.T) {
	host := "skifity-registry.skifity-builds.svc.cluster.local:5000"
	image := host + "/acme-shop-production/web:abc"
	cases := []struct {
		image, clusterIP, want string
		ok                     bool
	}{
		{image, "10.43.0.12", "10.43.0.12:5000/acme-shop-production/web:abc", true},
		{image, "192.168.10.4", "192.168.10.4:5000/acme-shop-production/web:abc", true},
		// Not a private range: left as it is, and the scan says it could
		// not reach the image rather than turning off certificate checks.
		{image, "100.64.3.2", image, false},
		{image, "fd00::12", image, false},
		{image, "", image, false},
		{"ghcr.io/acme/web:1", "10.43.0.12", "ghcr.io/acme/web:1", false},
		{"skifity-registry.skifity-builds.svc.cluster.local:5000.evil.test/x:1", "10.43.0.12",
			"skifity-registry.skifity-builds.svc.cluster.local:5000.evil.test/x:1", false},
	}
	for _, c := range cases {
		got, ok := InClusterReference(c.image, host, c.clusterIP, 5000)
		if got != c.want || ok != c.ok {
			t.Errorf("%s at %q became %q (%v), want %q (%v)", c.image, c.clusterIP, got, ok, c.want, c.ok)
		}
	}
}
