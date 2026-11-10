package cluster

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"skifity/internal/kube"
	"skifity/internal/store"
)

// The workloads that three components' installs wait for, named once so that
// the install and the check at start cannot come to disagree.
const (
	cnpgOperatorDeployment      = "cnpg-controller-manager"
	kedaOperatorDeployment      = "keda-operator"
	longhornDeployerDeployment  = "longhorn-driver-deployer"
	upgradeControllerDeployment = "system-upgrade-controller"
)

// componentWorkload is the one workload a component cannot be installed
// without: a Deployment, or for the GPU plugin a DaemonSet.
type componentWorkload struct {
	daemonSet bool
	namespace string
	name      string
}

// componentWorkloadFor is what says a component is in the cluster. Every
// component the panel installs has one; a test holds each new one to that.
func (c *Cluster) componentWorkloadFor(name string) (componentWorkload, bool) {
	switch name {
	case "cert-manager":
		// The one the installer and AdoptClusterIssuer look for too.
		return componentWorkload{namespace: "cert-manager", name: "cert-manager"}, true
	case "registry":
		return componentWorkload{namespace: c.client.BuildNamespace(), name: RegistryService}, true
	case "cloudnative-pg":
		return componentWorkload{namespace: kube.DatabaseOperatorNamespace, name: cnpgOperatorDeployment}, true
	case "keda":
		return componentWorkload{namespace: "keda", name: kedaOperatorDeployment}, true
	case "longhorn":
		return componentWorkload{namespace: "longhorn-system", name: longhornDeployerDeployment}, true
	case GuardComponent:
		return componentWorkload{namespace: c.client.SystemNamespace(), name: GuardDeployment}, true
	case TunnelComponent:
		return componentWorkload{namespace: c.client.SystemNamespace(), name: TunnelDeployment}, true
	case UpgradeComponent:
		return componentWorkload{namespace: kube.UpgradeNamespace, name: upgradeControllerDeployment}, true
	case NVIDIAComponent:
		return componentWorkload{daemonSet: true, namespace: kube.GPUNamespace, name: kube.NVIDIADevicePluginName}, true
	}
	return componentWorkload{}, false
}

// SettleComponents records as not installed each component the panel has down
// as installed whose workload is not in the cluster, and returns their names.
//
// The record says what the panel once did to a cluster, and a database
// restored onto a new server says it about one that is gone. Put back that
// way, it listed PostgreSQL, scale to zero and the rest as installed on a
// cluster that had none of them, and nothing would install them again: the
// first database, the Install button and EnsureComponent all go by the record.
// A PostgreSQL database could not even be deleted, without the kind its
// operator defines. Checked at start, which is when a restored database is
// first read. Only an answer that the workload is not there counts; any other
// error stops the check and changes nothing more.
func (c *Cluster) SettleComponents(ctx context.Context) ([]string, error) {
	recorded, err := c.db.ListComponents(ctx)
	if err != nil {
		return nil, err
	}
	gone := []string{}
	for _, component := range recorded {
		if component.Status != "installed" {
			continue
		}
		workload, known := c.componentWorkloadFor(component.Name)
		if !known {
			continue
		}
		present, err := c.workloadPresent(ctx, workload)
		if err != nil {
			return gone, fmt.Errorf("look for %s in the cluster: %w", component.Name, err)
		}
		if present {
			continue
		}
		if err := c.db.SetComponent(ctx, store.ClusterComponent{Name: component.Name, Status: "absent"}); err != nil {
			return gone, err
		}
		c.log.Info("a component recorded as installed is not in the cluster, so it is offered to install again",
			"component", component.Name, "namespace", workload.namespace, "workload", workload.name)
		gone = append(gone, component.Name)
	}
	return gone, nil
}

// workloadPresent reports whether a component's workload exists.
func (c *Cluster) workloadPresent(ctx context.Context, workload componentWorkload) (bool, error) {
	apps := c.client.Clientset().AppsV1()
	var err error
	if workload.daemonSet {
		_, err = apps.DaemonSets(workload.namespace).Get(ctx, workload.name, metav1.GetOptions{})
	} else {
		_, err = apps.Deployments(workload.namespace).Get(ctx, workload.name, metav1.GetOptions{})
	}
	switch {
	case err == nil:
		return true, nil
	case kube.IsNotFound(err):
		return false, nil
	default:
		return false, err
	}
}
