package deploy

import (
	"context"
	"fmt"

	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// ResumeInterrupted picks up the deployments a panel restart cut off, and
// answers with how many it started again.
//
// They used to be failed on the next start with "deploy again" as the fix, so
// a push that landed while the panel was being upgraded was lost, and so was
// a build twenty minutes in. Each is now handed back to run, which starts from
// where the deployment stood: one with no image yet builds — the build deletes
// whatever its predecessor left of the same Job first — and one whose image
// exists goes straight to the rollout, which applies the same objects again.
// The release command runs again too, which a release command has to survive
// anyway: a deploy that fails after it is retried the same way.
//
// Once per deployment. One the panel was running when it stopped a second
// time is failed instead, because the deployment may be why the panel stops.
// Of several left unfinished for one app only the newest is resumed; the rest
// had already been replaced by it, and are marked so.
func (d *Deployer) ResumeInterrupted(ctx context.Context) (int, error) {
	unfinished, err := d.db.ListUnfinishedDeployments(ctx)
	if err != nil {
		return 0, err
	}
	// Oldest first, so the last one seen for an app is its newest.
	newest := map[string]string{}
	for _, deployment := range unfinished {
		newest[deployment.AppID] = deployment.ID
	}

	resumed := 0
	for _, deployment := range unfinished {
		if newest[deployment.AppID] != deployment.ID {
			d.log.Info("an interrupted deployment was replaced by a newer one", "deployment", deployment.ID)
			if err := d.db.UpdateDeploymentStatus(ctx, deployment.ID, store.DeploySuperseded, "", "", ""); err != nil {
				return resumed, err
			}
			continue
		}

		claimed, err := d.db.ClaimDeploymentResume(ctx, deployment.ID)
		if err != nil {
			return resumed, err
		}
		if !claimed {
			problem := errdoc.DeployInterrupted()
			d.log.Info("an interrupted deployment was interrupted again, and is failed", "deployment", deployment.ID)
			if err := d.db.UpdateDeploymentStatus(ctx, deployment.ID, store.DeployFailed,
				problem.Code, problem.Cause, problem.Fix); err != nil {
				return resumed, err
			}
			continue
		}

		d.appendLog(ctx, deployment.ID, resumeNote(deployment))
		d.log.Info("resuming a deployment a restart interrupted", "deployment", deployment.ID, "status", deployment.Status)
		id := deployment.ID
		d.start(id, func(runCtx context.Context) {
			d.run(runCtx, id)
		})
		resumed++
	}
	return resumed, nil
}

// resumeNote is the line the deployment's log gets when it is picked up
// again, saying from where.
func resumeNote(deployment store.Deployment) string {
	from := "the start"
	switch {
	case deployment.Image != "":
		from = "the rollout: the image was already built"
	case deployment.Status == store.DeployBuilding:
		from = "the build, which starts again"
	}
	return fmt.Sprintf("The panel restarted while this deployment was %s. It picks up again from %s.",
		deployment.Status, from)
}
