package database

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// RunFilter holds optional filters for listing benchmark runs.
type RunFilter struct {
	Status   string // "pending", "running", "completed", "failed", or ""
	ModelID  string // ILIKE filter on model hf_id
	Limit    int
	Offset   int
}

// RunListItem is a denormalized row for the jobs list.
type RunListItem struct {
	ID               string     `json:"id"`
	ModelHfID        string     `json:"model_hf_id"`
	InstanceTypeName string     `json:"instance_type_name"`
	Framework        string     `json:"framework"`
	RunType          string     `json:"run_type"`
	Status           string     `json:"status"`
	ErrorMessage     *string    `json:"error_message,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
}

// ListRuns returns benchmark runs matching the given filter, joined with
// models and instance_types for display names.
func (r *Repository) ListRuns(ctx context.Context, f RunFilter) ([]RunListItem, error) {
	var (
		conditions []string
		args       []any
		argIdx     int
	)

	if f.Status != "" {
		argIdx++
		conditions = append(conditions, fmt.Sprintf("br.status = $%d", argIdx))
		args = append(args, f.Status)
	}
	if f.ModelID != "" {
		argIdx++
		conditions = append(conditions, fmt.Sprintf("m.hf_id ILIKE $%d", argIdx))
		args = append(args, "%"+f.ModelID+"%")
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	// Pagination.
	limit := 50
	if f.Limit > 0 && f.Limit <= 200 {
		limit = f.Limit
	}
	argIdx++
	limitClause := fmt.Sprintf("LIMIT $%d", argIdx)
	args = append(args, limit)

	offsetClause := ""
	if f.Offset > 0 {
		argIdx++
		offsetClause = fmt.Sprintf("OFFSET $%d", argIdx)
		args = append(args, f.Offset)
	}

	query := fmt.Sprintf(`
		SELECT
			br.id, m.hf_id, it.name,
			br.framework, br.run_type, br.status, br.error_message,
			br.created_at, br.started_at, br.completed_at
		FROM benchmark_runs br
		JOIN models m ON br.model_id = m.id
		JOIN instance_types it ON br.instance_type_id = it.id
		%s
		ORDER BY br.created_at DESC
		%s %s
	`, where, limitClause, offsetClause)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query runs: %w", err)
	}
	defer rows.Close()

	var items []RunListItem
	for rows.Next() {
		var item RunListItem
		err := rows.Scan(
			&item.ID, &item.ModelHfID, &item.InstanceTypeName,
			&item.Framework, &item.RunType, &item.Status, &item.ErrorMessage,
			&item.CreatedAt, &item.StartedAt, &item.CompletedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan run row: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// RunExportDetails contains all the information needed to export a run's
// Kubernetes configuration, including joined model and instance type data.
type RunExportDetails struct {
	RunID                string
	ModelHfID            string
	ModelS3URI           *string
	InstanceTypeName     string
	Framework            string
	FrameworkVersion     string
	TensorParallelDegree int
	Quantization         *string
	MaxModelLen          int
	// PRD-46: vLLM scheduler knobs persisted on the run row so the
	// exported manifest reproduces the flags actually passed at runtime.
	MaxNumBatchedTokens *int
	// KVCacheDtype emits --kv-cache-dtype when non-nil; typically "fp8"
	// on FP8-capable accelerators, empty otherwise.
	KVCacheDtype *string
	// SGLang scheduler knobs (null on vLLM/neuron + historical runs). Captured
	// so an SGLang single-node export reproduces --chunked-prefill-size /
	// --mem-fraction-static that were actually applied.
	ChunkedPrefillSize *int
	MemFractionStatic  *float64
	// PRD-50: Run:ai streamer knobs. Null for historical runs (treated
	// as auto / 16 / auto-sized). UseRunaiStreamer is the resolved
	// decision — equivalent to `StreamerMode != "off" && ModelS3URI != ""`,
	// materialized here so the export handler doesn't re-derive it.
	StreamerMode           *string
	StreamerConcurrency    *int
	StreamerMemoryLimitGiB *int
	UseRunaiStreamer       bool
	// Concurrency drives --max-num-seqs at export time. Persisted on
	// the run row under its own column; we pull it here so
	// generateManifest has everything it needs without a second query.
	Concurrency          int
	AcceleratorType      string
	AcceleratorName      string
	AcceleratorCount     int
	AcceleratorMemoryGiB int
	VCPUs                int
	MemoryGiB            int
	// PRD-59: distributed topology so the exported manifest reproduces a
	// multi-node / disaggregated run correctly (not a wrong single-node
	// Deployment). Null on single-instance runs → the single-node manifest path.
	DeploymentMode         *string
	NodeCount              *int
	PipelineParallelDegree *int
	NetworkMode            *string
	PrefillReplicas        *int
	PrefillTP              *int
	DecodeReplicas         *int
	DecodeTP               *int
	// PRD-63: co-located "both" pool, so the exported manifest reproduces a
	// run that used a both pool (or a both-only run). Null on PD-only runs.
	BothReplicas *int
	BothTP       *int
	// PRD-64: per-role scheduler override, so the export reproduces the
	// per-role --max-num-batched-tokens actually applied. Null ⇒ role used the
	// shared MaxNumBatchedTokens.
	PrefillMaxNumBatchedTokens *int
	DecodeMaxNumBatchedTokens  *int
	BothMaxNumBatchedTokens    *int
	// PRD-61: the run's EPP routing config, so the exported EPP ConfigMap matches
	// what was applied (user overrides included). Null ⇒ the shipped default was
	// used; the export applies the same default the orchestrator would.
	PDNonCachedTokens      *int
	PDPrefixCacheWeight    *int
	PDQueueScorerWeight    *int
	PDMaxPrefixBlocks      *int
	PDLRUCapacityPerServer *int
	PDDeciderStrategy      *string
	// PRD-66 Part 2: configured multi-node image tags, injected by the export
	// handler from tool_versions (NOT persisted per-run — the tag is a
	// platform setting, and llm-d/pd-vLLM versions aren't the run's
	// framework_version). Empty ⇒ the generator falls back to the shipped
	// default, so the export stays byte-identical to today for callers that
	// don't set them.
	LLMDVersion   string
	PDVLLMVersion string
}

// GetRunExportDetails returns the information needed to export a run's
// Kubernetes configuration. Returns nil if the run is not found.
func (r *Repository) GetRunExportDetails(ctx context.Context, runID string) (*RunExportDetails, error) {
	var d RunExportDetails
	var maxModelLen *int
	err := r.pool.QueryRow(ctx, `
		SELECT
			br.id, m.hf_id, br.model_s3_uri, it.name,
			br.framework, br.framework_version,
			br.tensor_parallel_degree, br.quantization, br.max_model_len,
			br.max_num_batched_tokens, br.kv_cache_dtype, br.concurrency,
			br.chunked_prefill_size, br.mem_fraction_static,
			br.streamer_mode, br.streamer_concurrency, br.streamer_memory_limit_gib,
			it.accelerator_type, it.accelerator_name, it.accelerator_count, it.accelerator_memory_gib,
			it.vcpus, it.memory_gib,
			br.deployment_mode, br.node_count, br.pipeline_parallel_degree, br.network_mode,
			br.prefill_replicas, br.prefill_tp, br.decode_replicas, br.decode_tp,
			br.both_replicas, br.both_tp,
			br.prefill_max_num_batched_tokens, br.decode_max_num_batched_tokens,
			br.both_max_num_batched_tokens,
			br.pd_noncached_tokens, br.pd_prefix_cache_weight, br.pd_queue_scorer_weight,
			br.pd_max_prefix_blocks, br.pd_lru_capacity_per_server, br.pd_decider_strategy
		FROM benchmark_runs br
		JOIN models m ON br.model_id = m.id
		JOIN instance_types it ON br.instance_type_id = it.id
		WHERE br.id = $1
	`, runID).Scan(
		&d.RunID, &d.ModelHfID, &d.ModelS3URI, &d.InstanceTypeName,
		&d.Framework, &d.FrameworkVersion,
		&d.TensorParallelDegree, &d.Quantization, &maxModelLen,
		&d.MaxNumBatchedTokens, &d.KVCacheDtype, &d.Concurrency,
		&d.ChunkedPrefillSize, &d.MemFractionStatic,
		&d.StreamerMode, &d.StreamerConcurrency, &d.StreamerMemoryLimitGiB,
		&d.AcceleratorType, &d.AcceleratorName, &d.AcceleratorCount, &d.AcceleratorMemoryGiB,
		&d.VCPUs, &d.MemoryGiB,
		&d.DeploymentMode, &d.NodeCount, &d.PipelineParallelDegree, &d.NetworkMode,
		&d.PrefillReplicas, &d.PrefillTP, &d.DecodeReplicas, &d.DecodeTP,
		&d.BothReplicas, &d.BothTP,
		&d.PrefillMaxNumBatchedTokens, &d.DecodeMaxNumBatchedTokens, &d.BothMaxNumBatchedTokens,
		&d.PDNonCachedTokens, &d.PDPrefixCacheWeight, &d.PDQueueScorerWeight,
		&d.PDMaxPrefixBlocks, &d.PDLRUCapacityPerServer, &d.PDDeciderStrategy,
	)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("query run export details: %w", err)
	}
	if maxModelLen != nil {
		d.MaxModelLen = *maxModelLen
	}
	// Resolve the streamer-on decision once so the export handler and
	// UI don't each re-derive it. "off" disables even for S3 models;
	// otherwise it's on iff the run loaded from S3.
	mode := ""
	if d.StreamerMode != nil {
		mode = *d.StreamerMode
	}
	d.UseRunaiStreamer = mode != "off" && d.ModelS3URI != nil && *d.ModelS3URI != ""
	return &d, nil
}

// DeleteRun removes a benchmark run and its associated metrics.
func (r *Repository) DeleteRun(ctx context.Context, runID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM benchmark_metrics WHERE run_id = $1`, runID); err != nil {
		return fmt.Errorf("delete metrics: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM benchmark_runs WHERE id = $1`, runID); err != nil {
		return fmt.Errorf("delete run: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
