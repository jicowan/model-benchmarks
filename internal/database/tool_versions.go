package database

import (
	"context"
	"fmt"
)

// GetToolVersions returns the singleton tool_versions row. The migration
// ensures the row always exists, so callers don't need to handle a missing
// row case.
func (r *Repository) GetToolVersions(ctx context.Context) (*ToolVersions, error) {
	var tv ToolVersions
	err := r.pool.QueryRow(ctx,
		`SELECT framework_version, inference_perf_version, updated_at
		   FROM tool_versions WHERE id = 1`).
		Scan(&tv.FrameworkVersion, &tv.InferencePerfVersion, &tv.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("load tool_versions: %w", err)
	}
	return &tv, nil
}

// PutToolVersions updates the singleton row in place.
func (r *Repository) PutToolVersions(ctx context.Context, tv *ToolVersions) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE tool_versions
		    SET framework_version = $1,
		        inference_perf_version = $2,
		        updated_at = now()
		  WHERE id = 1`,
		tv.FrameworkVersion, tv.InferencePerfVersion)
	if err != nil {
		return fmt.Errorf("update tool_versions: %w", err)
	}
	return nil
}
