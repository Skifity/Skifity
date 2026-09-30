package provision

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"skifity/internal/api"
	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/plugins"
	"skifity/internal/sshx"
	"skifity/internal/store"
)

// Removing a server.
//
// The order matters: stop new work landing on the node, move what is running
// off it, then take it out of the cluster, and only then touch the machine.
// Doing it the other way round drops traffic.

// RemoveServer takes a node out of the cluster and optionally wipes it.
func (p *Provisioner) RemoveServer(ctx context.Context, serverID string, wipe bool) (store.Operation, error) {
	server, err := p.db.GetServer(ctx, serverID)
	if err != nil {
		return store.Operation{}, err
	}

	op := store.Operation{
		TeamID: server.TeamID, Kind: "server.remove",
		TargetType: "server", TargetID: serverID,
	}
	if err := p.db.CreateOperation(ctx, &op, removeServerSteps); err != nil {
		return store.Operation{}, err
	}

	p.start(op, func(runCtx context.Context) {
		p.runRemoveServer(runCtx, op, server, wipe)
	})
	return p.db.GetOperation(ctx, op.ID)
}

func (p *Provisioner) runRemoveServer(ctx context.Context, op store.Operation, server store.Server, wipe bool) {
	defer p.finish(op.ID)

	_ = p.db.SetOperationStatus(ctx, op.ID, store.OpRunning, "", "")
	_ = p.db.SetServerStatus(ctx, server.ID, store.ServerRemoving, "")
	p.publishOperation(ctx, op.ID)

	// Cordon: stop anything new being scheduled here.
	p.setStep(ctx, op, "cordon", store.StepRunning, store.StepNote{}, "")
	if err := p.cordon(ctx, server, true); err != nil {
		p.failStep(ctx, op, server.ID, "cordon", err)
		return
	}
	p.setStep(ctx, op, "cordon", store.StepSucceeded, store.StepNote{Message: "No new instances will be placed here", Key: "cordoned"}, "")

	// Drain: move what is running to the other servers.
	p.setStep(ctx, op, "drain", store.StepRunning, store.StepNote{}, "")
	moved, err := p.drain(ctx, server)
	if err != nil {
		p.failStep(ctx, op, server.ID, "drain", err)
		return
	}
	moveMessage, moveArgs := errdoc.Sprintf("Moved %d instance(s) to the other servers", moved)
	p.setStep(ctx, op, "drain", store.StepSucceeded,
		store.StepNote{Message: moveMessage, Key: "drained", Args: moveArgs}, "")

	// Delete the node object.
	p.setStep(ctx, op, "delete-node", store.StepRunning, store.StepNote{}, "")
	if err := p.deleteNode(ctx, server); err != nil {
		p.failStep(ctx, op, server.ID, "delete-node", err)
		return
	}
	p.setStep(ctx, op, "delete-node", store.StepSucceeded, store.StepNote{Message: "Removed from the cluster", Key: "nodeDeleted"}, "")

	// Clean the machine, so it can be reused.
	p.setStep(ctx, op, "uninstall", store.StepRunning, store.StepNote{}, "")
	if wipe {
		if err := p.uninstall(ctx, server); err != nil {
			// A machine that cannot be reached is not a reason to keep a dead
			// node in the cluster; report it and finish.
			p.setStep(ctx, op, "uninstall", store.StepFailed,
				store.StepNote{Message: "Kubernetes could not be removed from the machine", Key: "uninstallFailed"},
				errdoc.From(err).Text())
			p.log.Warn("could not clean the removed server", "server", server.ID, "error", err)
		} else {
			p.setStep(ctx, op, "uninstall", store.StepSucceeded, store.StepNote{Message: "Kubernetes was removed from the machine", Key: "uninstalled"}, "")
		}
	} else {
		p.setStep(ctx, op, "uninstall", store.StepSkipped,
			store.StepNote{Message: "Kubernetes was left installed on the machine", Key: "uninstallSkipped"}, "")
	}

	if err := p.db.DeleteServer(ctx, server.ID); err != nil {
		p.failOperation(ctx, op, server.ID, "internal", err)
		return
	}
	_ = p.db.SetOperationStatus(ctx, op.ID, store.OpSucceeded, "", "")
	p.publishOperation(ctx, op.ID)
	p.log.Info("server removed", "server", server.ID, "host", server.Host)

	// The server's row is gone by now, so the team comes from the record read
	// before it was deleted rather than from a lookup that would find nothing.
	p.plugins.Notify(ctx, plugins.EventServerRemoved, server.TeamID, map[string]any{
		"server_id": server.ID, "server": server.Name, "host": server.Host,
	})
}

func (p *Provisioner) cordon(ctx context.Context, server store.Server, unschedulable bool) error {
	if p.cluster == nil || server.NodeName == "" {
		return nil
	}
	node, err := p.cluster.Client().Clientset().CoreV1().Nodes().Get(ctx, server.NodeName, metav1.GetOptions{})
	if err != nil {
		if kubeIsNotFound(err) {
			return nil
		}
		return fmt.Errorf("read the node: %w", err)
	}
	if node.Spec.Unschedulable == unschedulable {
		return nil
	}
	node.Spec.Unschedulable = unschedulable
	if _, err := p.cluster.Client().Clientset().CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("cordon the node: %w", err)
	}
	return nil
}

// drain evicts the pods on a node and waits for them to go.
//
// Eviction rather than deletion, so PodDisruptionBudgets are respected: that is
// what stops draining one node taking an app's last instance with it.
func (p *Provisioner) drain(ctx context.Context, server store.Server) (int, error) {
	if p.cluster == nil || server.NodeName == "" {
		return 0, nil
	}
	clientset := p.cluster.Client().Clientset()

	pods, err := clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + server.NodeName,
	})
	if err != nil {
		return 0, fmt.Errorf("list what is running on this server: %w", err)
	}

	evicted := 0
	for _, pod := range pods.Items {
		// DaemonSet pods come back immediately and mirror pods cannot be
		// evicted at all, so skipping them is correct rather than lenient.
		if isDaemonSetPod(pod) || isMirrorPod(pod) {
			continue
		}
		eviction := &policyv1.Eviction{
			ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: pod.Namespace},
		}
		if err := clientset.PolicyV1().Evictions(pod.Namespace).Evict(ctx, eviction); err != nil {
			if kubeIsNotFound(err) {
				continue
			}
			// A budget that refuses the eviction means removing this server
			// would take an app down. That is worth stopping for.
			return evicted, errdoc.New("cluster.drain_blocked", "An app would go down if this server is removed").
				WithCause("%s in %s could not be moved: %s", pod.Name, pod.Namespace, err.Error()).
				WithImpact("The server was not removed and is still running your apps.").
				WithFix("Add another server first, or scale the app that is blocking this to more than one instance so it can move.").
				Retry()
		}
		evicted++
	}

	// Wait for them to actually go, or the node is deleted with work on it.
	deadline := time.Now().Add(5 * time.Minute)
	for {
		remaining, err := clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
			FieldSelector: "spec.nodeName=" + server.NodeName,
		})
		if err != nil {
			return evicted, fmt.Errorf("check what is left on this server: %w", err)
		}
		outstanding := 0
		for _, pod := range remaining.Items {
			if !isDaemonSetPod(pod) && !isMirrorPod(pod) {
				outstanding++
			}
		}
		if outstanding == 0 {
			return evicted, nil
		}
		if time.Now().After(deadline) {
			return evicted, errdoc.New("cluster.drain_timeout", "Some instances would not move").
				WithCause("%d instance(s) were still running on %s after five minutes.", outstanding, server.Name).
				WithImpact("The server was not removed.").
				WithFix("An app with a volume cannot move to another server unless cross-node storage is enabled. Stop that app, or enable Longhorn under Settings, then try again.").
				Retry()
		}
		select {
		case <-ctx.Done():
			return evicted, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func (p *Provisioner) deleteNode(ctx context.Context, server store.Server) error {
	if p.cluster == nil || server.NodeName == "" {
		return nil
	}
	err := p.cluster.Client().Clientset().CoreV1().Nodes().Delete(ctx, server.NodeName, metav1.DeleteOptions{})
	if err != nil && !kubeIsNotFound(err) {
		return fmt.Errorf("remove the node from the cluster: %w", err)
	}
	return nil
}

// uninstall removes k3s from the machine so it can be reused.
func (p *Provisioner) uninstall(ctx context.Context, server store.Server) error {
	if server.SSHKeyEnc == "" {
		return errors.New("there is no stored key for this server, so it cannot be cleaned remotely")
	}
	plaintext, err := p.keyring.Open(server.SSHKeyEnc, serverKeyContext(server.ID))
	if err != nil {
		return fmt.Errorf("read this server's stored key: %w", err)
	}

	client, err := sshx.Dial(ctx, sshx.Config{
		Host: server.Host, Port: server.SSHPort, HostKey: server.HostKey,
		Credentials: sshx.Credentials{User: server.SSHUser, PrivateKey: string(plaintext)},
	})
	if err != nil {
		return err
	}
	defer client.Close()

	result, err := client.Run(ctx, UninstallScript)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("the uninstall script failed: %s", result.Combined())
	}
	return nil
}

// PromoteServer turns a worker into a control plane member.
//
// k3s cannot change a node's role in place, so the node is removed and rejoined
// as a server. Its workloads move away first, which is why this reuses the same
// drain the removal path uses.
func (p *Provisioner) PromoteServer(ctx context.Context, serverID string) (store.Operation, error) {
	server, err := p.db.GetServer(ctx, serverID)
	if err != nil {
		return store.Operation{}, err
	}
	if server.Role == "control-plane" {
		return store.Operation{}, errdoc.BadRequest("This server is already a control plane server.")
	}

	op := store.Operation{
		TeamID: server.TeamID, Kind: "server.promote",
		TargetType: "server", TargetID: serverID,
	}
	// The check comes first, and it is not a formality.
	//
	// Promotion drains the node, removes it from the cluster and uninstalls
	// k3s before reinstalling it as a control plane member. Without this step
	// the first thing to notice that the machine is too small for etcd was the
	// install at the end — by which time the apps had been moved off and a
	// working worker had been turned into nothing at all. The panel refuses to
	// *add* a control plane server below these requirements; it used to promote
	// one without looking.
	steps := []string{StepPreflight, "drain", "leave", StepInstallK3s, StepWaitReady}
	if err := p.db.CreateOperation(ctx, &op, steps); err != nil {
		return store.Operation{}, err
	}

	p.start(op, func(runCtx context.Context) {
		p.runPromote(runCtx, op, server)
	})
	return p.db.GetOperation(ctx, op.ID)
}

func (p *Provisioner) runPromote(ctx context.Context, op store.Operation, server store.Server) {
	defer p.finish(op.ID)

	_ = p.db.SetOperationStatus(ctx, op.ID, store.OpRunning, "", "")
	p.publishOperation(ctx, op.ID)

	// Everything below this point changes the machine, so the check is above it.
	check := &addState{
		operationID: op.ID,
		serverID:    server.ID,
		request:     requestFromServer(server),
	}
	check.request.ControlPlane = true

	p.setStep(ctx, op, StepPreflight, store.StepRunning, store.StepNote{}, "")
	if err := p.stepConnect(ctx, check); err != nil {
		p.failStep(ctx, op, server.ID, StepPreflight, err)
		return
	}
	if err := p.stepPreflight(ctx, check); err != nil {
		check.client.Close()
		p.failStep(ctx, op, server.ID, StepPreflight, err)
		return
	}
	check.client.Close()
	p.setStep(ctx, op, StepPreflight, store.StepSucceeded, check.lastNote, "")

	p.setStep(ctx, op, "drain", store.StepRunning, store.StepNote{}, "")
	if err := p.cordon(ctx, server, true); err != nil {
		p.failStep(ctx, op, server.ID, "drain", err)
		return
	}
	moved, err := p.drain(ctx, server)
	if err != nil {
		p.failStep(ctx, op, server.ID, "drain", err)
		return
	}
	movedMessage, movedArgs := errdoc.Sprintf("Moved %d instance(s) off this server first", moved)
	p.setStep(ctx, op, "drain", store.StepSucceeded,
		store.StepNote{Message: movedMessage, Key: "drainedFirst", Args: movedArgs}, "")

	p.setStep(ctx, op, "leave", store.StepRunning, store.StepNote{}, "")
	if err := p.deleteNode(ctx, server); err != nil {
		p.failStep(ctx, op, server.ID, "leave", err)
		return
	}
	if err := p.uninstall(ctx, server); err != nil {
		p.failStep(ctx, op, server.ID, "leave", err)
		return
	}
	p.setStep(ctx, op, "leave", store.StepSucceeded, store.StepNote{Message: "Left the cluster as a worker", Key: "leftAsWorker"}, "")

	// Rejoin as a control plane member.
	server.Role = "control-plane"
	server.NodeName = ""
	if err := p.db.UpdateServer(ctx, &server); err != nil {
		p.failOperation(ctx, op, server.ID, "internal", err)
		return
	}

	state := &addState{
		operationID: op.ID,
		serverID:    server.ID,
		request:     requestFromServer(server),
	}
	p.setStep(ctx, op, StepInstallK3s, store.StepRunning, store.StepNote{}, "")
	if err := p.stepConnect(ctx, state); err != nil {
		p.failStep(ctx, op, server.ID, StepInstallK3s, err)
		return
	}
	defer state.client.Close()

	if err := p.stepInstallK3s(ctx, state); err != nil {
		p.failStep(ctx, op, server.ID, StepInstallK3s, err)
		return
	}
	p.setStep(ctx, op, StepInstallK3s, store.StepSucceeded, state.lastNote, "")

	p.setStep(ctx, op, StepWaitReady, store.StepRunning, store.StepNote{}, "")
	if err := p.stepWaitReady(ctx, state); err != nil {
		p.failStep(ctx, op, server.ID, StepWaitReady, err)
		return
	}
	p.setStep(ctx, op, StepWaitReady, store.StepSucceeded, state.lastNote, "")

	if err := p.cordon(ctx, server, false); err != nil {
		p.log.Warn("could not uncordon after promotion", "server", server.ID, "error", err)
	}
	_ = p.db.SetOperationStatus(ctx, op.ID, store.OpSucceeded, "", "")
	_ = p.db.SetServerStatus(ctx, server.ID, store.ServerReady, "")
	p.publishOperation(ctx, op.ID)
}

func isDaemonSetPod(pod corev1.Pod) bool {
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

func isMirrorPod(pod corev1.Pod) bool {
	_, ok := pod.Annotations["kubernetes.io/config.mirror"]
	return ok
}

// requestFromServer rebuilds an add-server request from a stored record, for
// the paths that reuse the add machinery on a server that already exists.
func requestFromServer(server store.Server) api.AddServerRequest {
	return api.AddServerRequest{
		TeamID: server.TeamID, Name: server.Name, Host: server.Host,
		SSHPort: server.SSHPort, SSHUser: server.SSHUser,
		ControlPlane: server.Role == "control-plane",
	}
}

// kubeIsNotFound reports whether a Kubernetes error means the object is gone.
func kubeIsNotFound(err error) bool { return kube.IsNotFound(err) }
