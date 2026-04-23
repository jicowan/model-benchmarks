package scenario

import "testing"

func intPtr(v int) *int     { return &v }
func boolPtr(v bool) *bool  { return &v }

func TestMerge_NilOverrideReturnsCopy(t *testing.T) {
	base := &Scenario{ID: "test", NumWorkers: 4, Streaming: true}
	out := base.Merge(nil)
	if out == base {
		t.Error("Merge(nil) must return a copy, not the original")
	}
	if out.NumWorkers != 4 || !out.Streaming {
		t.Errorf("copy drifted: %+v", out)
	}
}

func TestMerge_OverridesApplied(t *testing.T) {
	base := &Scenario{
		ID:         "chatbot",
		NumWorkers: 4,
		Streaming:  true,
		Input:      Distribution{Mean: 256, StdDev: 64, Min: 128, Max: 512},
		Output:     Distribution{Mean: 128, StdDev: 32, Min: 64, Max: 256},
	}
	ov := &Override{
		NumWorkers: intPtr(16),
		Streaming:  boolPtr(false),
		InputMean:  intPtr(1024),
		OutputMean: intPtr(512),
	}
	out := base.Merge(ov)
	if out.NumWorkers != 16 {
		t.Errorf("NumWorkers = %d, want 16", out.NumWorkers)
	}
	if out.Streaming {
		t.Error("Streaming should be false")
	}
	// Bounds derive from new means: std=mean/4, min=mean/2, max=mean*2.
	if out.Input.Mean != 1024 || out.Input.StdDev != 256 || out.Input.Min != 512 || out.Input.Max != 2048 {
		t.Errorf("Input distribution wrong: %+v", out.Input)
	}
	if out.Output.Mean != 512 || out.Output.StdDev != 128 || out.Output.Min != 256 || out.Output.Max != 1024 {
		t.Errorf("Output distribution wrong: %+v", out.Output)
	}
	// Base unchanged.
	if base.NumWorkers != 4 {
		t.Error("Merge mutated the base scenario")
	}
}

func TestMerge_PartialOverride(t *testing.T) {
	base := &Scenario{
		ID:         "batch",
		NumWorkers: 8,
		Streaming:  true,
		Input:      Distribution{Mean: 512, StdDev: 128, Min: 256, Max: 1024},
	}
	// Only num_workers overridden — Input should be preserved.
	out := base.Merge(&Override{NumWorkers: intPtr(32)})
	if out.NumWorkers != 32 {
		t.Errorf("NumWorkers = %d, want 32", out.NumWorkers)
	}
	if out.Input.Mean != 512 {
		t.Error("Input.Mean should be untouched when only NumWorkers overridden")
	}
	if !out.Streaming {
		t.Error("Streaming should inherit")
	}
}
