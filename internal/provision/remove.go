package provision

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

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

// RemoveServer takes a node out of the cluster, then wipes the machine, or
// deletes it at the provider the panel created it at.
func (p *Provisioner) RemoveServer(ctx context.Context, serverID string, opts api.RemoveServerOptions) (store.Operation, error) {
	server, err := p.db.GetServer(ctx, serverID)
	if err != nil {
		return store.Operation{}, err
	}
	steps := removeServerSteps
	if opts.DeleteMachine {
		// Asked again here rather than trusted from the caller: the machine
		// that is deleted is only ever one this panel recorded ordering.
		if _, err := p.db.GetCloudServer(ctx, serverID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return store.Operation{}, errdoc.CloudNotCreated(server.Name)
			}
			return store.Operation{}, err
		}
		steps = append(slices.Clone(removeServerSteps), StepCloudDelete)
	}

	op := store.Operation{
		TeamID: server.TeamID, Kind: "server.remove",
		TargetType: "server", TargetID: serverID,
	}
	if err := p.db.CreateOperation(ctx, &op, steps); err != nil {
		return store.Operation{}, err
	}

	p.start(op, func(runCtx context.Context) {
		p.runRemoveServer(runCtx, op, server, opts)
	})
	return p.db.GetOperation(ctx, op.ID)
}

func (p *Provisioner) runRemoveServer(ctx context.Context, op store.Operation, server store.Server, opts api.RemoveServerOptions) {
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
	drained, err := p.drain(ctx, server)
	if err != nil {
		p.failStep(ctx, op, server.ID, "drain", err)
		return
	}
	p.setStep(ctx, op, "drain", store.StepSucceeded, drained.note(), "")

	// Delete the node object.
	p.setStep(ctx, op, "delete-node", store.StepRunning, store.StepNote{}, "")
	if err := p.deleteNode(ctx, server); err != nil {
		p.failStep(ctx, op, server.ID, "delete-node", err)
		return
	}
	p.setStep(ctx, op, "delete-node", store.StepSucceeded, store.StepNote{Message: "Removed from the cluster", Key: "nodeDeleted"}, "")

	// The machines the panel created let this one's address in through
	// their provider's firewall; it is not a member any more.
	if err := p.refreshCloudFirewalls(ctx, server.ID); err != nil {
		p.log.Warn("could not update the cloud firewalls after a server left", "server", server.ID, "error", err)
	}

	// Clean the machine, so it can be reused.
	p.setStep(ctx, op, "uninstall", store.StepRunning, store.StepNote{}, "")
	switch {
	case opts.DeleteMachine:
		// Nothing to clean on a machine that is about to stop existing.
		p.setStep(ctx, op, "uninstall", store.StepSkipped,
			store.StepNote{Message: "Skipped: the machine is being deleted", Key: "uninstallNotNeeded"}, "")
	case opts.Wipe:
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
	default:
		p.setStep(ctx, op, "uninstall", store.StepSkipped,
			store.StepNote{Message: "Kubernetes was left installed on the machine", Key: "uninstallSkipped"}, "")
	}

	// Delete the machine at the provider, last: the server is out of the
	// cluster by now, so nothing is lost if this fails, and the server's row
	// stays so that removing it again, with the box ticked, tries again.
	if opts.DeleteMachine {
		p.setStep(ctx, op, StepCloudDelete, store.StepRunning, store.StepNote{}, "")
		note, err := p.deleteCloudMachine(ctx, server)
		if err != nil {
			p.failStep(ctx, op, server.ID, StepCloudDelete, err)
			return
		}
		p.setStep(ctx, op, StepCloudDelete, store.StepSucceeded, note, "")
	}

	if err := p.db.DeleteServer(ctx, server.ID); err != nil {
		p.failOperation(ctx, op, server.ID, err)
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
// drainResult is what emptying a server did.
type drainResult struct {
	// moved is how many instances went to the other servers or, from a
	// server that had stopped answering, are being started on them.
	moved int
	// unreachable is a server whose node had stopped reporting.
	unreachable bool
	// stranded names what kept its data on the server's own disk, and so
	// cannot be started anywhere else.
	stranded []string
}

// note is the line under the drain step.
func (d drainResult) note() store.StepNote {
	switch {
	case d.unreachable && len(d.stranded) > 0:
		message, args := errdoc.Sprintf("The server was not answering: %d instance(s) on it are being started on the other servers. %s kept their data on it and cannot be; restore them from a backup",
			d.moved, strings.Join(d.stranded, ", "))
		return store.StepNote{Message: message, Key: "drainedUnreachableStranded", Args: args}
	case d.unreachable:
		message, args := errdoc.Sprintf("The server was not answering: %d instance(s) on it are being started on the other servers", d.moved)
		return store.StepNote{Message: message, Key: "drainedUnreachable", Args: args}
	}
	message, args := errdoc.Sprintf("Moved %d instance(s) to the other servers", d.moved)
	return store.StepNote{Message: message, Key: "drained", Args: args}
}

func (p *Provisioner) drain(ctx context.Context, server store.Server) (drainResult, error) {
	if p.cluster == nil || server.NodeName == "" {
		return drainResult{}, nil
	}
	clientset := p.cluster.Client().Clientset()

	pods, err := clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + server.NodeName,
	})
	if err != nil {
		return drainResult{}, fmt.Errorf("list what is running on this server: %w", err)
	}

	// A server that has stopped answering cannot be emptied. An eviction
	// asks the server to stop an instance, nothing there is listening, and
	// the instance stays "terminating" for as long as the node is listed: the
	// removal waited five minutes for that and stopped with "some instances
	// would not move", so a server that was gone for good could not be
	// removed at all. Its instances stopped when it did. Deleting its node,
	// the next step, is what has Kubernetes start them on the other servers.
	node, err := clientset.CoreV1().Nodes().Get(ctx, server.NodeName, metav1.GetOptions{})
	if err == nil && unreachable(node) {
		return abandon(ctx, clientset, pods.Items, node), nil
	}

	// The apps with no ready instance anywhere else start one elsewhere
	// first; see moveSoleInstances. What is left is evicted.
	movedFirst := moveSoleInstances(ctx, clientset, p.log, pods.Items, server.NodeName, moveWait)
	pods, err = clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + server.NodeName,
	})
	if err != nil {
		return drainResult{}, fmt.Errorf("list what is running on this server: %w", err)
	}

	evicted := movedFirst
	evictBy := time.Now().Add(2 * time.Minute)
	for _, pod := range pods.Items {
		// DaemonSet pods come back immediately and mirror pods cannot be
		// evicted at all, so skipping them is correct rather than lenient.
		if isDaemonSetPod(pod) || isMirrorPod(pod) || pod.DeletionTimestamp != nil {
			continue
		}
		if err := evict(ctx, clientset, pod, evictBy); err != nil {
			// A budget that still refuses the eviction means removing this
			// server would take an app down. That is worth stopping for.
			return drainResult{moved: evicted}, errdoc.New("cluster.drain_blocked", "An app would go down if this server is removed").
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
			return drainResult{moved: evicted}, fmt.Errorf("check what is left on this server: %w", err)
		}
		outstanding := 0
		for _, pod := range remaining.Items {
			if !isDaemonSetPod(pod) && !isMirrorPod(pod) {
				outstanding++
			}
		}
		if outstanding == 0 {
			return drainResult{moved: evicted}, nil
		}
		if time.Now().After(deadline) {
			return drainResult{moved: evicted}, errdoc.New("cluster.drain_timeout", "Some instances would not move").
				WithCause("%d instance(s) were still running on %s after five minutes.", outstanding, server.Name).
				WithImpact("The server was not removed.").
				WithFix("An app with a volume cannot move to another server unless cross-node storage is enabled. Stop that app, or enable Longhorn under Settings, then try again.").
				Retry()
		}
		select {
		case <-ctx.Done():
			return drainResult{moved: evicted}, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// unreachable reports whether a node has stopped reporting, which the node
// controller says with a Ready condition of Unknown. A node that reports
// itself not ready is still answering, and can still stop what runs on it.
func unreachable(node *corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionUnknown
		}
	}
	return false
}

// abandon is what taking out a server that has stopped answering does with
// what was on it: nothing, here. Its instances are started on the other
// servers once its node is deleted. What kept its data on the server's own
// disk cannot be, wherever it is started, and is named so that nobody finds
// out by waiting.
func abandon(ctx context.Context, clientset kubernetes.Interface, pods []corev1.Pod, node *corev1.Node) drainResult {
	result := drainResult{unreachable: true}
	named := map[string]bool{}
	for _, pod := range pods {
		// A finished build or command is not an instance, and nothing starts
		// it again.
		if isDaemonSetPod(pod) || isMirrorPod(pod) ||
			pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		if !keptDataOn(ctx, clientset, pod, node) {
			result.moved++
			continue
		}
		if name := workloadName(pod); !named[name] {
			named[name] = true
			result.stranded = append(result.stranded, name)
		}
	}
	slices.Sort(result.stranded)
	return result
}

// keptDataOn reports whether a pod has a volume that lives on this node's own
// disk: a claim bound to a volume only this node can mount, which is what the
// storage k3s ships gives every claim.
func keptDataOn(ctx context.Context, clientset kubernetes.Interface, pod corev1.Pod, node *corev1.Node) bool {
	names := []string{node.Name, node.Labels[corev1.LabelHostname]}
	for _, volume := range pod.Spec.Volumes {
		if volume.PersistentVolumeClaim == nil {
			continue
		}
		claim, err := clientset.CoreV1().PersistentVolumeClaims(pod.Namespace).Get(ctx, volume.PersistentVolumeClaim.ClaimName, metav1.GetOptions{})
		if err != nil || claim.Spec.VolumeName == "" {
			continue
		}
		pv, err := clientset.CoreV1().PersistentVolumes().Get(ctx, claim.Spec.VolumeName, metav1.GetOptions{})
		if err != nil || pv.Spec.NodeAffinity == nil || pv.Spec.NodeAffinity.Required == nil {
			continue
		}
		for _, term := range pv.Spec.NodeAffinity.Required.NodeSelectorTerms {
			for _, expression := range term.MatchExpressions {
				for _, value := range expression.Values {
					if value != "" && slices.Contains(names, value) {
						return true
					}
				}
			}
		}
	}
	return false
}

// workloadName is what a person called the app or database a pod belongs to,
// as near as its labels say.
func workloadName(pod corev1.Pod) string {
	for _, key := range []string{"cnpg.io/cluster", "app.kubernetes.io/name"} {
		if name := pod.Labels[key]; name != "" {
			return name
		}
	}
	return pod.Name
}

func (p *Provisioner) deleteNode(ctx context.Context, server store.Server) error {
	if p.cluster == nil || server.NodeName == "" {
		return nil
	}
	clientset := p.cluster.Client().Clientset()
	stopServing(ctx, clientset, p.log, server.NodeName, leaveWait)
	err := clientset.CoreV1().Nodes().Delete(ctx, server.NodeName, metav1.DeleteOptions{})
	if err != nil && !kubeIsNotFound(err) {
		return fmt.Errorf("remove the node from the cluster: %w", err)
	}
	return nil
}

// leavingTaint is put on a server once its apps have moved, just before its
// node is deleted.
//
// What a drain leaves is what runs on every server, the ingress among it, and
// a node deleted with the ingress still on it takes it away mid-request:
// whatever the other servers had just sent there, and the route to it, go at
// once. A run with the second server across a slow link lost a request that
// way. NoExecute stops those pods the way a drain stops an app's: the ingress
// is told to stop, the rest of the cluster stops sending it new requests, and
// it finishes the ones it holds.
var leavingTaint = corev1.Taint{Key: "skifity.io/leaving", Effect: corev1.TaintEffectNoExecute}

// leaveWait is how long the node's own pods are given to stop. The ingress
// takes about fifteen seconds; the node is deleted after this either way.
const leaveWait = 90 * time.Second

// stopServing taints a node that is about to be deleted and waits for the
// pods that do not tolerate it to stop. It is best effort: a server that is
// not answering cannot stop anything, and is deleted as it is.
func stopServing(ctx context.Context, clientset kubernetes.Interface, log *slog.Logger, nodeName string, wait time.Duration) {
	node, err := clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil || unreachable(node) {
		return
	}
	if !slices.ContainsFunc(node.Spec.Taints, func(t corev1.Taint) bool { return t.Key == leavingTaint.Key }) {
		node.Spec.Taints = append(node.Spec.Taints, leavingTaint)
		if _, err := clientset.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{}); err != nil {
			log.Warn("could not stop the ingress on a server being removed", "node", nodeName, "error", err)
			return
		}
	}
	deadline := time.Now().Add(wait)
	for {
		pods, err := clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: "spec.nodeName=" + nodeName})
		if err != nil {
			return
		}
		left := 0
		for _, pod := range pods.Items {
			if pod.Spec.NodeName == nodeName && isDaemonSetPod(pod) && !toleratesLeaving(pod) &&
				pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed {
				left++
			}
		}
		if left == 0 {
			return
		}
		if time.Now().After(deadline) {
			log.Warn("pods on a server being removed had not stopped; removing it anyway", "node", nodeName, "pods", left)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// toleratesLeaving reports whether a pod stays on a node with leavingTaint:
// one that tolerates every taint, or this one by name.
func toleratesLeaving(pod corev1.Pod) bool {
	for _, t := range pod.Spec.Tolerations {
		if t.Effect != "" && t.Effect != leavingTaint.Effect {
			continue
		}
		if (t.Key == "" && t.Operator == corev1.TolerationOpExists) || t.Key == leavingTaint.Key {
			return true
		}
	}
	return false
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
	drained, err := p.drain(ctx, server)
	if err != nil {
		p.failStep(ctx, op, server.ID, "drain", err)
		return
	}
	movedMessage, movedArgs := errdoc.Sprintf("Moved %d instance(s) off this server first", drained.moved)
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
		p.failOperation(ctx, op, server.ID, err)
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
