package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// AppGPU is what each instance of an app's workloads is given. The zero value,
// Count 0, is an app with no GPU, which is every app that has no row.
type AppGPU struct {
	AppID string `json:"-"`
	// Count cards of Vendor's — nvidia, amd or intel — for each instance.
	Count  int    `json:"count"`
	Vendor string `json:"vendor"`
	// Product is a model to prefer, such as NVIDIA-A10; empty is any.
	Product string `json:"product"`
	// Workloads are what get them: "web" for the app itself, or a process's
	// name. Empty is the app alone.
	Workloads []string `json:"workloads"`
}

// GetAppGPU reads an app's GPUs; an app with none has Count 0.
func (db *DB) GetAppGPU(ctx context.Context, appID string) (AppGPU, error) {
	gpu := AppGPU{AppID: appID, Workloads: []string{}}
	var workloads string
	err := db.QueryRowContext(ctx,
		`SELECT count, vendor, product, workloads FROM app_gpus WHERE app_id = ?`, appID).
		Scan(&gpu.Count, &gpu.Vendor, &gpu.Product, &workloads)
	if errors.Is(err, sql.ErrNoRows) {
		return gpu, nil
	}
	if err != nil {
		return gpu, fmt.Errorf("read the GPUs of %s: %w", appID, err)
	}
	gpu.Workloads = splitNames(workloads)
	return gpu, nil
}

// SetAppGPU stores an app's GPUs, replacing what it had. Count 0 removes
// them, and the row with them.
func (db *DB) SetAppGPU(ctx context.Context, gpu AppGPU) error {
	if gpu.Count <= 0 {
		if _, err := db.Exec(ctx, `DELETE FROM app_gpus WHERE app_id = ?`, gpu.AppID); err != nil {
			return fmt.Errorf("remove the GPUs of %s: %w", gpu.AppID, err)
		}
		return nil
	}
	_, err := db.Exec(ctx, `
		INSERT INTO app_gpus (app_id, count, vendor, product, workloads, updated_at)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(app_id) DO UPDATE SET
			count = excluded.count,
			vendor = excluded.vendor,
			product = excluded.product,
			workloads = excluded.workloads,
			updated_at = excluded.updated_at`,
		gpu.AppID, gpu.Count, gpu.Vendor, gpu.Product, strings.Join(gpu.Workloads, ","), Now())
	if err != nil {
		return fmt.Errorf("save the GPUs of %s: %w", gpu.AppID, err)
	}
	return nil
}

// splitNames reads a comma-separated list of names back.
func splitNames(text string) []string {
	out := []string{}
	for _, name := range strings.Split(text, ",") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
	}
	return out
}
