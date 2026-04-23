package orchestrator

import (
	"context"
	"log"
	"os"
)

// computeRunCost looks up the hourly rate for the run's instance type and the
// cluster's region, then returns (total_cost_usd, loadgen_cost_usd) for the
// given run. Returns (nil, nil) and logs a warning if the run is missing
// completion timestamps or the pricing table has no matching row — in both
// cases the caller will UPDATE the columns to NULL and aggregates COALESCE
// them to $0 (PRD-35).
//
// total_cost_usd   = hourly × (completed_at − started_at) / 3600
//                    — full EC2 node lifetime (image pull + load + bench + teardown)
// loadgen_cost_usd = hourly × metrics.total_duration_seconds / 3600
//                    — only the inference-perf window, stored for future display
func (o *Orchestrator) computeRunCost(ctx context.Context, runID string) (*float64, *float64) {
	run, err := o.repo.GetBenchmarkRun(ctx, runID)
	if err != nil || run == nil {
		return nil, nil
	}
	if run.StartedAt == nil || run.CompletedAt == nil {
		// Nothing to bill — runs that never reached "running" don't have a
		// node lifetime. Skip silently; this path is reachable for runs
		// that failed during pre-flight.
		return nil, nil
	}

	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-east-2"
	}

	p, err := o.repo.GetPricingForInstanceType(ctx, run.InstanceTypeID, region)
	if err != nil || p == nil {
		log.Printf("[cost] no pricing row for instance_type_id=%s region=%s run=%s",
			run.InstanceTypeID, region, runID)
		return nil, nil
	}
	hourly := p.OnDemandHourlyUSD

	nodeSec := run.CompletedAt.Sub(*run.StartedAt).Seconds()
	if nodeSec < 0 {
		nodeSec = 0
	}
	totalUSD := hourly * nodeSec / 3600
	totalPtr := &totalUSD

	// loadgen cost — only computable when metrics exist. Failed runs may have
	// no metrics row; treat as "no loadgen cost" rather than blocking the
	// total-cost write.
	var loadgenPtr *float64
	if metrics, mErr := o.repo.GetMetricsByRunID(ctx, runID); mErr == nil && metrics != nil && metrics.TotalDurationSeconds != nil {
		loadgenUSD := hourly * *metrics.TotalDurationSeconds / 3600
		loadgenPtr = &loadgenUSD
	}

	return totalPtr, loadgenPtr
}

// persistSuiteCost computes and stores the suite's total_cost_usd (PRD-35).
// Call after UpdateSuiteRunStatus(..., "completed" | "failed", ...) so
// completed_at is populated. All scenarios in a suite share a single model
// deployment, so the EC2 node is billable from started_at to completed_at
// — one window, same formula as a single run.
func (o *Orchestrator) persistSuiteCost(ctx context.Context, suiteRunID string) {
	suite, err := o.repo.GetTestSuiteRun(ctx, suiteRunID)
	if err != nil || suite == nil {
		return
	}
	if suite.StartedAt == nil || suite.CompletedAt == nil {
		return
	}

	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-east-2"
	}

	p, err := o.repo.GetPricingForInstanceType(ctx, suite.InstanceTypeID, region)
	if err != nil || p == nil {
		log.Printf("[cost] no pricing row for instance_type_id=%s region=%s suite=%s",
			suite.InstanceTypeID, region, suiteRunID)
		return
	}

	nodeSec := suite.CompletedAt.Sub(*suite.StartedAt).Seconds()
	if nodeSec < 0 {
		nodeSec = 0
	}
	totalUSD := p.OnDemandHourlyUSD * nodeSec / 3600
	if err := o.repo.UpdateSuiteRunCost(ctx, suiteRunID, &totalUSD); err != nil {
		log.Printf("[cost] update suite run cost %s: %v", suiteRunID, err)
	}
}

