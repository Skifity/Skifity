package store

import (
	"encoding/json"
	"fmt"
)

// RecordedSpec is the part of a deployment's runtime spec a rollback puts back.
type RecordedSpec struct {
	Replicas     int    `json:"replicas"`
	Autoscale    bool   `json:"autoscale"`
	MinReplicas  int    `json:"min_replicas"`
	MaxReplicas  int    `json:"max_replicas"`
	CPUTarget    int    `json:"cpu_target"`
	CPURequestM  int    `json:"cpu_request_m"`
	CPULimitM    int    `json:"cpu_limit_m"`
	MemRequestMB int    `json:"mem_request_mb"`
	MemLimitMB   int    `json:"mem_limit_mb"`
	Port         int    `json:"port"`
	HealthPath   string `json:"health_path"`
	StartCommand string `json:"start_command"`
}

// SpecChange is one setting a rollback would change.
type SpecChange struct {
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// RollbackChanges says which of an app's settings rolling back to a
// deployment's recorded spec would change, from what to what, in the order a
// person reads them. Only what restoreRuntimeSpec actually puts back is
// listed, so the answer is the rollback and not a guess at it.
func RollbackChanges(app App, encoded string) ([]SpecChange, error) {
	changes := []SpecChange{}
	if encoded == "" || encoded == "{}" {
		return changes, nil
	}
	var spec RecordedSpec
	if err := json.Unmarshal([]byte(encoded), &spec); err != nil {
		return nil, fmt.Errorf("read the recorded settings: %w", err)
	}
	add := func(field string, from, to any) {
		f, t := fmt.Sprint(from), fmt.Sprint(to)
		if f != t {
			changes = append(changes, SpecChange{Field: field, From: f, To: t})
		}
	}
	add("replicas", app.Replicas, spec.Replicas)
	add("autoscale", app.Autoscale, spec.Autoscale)
	add("min_replicas", app.MinReplicas, spec.MinReplicas)
	add("max_replicas", app.MaxReplicas, spec.MaxReplicas)
	add("cpu_target", app.CPUTarget, spec.CPUTarget)
	add("cpu_request_m", app.CPURequestM, spec.CPURequestM)
	add("cpu_limit_m", app.CPULimitM, spec.CPULimitM)
	add("mem_request_mb", app.MemRequestMB, spec.MemRequestMB)
	add("mem_limit_mb", app.MemLimitMB, spec.MemLimitMB)
	add("port", app.Port, spec.Port)
	add("health_path", app.HealthPath, spec.HealthPath)
	add("start_command", app.StartCommand, spec.StartCommand)
	return changes, nil
}
