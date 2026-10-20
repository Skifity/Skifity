package kube

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// FenceContainerName is the first container of every pod that runs a team's
// code.
const FenceContainerName = "fence"

// FenceContainer holds a new pod back until its network policy is in place.
//
// The policy controller writes a pod's rules a moment after the pod has an
// address, and until then nothing fences it: see internal/netfence, and the
// first real cluster, where a pod in an app's namespace reached the Kubernetes
// API on its server's address in that moment. The team's code starts after
// this container has seen its own connections refused.
//
// It is the panel's own image, running as nobody, with nothing writable and
// nothing to add, and small enough that it counts for nothing against the
// environment's quota: a pod's reservation is its containers' or its largest
// init container's, whichever is more.
func FenceContainer(image string) corev1.Container {
	return corev1.Container{
		Name:            FenceContainerName,
		Image:           image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         []string{"/usr/local/bin/skifity", "wait-for-fence"},
		Env: []corev1.EnvVar{{
			Name: "SKIFITY_HOST_IP",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{FieldPath: "status.hostIP"},
			},
		}},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:              resource.MustParse("10m"),
				corev1.ResourceMemory:           resource.MustParse("16Mi"),
				corev1.ResourceEphemeralStorage: resource.MustParse("1Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:              resource.MustParse("100m"),
				corev1.ResourceMemory:           resource.MustParse("32Mi"),
				corev1.ResourceEphemeralStorage: resource.MustParse("16Mi"),
			},
		},
		SecurityContext: &corev1.SecurityContext{
			// distroless's "nonroot", which the panel's image runs as.
			RunAsNonRoot:             ptr(true),
			RunAsUser:                ptr(int64(65532)),
			RunAsGroup:               ptr(int64(65532)),
			AllowPrivilegeEscalation: ptr(false),
			ReadOnlyRootFilesystem:   ptr(true),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
	}
}

// withFence puts the fence first among a pod's init containers, when there is
// an image to run it from. There is none for a panel outside the cluster it
// manages, which has no image of its own there; its pods start as they did.
func withFence(image string, initContainers []corev1.Container) []corev1.Container {
	if image == "" {
		return initContainers
	}
	return append([]corev1.Container{FenceContainer(image)}, initContainers...)
}
