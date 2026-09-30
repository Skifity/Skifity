package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const serverColumns = `id, team_id, name, host, ssh_port, ssh_user, ssh_key_enc, host_key, role,
	status, status_detail, node_name, external_ip, internal_ip, os_info, arch,
	cpu_cores, memory_mb, disk_gb, labels, adopted, created_at, updated_at, last_seen_at`

func scanServer(row interface{ Scan(...any) error }) (Server, error) {
	var s Server
	var created, updated string
	var lastSeen sql.NullString
	err := row.Scan(&s.ID, &s.TeamID, &s.Name, &s.Host, &s.SSHPort, &s.SSHUser, &s.SSHKeyEnc,
		&s.HostKey, &s.Role, &s.Status, &s.StatusDetail, &s.NodeName, &s.ExternalIP, &s.InternalIP,
		&s.OSInfo, &s.Arch, &s.CPUCores, &s.MemoryMB, &s.DiskGB, &s.Labels, &s.Adopted,
		&created, &updated, &lastSeen)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return s, ErrNotFound
		}
		return s, fmt.Errorf("scan server: %w", err)
	}
	s.CreatedAt, _ = ParseTime(created)
	s.UpdatedAt, _ = ParseTime(updated)
	s.LastSeenAt = scanTime(lastSeen)
	return s, nil
}

// CreateServer registers a machine before provisioning starts, so a failed
// provision still leaves a row the user can retry or clean up.
func (db *DB) CreateServer(ctx context.Context, s *Server) error {
	if s.ID == "" {
		s.ID = NewID("srv")
	}
	if s.SSHPort == 0 {
		s.SSHPort = 22
	}
	if s.Status == "" {
		s.Status = ServerPending
	}
	if s.Labels == "" {
		s.Labels = "{}"
	}
	now := Now()
	_, err := db.Exec(ctx, `INSERT INTO servers
		(id, team_id, name, host, ssh_port, ssh_user, ssh_key_enc, host_key, role, status, status_detail,
		 node_name, external_ip, internal_ip, os_info, arch, cpu_cores, memory_mb, disk_gb, labels,
		 adopted, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		s.ID, s.TeamID, s.Name, s.Host, s.SSHPort, s.SSHUser, s.SSHKeyEnc, s.HostKey,
		defaultStr(s.Role, "worker"), s.Status, s.StatusDetail, s.NodeName, s.ExternalIP, s.InternalIP,
		s.OSInfo, s.Arch, s.CPUCores, s.MemoryMB, s.DiskGB, s.Labels, s.Adopted, now, now)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return fmt.Errorf("%w: %s is already registered as a server", ErrConflict, s.Host)
		}
		return fmt.Errorf("create server: %w", err)
	}
	s.CreatedAt, _ = ParseTime(now)
	s.UpdatedAt = s.CreatedAt
	return nil
}

// GetServer looks a server up by id.
func (db *DB) GetServer(ctx context.Context, id string) (Server, error) {
	return scanServer(db.QueryRowContext(ctx, `SELECT `+serverColumns+` FROM servers WHERE id = ?`, id))
}

// ListServers returns a team's servers, oldest first.
func (db *DB) ListServers(ctx context.Context, teamID string) ([]Server, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+serverColumns+` FROM servers WHERE team_id = ? ORDER BY created_at`, teamID)
	if err != nil {
		return nil, fmt.Errorf("list servers: %w", err)
	}
	defer rows.Close()
	out := []Server{}
	for rows.Next() {
		s, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListAllServers returns every server across teams, for the node reconciler.
func (db *DB) ListAllServers(ctx context.Context) ([]Server, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+serverColumns+` FROM servers ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list all servers: %w", err)
	}
	defer rows.Close()
	out := []Server{}
	for rows.Next() {
		s, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// UpdateServer writes a server's mutable fields.
func (db *DB) UpdateServer(ctx context.Context, s *Server) error {
	now := Now()
	res, err := db.Exec(ctx, `UPDATE servers SET
		name=?, host=?, ssh_port=?, ssh_user=?, ssh_key_enc=?, host_key=?, role=?, status=?, status_detail=?,
		node_name=?, external_ip=?, internal_ip=?, os_info=?, arch=?, cpu_cores=?, memory_mb=?, disk_gb=?,
		labels=?, updated_at=? WHERE id=?`,
		s.Name, s.Host, s.SSHPort, s.SSHUser, s.SSHKeyEnc, s.HostKey, s.Role, s.Status, s.StatusDetail,
		s.NodeName, s.ExternalIP, s.InternalIP, s.OSInfo, s.Arch, s.CPUCores, s.MemoryMB, s.DiskGB,
		s.Labels, now, s.ID)
	if err != nil {
		return fmt.Errorf("update server: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.UpdatedAt, _ = ParseTime(now)
	return nil
}

// SetServerStatus updates just the status pair, which provisioning does often.
func (db *DB) SetServerStatus(ctx context.Context, id string, status ServerStatus, detail string) error {
	_, err := db.Exec(ctx, `UPDATE servers SET status = ?, status_detail = ?, updated_at = ? WHERE id = ?`,
		status, detail, Now(), id)
	if err != nil {
		return fmt.Errorf("set server status: %w", err)
	}
	return nil
}

// TouchServerSeen records that the node was observed healthy.
func (db *DB) TouchServerSeen(ctx context.Context, id string, at time.Time) error {
	_, err := db.Exec(ctx, `UPDATE servers SET last_seen_at = ? WHERE id = ?`, FormatTime(at), id)
	if err != nil {
		return fmt.Errorf("touch server: %w", err)
	}
	return nil
}

// DeleteServer removes a server record once the node has been drained and removed.
func (db *DB) DeleteServer(ctx context.Context, id string) error {
	res, err := db.Exec(ctx, `DELETE FROM servers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete server: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountControlPlanes reports how many servers are control-plane nodes, which
// decides whether "Promote to control plane" is offered and whether removing a
// node would break quorum.
func (db *DB) CountControlPlanes(ctx context.Context, teamID string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM servers WHERE team_id = ? AND role = 'control-plane'`, teamID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count control planes: %w", err)
	}
	return n, nil
}

// --- operations ---

// CreateOperation starts a resumable multi-step job with its steps pre-created,
// so the UI can show the whole plan before any of it runs.
func (db *DB) CreateOperation(ctx context.Context, op *Operation, stepKeys []string) error {
	if op.ID == "" {
		op.ID = NewID("op")
	}
	if op.Status == "" {
		op.Status = OpPending
	}
	now := Now()
	err := db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO operations
			(id, team_id, kind, target_type, target_id, status, created_by, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?)`,
			op.ID, op.TeamID, op.Kind, op.TargetType, op.TargetID, op.Status, op.CreatedBy, now, now); err != nil {
			return fmt.Errorf("insert operation: %w", err)
		}
		for i, key := range stepKeys {
			if _, err := tx.ExecContext(ctx, `INSERT INTO operation_steps
				(id, operation_id, seq, key, status) VALUES (?,?,?,?,?)`,
				NewID("step"), op.ID, i, key, StepPending); err != nil {
				return fmt.Errorf("insert operation step %s: %w", key, err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	op.CreatedAt, _ = ParseTime(now)
	op.UpdatedAt = op.CreatedAt
	return nil
}

// GetOperation returns an operation with its steps in order.
func (db *DB) GetOperation(ctx context.Context, id string) (Operation, error) {
	var op Operation
	var created, updated string
	var finished sql.NullString
	err := db.QueryRowContext(ctx, `SELECT id, team_id, kind, target_type, target_id, status,
		error_code, error_msg, created_by, created_at, updated_at, finished_at FROM operations WHERE id = ?`, id).
		Scan(&op.ID, &op.TeamID, &op.Kind, &op.TargetType, &op.TargetID, &op.Status,
			&op.ErrorCode, &op.ErrorMsg, &op.CreatedBy, &created, &updated, &finished)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return op, ErrNotFound
		}
		return op, fmt.Errorf("get operation: %w", err)
	}
	op.CreatedAt, _ = ParseTime(created)
	op.UpdatedAt, _ = ParseTime(updated)
	op.FinishedAt = scanTime(finished)

	steps, err := db.ListOperationSteps(ctx, id)
	if err != nil {
		return op, err
	}
	op.Steps = steps
	return op, nil
}

// LatestOperation returns the most recent operation for a target, which is what
// the UI reopens when a user returns to a server that is still being added.
func (db *DB) LatestOperation(ctx context.Context, targetType, targetID string) (Operation, error) {
	var id string
	err := db.QueryRowContext(ctx, `SELECT id FROM operations
		WHERE target_type = ? AND target_id = ? ORDER BY created_at DESC LIMIT 1`, targetType, targetID).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Operation{}, ErrNotFound
		}
		return Operation{}, fmt.Errorf("find latest operation: %w", err)
	}
	return db.GetOperation(ctx, id)
}

// ListOperationSteps returns an operation's steps in sequence order.
func (db *DB) ListOperationSteps(ctx context.Context, opID string) ([]OperationStep, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, operation_id, seq, key, status, message, message_key,
		message_args, notes, detail, started_at, finished_at
		FROM operation_steps WHERE operation_id = ? ORDER BY seq`, opID)
	if err != nil {
		return nil, fmt.Errorf("list operation steps: %w", err)
	}
	defer rows.Close()
	out := []OperationStep{}
	for rows.Next() {
		var s OperationStep
		var started, finished sql.NullString
		var args, notes string
		if err := rows.Scan(&s.ID, &s.OperationID, &s.Seq, &s.Key, &s.Status, &s.Message,
			&s.MessageKey, &args, &notes, &s.Detail, &started, &finished); err != nil {
			return nil, fmt.Errorf("scan operation step: %w", err)
		}
		if notes != "" {
			_ = json.Unmarshal([]byte(notes), &s.Notes)
		}
		if args != "" {
			// A row written before this column existed, or one whose values
			// were not recorded, simply has none. The English in Message is
			// what the panel falls back to either way.
			_ = json.Unmarshal([]byte(args), &s.MessageArgs)
		}
		s.StartedAt = scanTime(started)
		s.FinishedAt = scanTime(finished)
		out = append(out, s)
	}
	return out, rows.Err()
}

// SetOperationStatus moves an operation between states and records the reason on
// failure.
func (db *DB) SetOperationStatus(ctx context.Context, id string, status OperationStatus, errCode, errMsg string) error {
	var finished any
	if status == OpSucceeded || status == OpFailed || status == OpCancelled {
		finished = Now()
	}
	_, err := db.Exec(ctx, `UPDATE operations SET status = ?, error_code = ?, error_msg = ?, updated_at = ?,
		finished_at = COALESCE(?, finished_at) WHERE id = ?`, status, errCode, errMsg, Now(), finished, id)
	if err != nil {
		return fmt.Errorf("set operation status: %w", err)
	}
	return nil
}

// SetStepStatus updates one step. Re-running a succeeded step is how retry works,
// so this is written to be safe to call repeatedly.
func (db *DB) SetStepStatus(ctx context.Context, opID, key string, status StepStatus, note StepNote, detail string) error {
	var started, finished any
	switch status {
	case StepRunning:
		started = Now()
	case StepSucceeded, StepFailed, StepSkipped:
		finished = Now()
	}
	encoded, encodedNotes := "", ""
	if len(note.Args) > 0 {
		if blob, err := json.Marshal(note.Args); err == nil {
			encoded = string(blob)
		}
	}
	if len(note.Details) > 0 {
		if blob, err := json.Marshal(note.Details); err == nil {
			encodedNotes = string(blob)
		}
	}
	_, err := db.Exec(ctx, `UPDATE operation_steps SET status = ?, message = ?, message_key = ?,
		message_args = ?, notes = ?, detail = ?,
		started_at = COALESCE(?, started_at), finished_at = COALESCE(?, finished_at)
		WHERE operation_id = ? AND key = ?`,
		status, note.Message, note.Key, encoded, encodedNotes, detail, started, finished, opID, key)
	if err != nil {
		return fmt.Errorf("set step status: %w", err)
	}
	_, err = db.Exec(ctx, `UPDATE operations SET updated_at = ? WHERE id = ?`, Now(), opID)
	if err != nil {
		return fmt.Errorf("touch operation: %w", err)
	}
	return nil
}

// ResetStepsFrom marks a failed step and everything after it pending again, which
// is what "Retry" does.
func (db *DB) ResetStepsFrom(ctx context.Context, opID, key string) error {
	_, err := db.Exec(ctx, `UPDATE operation_steps SET status = 'pending', message = '',
		message_key = '', message_args = '', notes = '', detail = '',
		started_at = NULL, finished_at = NULL
		WHERE operation_id = ? AND seq >= (SELECT seq FROM operation_steps WHERE operation_id = ? AND key = ?)`,
		opID, opID, key)
	if err != nil {
		return fmt.Errorf("reset operation steps: %w", err)
	}
	return nil
}

// ListRunningOperations finds operations left mid-flight by a panel restart.
func (db *DB) ListRunningOperations(ctx context.Context) ([]Operation, error) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM operations WHERE status IN ('pending','running')`)
	if err != nil {
		return nil, fmt.Errorf("list running operations: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan operation id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Operation, 0, len(ids))
	for _, id := range ids {
		op, err := db.GetOperation(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, nil
}
