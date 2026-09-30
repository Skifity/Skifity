package kube

import (
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"skifity/internal/cron"
	"skifity/internal/version"
)

// One-off commands.
//
// Every product like this needs a way to run something once, in the app's own
// image, with the app's own variables: a database migration, a management
// command, a look around. Without it a migration has nowhere to run, and the
// answer people reach for is a shell on the server and kubectl — which is the
// thing the panel exists to avoid.
//
// A run is a Kubernetes Job, not an exec into a running pod. An exec needs a
// pod that is already up, which is exactly what a failed migration prevents,
// and it competes for the CPU and memory the app is serving with. A Job runs
// on its own, can be watched, and leaves a log.

// RunKindOneOff is a command somebody asked for.
const RunKindOneOff = "one-off"

// RunKindRelease is the release command, run as part of a deployment.
const RunKindRelease = "release"

// RunKindScheduled is a command that runs on a schedule.
const RunKindScheduled = "scheduled"

// RunSpec is a one-off command in an app's own environment.
type RunSpec struct {
	// App is the app the command borrows its image, variables, namespace and
	// resource limits from.
	App AppSpec
	// Name is the Job's name, which is also how its log is found again.
	Name string
	// Command is a shell line, run the way the app's start command is: people
	// type `npm run migrate && npm run seed`, and splitting that here would
	// get the quoting subtly wrong.
	Command string
	// Kind is one-off or release, and is a label so the two can be told apart.
	Kind string
	// TimeoutSeconds bounds the run. Zero means thirty minutes.
	TimeoutSeconds int
}

// Validate reports a run that could not work.
func (s RunSpec) Validate() error {
	if s.Name == "" || s.App.Namespace == "" {
		return fmt.Errorf("a run needs a name and a namespace")
	}
	if s.App.Image == "" {
		return fmt.Errorf("this app has not been built yet, so there is no image to run the command in")
	}
	if s.Command == "" {
		return fmt.Errorf("a run needs a command")
	}
	return nil
}

// BuildRunJob renders the Job for a one-off command.
func BuildRunJob(s RunSpec) (*batchv1.Job, error) {
	if s.TimeoutSeconds == 0 {
		s.TimeoutSeconds = 30 * 60
	}
	if s.Kind == "" {
		s.Kind = RunKindOneOff
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}

	labels := s.App.Labels()
	labels["app.kubernetes.io/component"] = "run"
	labels[version.LabelKey("run-kind")] = s.Kind

	// The pod's labels are deliberately not the Job's.
	//
	// An app's Service, its disruption budget, its topology spread and the
	// panel's own "which pods are this app" queries all select on the app's
	// name and instance together, and so does the Deployment, which is what
	// the autoscaler reads its metrics through. A migration pod carrying both
	// would be listed on the instances tab as though it were serving traffic,
	// have its CPU averaged into the decision to scale the app up, and be
	// offered as the pod to read the app's logs from — which is how somebody
	// opens the logs tab during a nightly job and reads the job instead.
	//
	// Changing the name label is enough to fall out of every one of those
	// selectors while the app-id, project and team labels still say who this
	// belongs to. The Job's own labels keep the app's name, because that is
	// what a run is looked up by.
	podLabels := make(map[string]string, len(labels))
	for k, v := range labels {
		podLabels[k] = v
	}
	podLabels["app.kubernetes.io/name"] = s.Name

	// The same confinement the app itself runs under, and not a second set of
	// rules. An environment lowered so that its app can run an image that
	// starts as root, where a migration in that same image was still refused,
	// is a panel saying "your app runs, and you cannot run anything in it".
	confinement := s.App.Confinement()

	container := corev1.Container{
		Name:            "run",
		Image:           s.App.Image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		// A shell, because that is what the person typed into.
		Command:   []string{"/bin/sh", "-c"},
		Args:      []string{s.Command},
		Resources: buildResources(s.App),
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr(false),
			RunAsNonRoot:             confinement.RunAsNonRoot(),
			SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
	}
	if confinement.DropAllCapabilities() {
		container.SecurityContext.Capabilities = &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}
	}
	if s.App.EnvFromSecret != "" {
		// The whole point: the same DATABASE_URL the app runs with.
		container.EnvFrom = []corev1.EnvFromSource{{
			SecretRef: &corev1.SecretEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: s.App.EnvFromSecret},
			},
		}}
	}
	container.Env = buildPlainEnv(s.App)

	// Volumes are mounted too, because a command that writes a file the app
	// then reads has to write it where the app looks.
	for _, v := range s.App.Volumes {
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name: v.Name, MountPath: v.MountPath,
		})
	}

	podSpec := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever,
		Containers:    []corev1.Container{container},
		SecurityContext: &corev1.PodSecurityContext{
			RunAsNonRoot: confinement.RunAsNonRoot(),
			RunAsUser:    confinement.RunAsUser(),
			RunAsGroup:   confinement.RunAsUser(),
			// As on the app itself: the group is what makes a mounted volume
			// writable by a process whose uid nobody here knows.
			FSGroup: ptr(int64(1000)),
		},
		AutomountServiceAccountToken: ptr(false),
	}
	for _, v := range s.App.Volumes {
		podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
			Name: v.Name,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: ResourceName(s.App.Name, v.Name),
				},
			},
		})
	}

	// No retries. A migration that half-ran and then ran again is worse than
	// one that failed and said so.
	backoff := int32(0)
	deadline := int64(s.TimeoutSeconds)
	// Long enough that somebody can read the log after it finished, short
	// enough that finished Jobs do not pile up in the namespace.
	ttl := int32(3600)

	return &batchv1.Job{
		TypeMeta:   metav1.TypeMeta{APIVersion: "batch/v1", Kind: "Job"},
		ObjectMeta: metav1.ObjectMeta{Name: s.Name, Namespace: s.App.Namespace, Labels: labels},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			ActiveDeadlineSeconds:   &deadline,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels},
				Spec:       podSpec,
			},
		},
	}, nil
}

// RunJobName builds a unique, valid name for a run.
func RunJobName(appSlug, kind, id string) string {
	prefix := appSlug + "-run"
	if kind == RunKindRelease {
		prefix = appSlug + "-release"
	}
	if len(id) > 10 {
		id = id[:10]
	}
	return ResourceName(prefix, id)
}

// BuildCronJob renders a scheduled command.
//
// Kubernetes does the scheduling rather than the panel, deliberately: a panel
// that is restarting at 03:00 should not be the reason a nightly job did not
// run, and a cluster that outlives this process should keep running the things
// it was told to.
func BuildCronJob(s RunSpec, schedule string) (*batchv1.CronJob, error) {
	job, err := BuildRunJob(s)
	if err != nil {
		return nil, err
	}
	if schedule == "" {
		return nil, fmt.Errorf("a scheduled command needs a schedule")
	}
	// Canonicalised here rather than by the caller, so that every rendering of
	// a scheduled command agrees: the one applied to the cluster and the one
	// the Advanced view shows. Kubernetes parses this string with its own
	// implementation, and cron has two spellings for Sunday; see
	// cron.Canonical. Doing it in one caller meant the panel sent one string
	// and displayed another.
	schedule, err = cron.Canonical(schedule)
	if err != nil {
		return nil, err
	}

	// Forbid, not Allow: a job that is still running when the next one is due
	// is a job that takes longer than its interval, and two copies of a
	// nightly report is worse than one late one.
	policy := batchv1.ForbidConcurrent
	var successful int32 = 3
	var failed int32 = 3
	// A job that could not start for a while runs once when it can, rather
	// than firing every missed interval at once.
	var startingDeadline int64 = 300

	// A one-off Job deletes itself an hour after it finishes, because nobody
	// comes back to a command they ran by hand a day later. A scheduled one
	// must not: the history limits above are the promise that the last three
	// runs are there to look at, and an hourly self-deletion would empty a
	// nightly job's history long before anybody woke up to read it. Kubernetes
	// keeps exactly as many as the limits say.
	template := job.Spec
	template.TTLSecondsAfterFinished = nil

	return &batchv1.CronJob{
		TypeMeta:   metav1.TypeMeta{APIVersion: "batch/v1", Kind: "CronJob"},
		ObjectMeta: metav1.ObjectMeta{Name: s.Name, Namespace: s.App.Namespace, Labels: job.Labels},
		Spec: batchv1.CronJobSpec{
			Schedule:                   schedule,
			ConcurrencyPolicy:          policy,
			StartingDeadlineSeconds:    &startingDeadline,
			SuccessfulJobsHistoryLimit: &successful,
			FailedJobsHistoryLimit:     &failed,
			// TimeZone is deliberately unset, so a schedule means UTC. A
			// cluster's idea of local time is not something anybody chose.
			JobTemplate: batchv1.JobTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: job.Labels},
				Spec:       template,
			},
		},
	}, nil
}

// CronJobName builds a valid name for a scheduled command.
func CronJobName(appSlug, jobName string) string {
	return ResourceName(appSlug+"-job", Slugify(jobName))
}
