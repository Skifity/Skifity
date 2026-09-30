package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"skifity/internal/store"
)

// Locking an app's deploys: nothing is deployed or rolled back, from
// anywhere, until somebody unlocks it.

type lockInput struct {
	AppID  string `json:"app_id" jsonschema:"the app's id"`
	Reason string `json:"reason" jsonschema:"why, in a sentence, so whoever finds it locked knows who to ask and when it is over; up to 200 characters"`
}

type lockOutput struct {
	Reason   string `json:"reason"`
	LockedBy string `json:"locked_by"`
	LockedAt string `json:"locked_at"`
	Note     string `json:"note"`
}

func (s *Server) registerLocks() {
	addTool(s, &mcp.Tool{
		Name:        "lock_deploys",
		Annotations: changes("Lock deploys", false, true),
		Description: "Stop every deploy and rollback of an app — pushes, the panel, the CLI and deploy_app alike — until unlock_deploys, with a reason everybody sees. " +
			"For a freeze: a launch, an incident, a migration that must not be interrupted. The running version keeps running.",
	}, s.lockDeploys)

	addTool(s, &mcp.Tool{
		Name: "unlock_deploys",
		// Destructive: a lock is somebody's decision that nothing ships, and
		// lifting it is what that decision was meant to stop.
		Annotations: changes("Unlock deploys", true, true),
		Description: "Let an app deploy again after lock_deploys. Somebody locked it on purpose, and deploy_app's refusal says who and why: " +
			"unlock only when the person asks, never to get a deploy of your own through.",
	}, s.unlockDeploys)
}

func (s *Server) lockDeploys(ctx context.Context, _ *mcp.CallToolRequest, in lockInput) (*mcp.CallToolResult, lockOutput, error) {
	var lock store.DeployLock
	if err := s.client.Do(ctx, "PUT", appPath(in.AppID, "/lock"), map[string]string{"reason": in.Reason}, &lock); err != nil {
		return errorResult(err), lockOutput{}, nil
	}
	out := lockOutput{
		Reason: lock.Reason, LockedBy: lock.LockedBy, LockedAt: stamp(lock.LockedAt),
		Note: "Deploys and rollbacks of this app are refused until unlock_deploys. What is running keeps running.",
	}
	return textResult(fmt.Sprintf("Locked: %s. %s", lock.Reason, out.Note)), out, nil
}

func (s *Server) unlockDeploys(ctx context.Context, _ *mcp.CallToolRequest, in appIDInput) (*mcp.CallToolResult, doneOutput, error) {
	if err := s.client.Do(ctx, "DELETE", appPath(in.AppID, "/lock"), nil, nil); err != nil {
		return errorResult(err), doneOutput{}, nil
	}
	return done("Unlocked. The app can be deployed and rolled back again.")
}
