package api

import (
	"context"
	"time"
)

// DriftReport says whether an app's objects in the cluster are still the ones
// the panel applied.
type DriftReport struct {
	// Status is in_sync, drifted (an object was changed), missing (an object
	// was deleted), not_deployed (nothing to compare yet), or applying (a
	// deploy or a sync is changing the app, and it is compared afterwards).
	Status string      `json:"status"`
	Items  []DriftItem `json:"items"`
	// CheckedAt is when the cluster was read.
	CheckedAt time.Time `json:"checked_at"`
	// Since is when the watcher first found the app not matching, while it
	// still does not.
	Since time.Time `json:"since,omitzero"`
	// AutoRepair says the watcher puts the objects back by itself.
	AutoRepair bool `json:"auto_repair"`
}

// DriftItem is one difference: an object deleted, or one field of one changed.
// Object kinds and field paths are Kubernetes' own, because this is only ever
// shown under Advanced.
type DriftItem struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Path is empty for a deleted object.
	Path string `json:"path,omitempty"`
	// Change is changed, removed or deleted.
	Change string `json:"change"`
	// Panel and Live are the two values as text. A Secret's are never sent:
	// Hidden says there was a difference to hide.
	Panel  string `json:"panel,omitempty"`
	Live   string `json:"live,omitempty"`
	Hidden bool   `json:"hidden,omitempty"`
	// ChangedBy is who changed it, as Kubernetes records the tool:
	// kubectl-edit, kubectl-patch, helm. Empty when it cannot say.
	ChangedBy string    `json:"changed_by,omitempty"`
	ChangedAt time.Time `json:"changed_at,omitzero"`
}

// DriftDetector compares an app's objects with what the panel applies, and
// puts them back.
type DriftDetector interface {
	// CheckDrift reads the cluster now.
	CheckDrift(ctx context.Context, appID string) (DriftReport, error)
	// RepairDrift applies the app's objects again, the same apply a change
	// to a variable makes: nothing is built, and no deployment is recorded.
	RepairDrift(ctx context.Context, appID string) error
}

// ObjectEvent is one thing Kubernetes said about one of an app's or a
// database's objects, with its repeats folded into a count.
type ObjectEvent struct {
	// Type is Normal or Warning.
	Type    string `json:"type"`
	Reason  string `json:"reason"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Message string `json:"message"`
	Count   int    `json:"count"`
	// FirstSeen and LastSeen span the repeats.
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	// Explanation is what it means for the app, in English, when the panel
	// knows. ExplanationCode is the key the interface finds its own
	// language's words under, events.explain.<code>, and ExplanationArgs the
	// values in them.
	Explanation     string   `json:"explanation,omitempty"`
	ExplanationCode string   `json:"explanation_code,omitempty"`
	ExplanationArgs []string `json:"explanation_args,omitempty"`
}
