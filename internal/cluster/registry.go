package cluster

import (
	"context"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"skifity/internal/kube"
	"skifity/internal/registry"
	"skifity/internal/settings"
	"skifity/internal/version"
)

// Keeping the registry from filling the disk.
//
// This is two steps that have to happen in that order and cannot happen in the
// same place. Untagging a manifest is an HTTP call the panel makes itself; it
// removes the reference and frees nothing. The registry's own garbage
// collection is what deletes the blobs, and it reads the storage directory, so
// it runs as a Job next to the files.
//
// Neither step may run while a build is pushing. The registry's garbage
// collection is documented as unsafe against a concurrent upload: it can delete
// a blob that a push has written and not yet referenced. The panel is the only
// thing that starts builds, so it can simply hold them, which is a stronger
// guarantee than a quiet hour.

// registryGCJob is the name of the Job that sweeps the blobs.
const registryGCJob = "skifity-registry-gc"

// BeginBuild registers that a build is running and returns the function that
// says it has finished.
//
// Builds hold this for reading, so any number run at once; the registry sweep
// holds it for writing, so it waits for the builds in flight and holds off the
// ones that have not started.
func (c *Cluster) BeginBuild() func() {
	c.registryMu.RLock()
	return c.registryMu.RUnlock
}

// PruneRegistry removes the images nothing can reach any more.
//
// It reports how many manifests it untagged, which is what the caller logs: a
// sweep that says it did nothing for weeks and then a full disk is the failure
// this exists to prevent, and a number in the log is how somebody notices.
func (c *Cluster) PruneRegistry(ctx context.Context) (int, error) {
	client := registry.New(kube.RegistryHost())

	images, err := c.db.ImagesWorthKeeping(ctx, registry.KeptPerApp)
	if err != nil {
		return 0, err
	}
	// Grouped by repository, because that is how the registry is organised and
	// because a repository with nothing to keep is an app that was deleted.
	keep := map[string]map[string]bool{}
	for _, image := range images {
		repo, tag, ok := registry.RepoAndTag(image)
		if !ok {
			continue
		}
		if keep[repo] == nil {
			keep[repo] = map[string]bool{}
		}
		keep[repo][tag] = true
	}

	repos, err := client.Repositories(ctx)
	if err != nil {
		return 0, err
	}

	removed := 0
	for _, repo := range repos {
		tags, err := client.Tags(ctx, repo)
		if err != nil {
			c.log.Warn("could not list a repository's tags", "repository", repo, "error", err)
			continue
		}
		for _, tag := range registry.Prunable(tags, keep[repo]) {
			digest, err := client.Digest(ctx, repo, tag)
			if registry.IsNotFound(err) {
				continue
			}
			if err != nil {
				c.log.Warn("could not resolve an image to delete",
					"repository", repo, "tag", tag, "error", err)
				continue
			}
			if err := client.DeleteManifest(ctx, repo, digest); err != nil {
				c.log.Warn("could not delete an image",
					"repository", repo, "tag", tag, "error", err)
				continue
			}
			removed++
		}
	}
	return removed, nil
}

// CollectRegistryGarbage untags what is unreachable and then frees the disk.
//
// Builds are held for the duration. A build that starts while the sweep is
// deleting blobs is the one case the registry's own documentation says will
// corrupt an image, and holding them is cheap: this runs weekly and takes
// minutes.
func (c *Cluster) CollectRegistryGarbage(ctx context.Context) error {
	c.registryMu.Lock()
	defer c.registryMu.Unlock()

	// Nothing to sweep if the registry was never installed, which is the normal
	// state of a panel that only runs prebuilt images.
	component, err := c.db.GetComponent(ctx, "registry")
	if err != nil {
		return err
	}
	if component.Status != "installed" {
		return nil
	}

	removed, err := c.PruneRegistry(ctx)
	if err != nil {
		return fmt.Errorf("work out which images are unreachable: %w", err)
	}
	c.log.Info("untagged images nothing can reach", "count", removed)

	namespace := c.client.BuildNamespace()
	// A finished Job with this name would refuse the next one, and the name is
	// the same every sweep — so the delete has to have finished before the next
	// one is applied. Applying onto an object that is still being deleted is
	// accepted by the API server and then collected, which leaves the wait
	// below watching for a Job that is not coming back.
	if err := c.client.Applier().DeleteAndWait(ctx, "batch/v1", "Job", namespace, registryGCJob, 2*time.Minute); err != nil {
		return fmt.Errorf("clear the previous registry sweep: %w", err)
	}
	if err := c.client.Applier().Apply(ctx, registryGCJobSpec(namespace)); err != nil {
		return fmt.Errorf("start the registry sweep: %w", err)
	}
	if err := c.client.WaitForJob(ctx, namespace, registryGCJob, 30*time.Minute); err != nil {
		return fmt.Errorf("the registry sweep did not finish: %w", err)
	}
	c.log.Info("the registry finished collecting its garbage")
	return nil
}

// registryGCJobSpec renders the Job that runs the registry's own collector.
//
// It mounts the registry's volume, which is ReadWriteOnce, so it has to land on
// the node the registry is already running on. The affinity below is what makes
// that happen; without it the Job sits Pending on a two-node cluster and the
// sweep silently never runs.
func registryGCJobSpec(namespace string) *batchv1.Job {
	labels := map[string]string{
		"app.kubernetes.io/name":       registryGCJob,
		"app.kubernetes.io/managed-by": version.Binary,
		"app.kubernetes.io/component":  "maintenance",
	}
	backoff := int32(0)
	deadline := int64(30 * 60)
	ttl := int32(24 * 3600)

	return &batchv1.Job{
		TypeMeta:   metav1.TypeMeta{APIVersion: "batch/v1", Kind: "Job"},
		ObjectMeta: metav1.ObjectMeta{Name: registryGCJob, Namespace: namespace, Labels: labels},
		Spec: batchv1.JobSpec{
			BackoffLimit:          &backoff,
			ActiveDeadlineSeconds: &deadline,
			// Long enough that somebody who saw the disk move can read what it
			// says, short enough that it is gone before the next weekly run.
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyNever,
					AutomountServiceAccountToken: ptrTo(false),
					Affinity: &corev1.Affinity{
						PodAffinity: &corev1.PodAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
								TopologyKey: "kubernetes.io/hostname",
								LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
									"app.kubernetes.io/name": RegistryService,
								}},
							}},
						},
					},
					Containers: []corev1.Container{{
						Name:  "collect",
						Image: "registry:3",
						// --delete-untagged is the half that matters: the panel
						// removed the tags, and without this the manifests they
						// pointed at are kept for a tag that no longer exists.
						Command: []string{"/bin/registry", "garbage-collect",
							"--delete-untagged", "/etc/distribution/config.yml"},
						VolumeMounts: []corev1.VolumeMount{{
							Name: "data", MountPath: "/var/lib/registry",
						}},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("50m"),
								corev1.ResourceMemory: resource.MustParse("64Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("512Mi"),
							},
						},
					}},
					Volumes: []corev1.Volume{{
						Name: "data",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
								ClaimName: RegistryService,
							},
						},
					}},
				},
			},
		},
	}
}

func ptrTo[T any](v T) *T { return &v }

// RegistrySweepInterval is how often the sweep runs.
//
// Weekly. It is not urgent — the disk fills over months — and it holds builds
// while it runs, so doing it more often would cost more than it saves.
const RegistrySweepInterval = 7 * 24 * time.Hour

// MaintainRegistry runs the sweep when it is due, and does nothing otherwise.
//
// The panel's scheduler calls this every minute, so "due" has to survive a
// restart: a panel that is restarted daily would otherwise never reach a weekly
// timer and the disk would fill anyway. The last run is a setting for exactly
// that reason.
func (c *Cluster) MaintainRegistry(ctx context.Context) {
	if !c.db.DueEvery(ctx, settings.KeyRegistrySweptAt, RegistrySweepInterval) {
		return
	}
	if err := c.CollectRegistryGarbage(ctx); err != nil {
		c.log.Warn("the registry sweep did not finish", "error", err)
	}
}
