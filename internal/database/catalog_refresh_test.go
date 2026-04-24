package database

import (
	"context"
	"testing"
)

// Compile-time assertions that both Repository and MockRepo satisfy the
// PRD-37 addition to the Repo interface. The assertion in iface.go
// already covers *Repository; we re-assert here alongside MockRepo so a
// rename or signature change surfaces in this file's test output.
var (
	_ Repo = (*Repository)(nil)
	_ Repo = (*MockRepo)(nil)
)

func TestMockRefreshCatalogRows_IsNoOp(t *testing.T) {
	repo := NewMockRepo()

	// Refresh on an empty mock succeeds.
	if err := repo.RefreshCatalogRows(context.Background()); err != nil {
		t.Fatalf("empty mock refresh: %v", err)
	}

	// Repeated refresh is safe.
	if err := repo.RefreshCatalogRows(context.Background()); err != nil {
		t.Fatalf("second refresh: %v", err)
	}

	// Refresh should not change what ListCatalog returns.
	before, _, err := repo.ListCatalog(context.Background(), CatalogFilter{})
	if err != nil {
		t.Fatalf("pre-refresh ListCatalog: %v", err)
	}
	if err := repo.RefreshCatalogRows(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	after, _, err := repo.ListCatalog(context.Background(), CatalogFilter{})
	if err != nil {
		t.Fatalf("post-refresh ListCatalog: %v", err)
	}
	if len(before) != len(after) {
		t.Errorf("refresh changed entry count: before=%d after=%d", len(before), len(after))
	}
}

func TestMockRefreshCatalogRows_RespectsCanceledContext(t *testing.T) {
	// A cancelled context should not cause the no-op to return an error.
	// Real DB behavior is tested against a live Postgres; this just
	// locks the mock's contract.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repo := NewMockRepo()
	if err := repo.RefreshCatalogRows(ctx); err != nil {
		t.Fatalf("mock refresh should ignore context cancellation: %v", err)
	}
}

// TestAllowedSortColumns_NoPrefixedIdentifiers ensures the PRD-37
// rewrite fully dropped the `br.` / `m.` / `it.` / `bm.` qualifiers —
// those would produce `relation does not exist` errors now that
// ListCatalog reads from the unqualified `catalog_rows` view.
func TestAllowedSortColumns_NoPrefixedIdentifiers(t *testing.T) {
	for key, col := range allowedSortColumns {
		for _, prefix := range []string{"br.", "m.", "it.", "bm."} {
			if len(col) >= len(prefix) && col[:len(prefix)] == prefix {
				t.Errorf("sort key %q maps to %q which still carries the %q prefix", key, col, prefix)
			}
		}
	}
}

// TestAllowedSortColumns_StableKeys guards the public API — the
// frontend's sort query params assume these keys.
func TestAllowedSortColumns_StableKeys(t *testing.T) {
	expected := []string{
		"model", "instance",
		"ttft_p50", "ttft_p95", "ttft_p99",
		"e2e_latency_p50", "e2e_latency_p95", "e2e_latency_p99",
		"itl_p50", "itl_p95", "itl_p99",
		"throughput_per_request", "throughput_aggregate", "requests_per_second",
		"accelerator_utilization", "accelerator_utilization_avg",
		"accelerator_memory_peak", "accelerator_memory_avg",
		"sm_active_avg", "tensor_active_avg", "dram_active_avg",
		"completed_at",
	}
	for _, k := range expected {
		if _, ok := allowedSortColumns[k]; !ok {
			t.Errorf("missing sort key %q", k)
		}
	}
	if len(allowedSortColumns) != len(expected) {
		t.Errorf("sort key count = %d, want %d", len(allowedSortColumns), len(expected))
	}
}
