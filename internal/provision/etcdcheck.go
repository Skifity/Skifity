package provision

import (
	"context"
	"strconv"
	"strings"

	"skifity/internal/settings"
	"skifity/internal/store"
)

// warnAboutEtcd times a server that is about to run etcd and adds what is
// worse than the thresholds to the step's note, as warnings.
//
// It runs where the server can already reach the control plane servers: for a
// new server, after the firewalls learned its address and the network check
// got through; for a worker being promoted, which is in the cluster already,
// right after the preflight. Earlier, a new server's connections would be
// dropped by the provider firewalls in front of the others, and every round
// trip would read as unmeasured.
func (p *Provisioner) warnAboutEtcd(ctx context.Context, state *addState) {
	if !state.request.ControlPlane {
		return
	}
	limits := p.etcdThresholds(ctx)
	if !limits.roundTrips() && limits.DiskSyncMs == 0 {
		return
	}
	peers, err := p.controlPlanePeers(ctx, state.serverID)
	if err != nil {
		p.log.Warn("could not list the control plane servers to time", "server", state.serverID, "error", err)
	}
	output := ""
	result, err := state.client.Run(ctx, EtcdFitnessScript(peers))
	if err != nil {
		p.log.Warn("could not time the server for etcd", "server", state.serverID, "error", err)
	} else {
		output = result.Stdout
	}
	appendWarnings(&state.lastNote, EvaluateEtcd(ParseEtcdFitness(output, peers), limits))
}

// appendWarnings adds problems to a step's note, each as its sentence and its
// fix, in the reader's language: the keys point into the error catalogue,
// where a preflight problem's words are.
func appendWarnings(note *store.StepNote, problems []Problem) {
	for _, problem := range problems {
		note.Details = append(note.Details,
			store.StepDetail{
				Text: problem.Detail, Key: "preflight." + problem.Code + ".detail",
				Args: problem.Args.Detail,
			},
			store.StepDetail{
				Text: problem.Fix, Key: "preflight." + problem.Code + ".fix",
				Args: problem.Args.Fix,
			})
	}
}

// controlPlanePeers are the addresses of the control plane servers a new one
// would run etcd with, at the address a join uses. The panel's own records
// come first; a cluster the installer built and the panel has no rows for is
// asked, as controlPlaneAddress does.
func (p *Provisioner) controlPlanePeers(ctx context.Context, excludeID string) ([]string, error) {
	servers, err := p.db.ListAllServers(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, s := range servers {
		if s.ID == excludeID || s.Role != "control-plane" {
			continue
		}
		if s.Status != store.ServerReady && s.Status != store.ServerNotReady {
			continue
		}
		address := s.ExternalIP
		if address == "" {
			address = s.Host
		}
		if address != "" && !contains(out, address) {
			out = append(out, address)
		}
	}
	if len(out) > 0 {
		return out, nil
	}
	address, err := p.kubernetesControlPlane(ctx)
	if err != nil || address == "" {
		return nil, err
	}
	return []string{address}, nil
}

// etcdThresholds reads the four settings, each falling back to its default
// when unset or unreadable.
func (p *Provisioner) etcdThresholds(ctx context.Context) EtcdThresholds {
	limits := DefaultEtcdThresholds()
	for key, into := range map[string]*int{
		settings.KeyEtcdRoundTripMs: &limits.RoundTripMs,
		settings.KeyEtcdJitterMs:    &limits.JitterMs,
		settings.KeyEtcdLossPercent: &limits.LossPercent,
		settings.KeyEtcdDiskSyncMs:  &limits.DiskSyncMs,
	} {
		value, _, err := p.db.GetSetting(ctx, key)
		if err != nil || strings.TrimSpace(value) == "" {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && n >= 0 {
			*into = n
		}
	}
	return limits
}
