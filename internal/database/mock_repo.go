package database

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// MockRepo is an in-memory implementation of Repo for testing.
type MockRepo struct {
	mu              sync.Mutex
	models          map[string]*Model            // keyed by "hfID|revision"
	instTypes       map[string]*InstanceType     // keyed by name
	runs            map[string]*BenchmarkRun     // keyed by run ID
	metrics         map[string]*BenchmarkMetrics // keyed by run ID
	pricing         map[string]*Pricing          // keyed by "instanceTypeID|region|date"
	oomEvents       []OOMEvent                   // OOM events
	suiteRuns       map[string]*TestSuiteRun     // keyed by suite run ID
	scenarioResults map[string]*ScenarioResult   // keyed by scenario result ID
	modelCache      map[string]*ModelCache       // keyed by cache ID
	catalogMatrix   *CatalogMatrix               // PRD-30 seeding matrix
	catalogSeeds    map[string]*CatalogSeedStatus
	scenarioOver    map[string]*ScenarioOverride // PRD-32
	auditLog        []ConfigAuditEntry           // PRD-32
	toolVersions    *ToolVersions                // PRD-34
	nextID          int
}

// NewMockRepo creates a new MockRepo.
func NewMockRepo() *MockRepo {
	return &MockRepo{
		models:          make(map[string]*Model),
		instTypes:       make(map[string]*InstanceType),
		runs:            make(map[string]*BenchmarkRun),
		metrics:         make(map[string]*BenchmarkMetrics),
		pricing:         make(map[string]*Pricing),
		suiteRuns:       make(map[string]*TestSuiteRun),
		scenarioResults: make(map[string]*ScenarioResult),
		modelCache:      make(map[string]*ModelCache),
	}
}

// SeedModel adds a model to the mock store.
func (m *MockRepo) SeedModel(model *Model) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := model.HfID + "|" + model.HfRevision
	m.models[key] = model
}

// SeedInstanceType adds an instance type to the mock store.
func (m *MockRepo) SeedInstanceType(it *InstanceType) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.instTypes[it.Name] = it
}

// GetRunStatus returns the current status of a run (for test assertions).
func (m *MockRepo) GetRunStatus(runID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.runs[runID]; ok {
		return r.Status
	}
	return ""
}

func (m *MockRepo) Ping(_ context.Context) error {
	return nil
}

func (m *MockRepo) GetModelByHfID(_ context.Context, hfID, hfRevision string) (*Model, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := hfID + "|" + hfRevision
	return m.models[key], nil
}

func (m *MockRepo) GetModelByID(_ context.Context, id string) (*Model, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, model := range m.models {
		if model.ID == id {
			return model, nil
		}
	}
	return nil, nil
}

func (m *MockRepo) EnsureModel(_ context.Context, hfID, hfRevision string) (*Model, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := hfID + "|" + hfRevision
	if model, ok := m.models[key]; ok {
		return model, nil
	}
	m.nextID++
	model := &Model{
		ID:         fmt.Sprintf("model-%08d", m.nextID),
		HfID:       hfID,
		HfRevision: hfRevision,
		CreatedAt:  time.Now(),
	}
	m.models[key] = model
	return model, nil
}

func (m *MockRepo) GetInstanceTypeByName(_ context.Context, name string) (*InstanceType, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.instTypes[name], nil
}

func (m *MockRepo) GetInstanceTypeByID(_ context.Context, id string) (*InstanceType, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, it := range m.instTypes {
		if it.ID == id {
			return it, nil
		}
	}
	return nil, nil
}

func (m *MockRepo) CreateBenchmarkRun(_ context.Context, run *BenchmarkRun) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	id := fmt.Sprintf("run-%08d", m.nextID)
	run.ID = id
	run.CreatedAt = time.Now()
	m.runs[id] = run
	return id, nil
}

func (m *MockRepo) UpdateRunStatus(_ context.Context, runID, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[runID]
	if !ok {
		return fmt.Errorf("run %s not found", runID)
	}
	run.Status = status
	now := time.Now()
	switch status {
	case "running":
		run.StartedAt = &now
	case "completed", "failed":
		run.CompletedAt = &now
	}
	return nil
}

func (m *MockRepo) UpdateRunFailed(_ context.Context, runID, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[runID]
	if !ok {
		return fmt.Errorf("run %s not found", runID)
	}
	run.Status = "failed"
	run.ErrorMessage = &reason
	now := time.Now()
	run.CompletedAt = &now
	return nil
}

func (m *MockRepo) UpdateLoadgenConfig(_ context.Context, runID, config string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[runID]
	if !ok {
		return fmt.Errorf("run %s not found", runID)
	}
	run.LoadgenConfig = &config
	return nil
}

func (m *MockRepo) SetLoadgenStartedAt(_ context.Context, runID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[runID]
	if !ok {
		return fmt.Errorf("run %s not found", runID)
	}
	now := time.Now()
	run.LoadgenStartedAt = &now
	return nil
}

func (m *MockRepo) GetLoadgenStartedAt(_ context.Context, runID string) (*time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[runID]
	if !ok {
		return nil, fmt.Errorf("run %s not found", runID)
	}
	return run.LoadgenStartedAt, nil
}

func (m *MockRepo) PersistMetrics(_ context.Context, runID string, bm *BenchmarkMetrics) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[runID]
	if !ok {
		return fmt.Errorf("run %s not found", runID)
	}
	bm.RunID = runID
	bm.ID = fmt.Sprintf("met-%08d", m.nextID+1)
	bm.CreatedAt = time.Now()
	m.metrics[runID] = bm
	run.Status = "completed"
	now := time.Now()
	run.CompletedAt = &now
	return nil
}

func (m *MockRepo) GetBenchmarkRun(_ context.Context, runID string) (*BenchmarkRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runs[runID], nil
}

func (m *MockRepo) GetRunsByStatus(_ context.Context, status string) ([]BenchmarkRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var runs []BenchmarkRun
	for _, run := range m.runs {
		if run.Status == status {
			runs = append(runs, *run)
		}
	}
	return runs, nil
}

func (m *MockRepo) GetMetricsByRunID(_ context.Context, runID string) (*BenchmarkMetrics, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.metrics[runID], nil
}

// ListRuns returns benchmark runs matching the given filter.
func (m *MockRepo) ListRuns(_ context.Context, f RunFilter) ([]RunListItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var items []RunListItem
	for _, run := range m.runs {
		if f.Status != "" && run.Status != f.Status {
			continue
		}

		// Resolve model for hf_id.
		var modelHfID string
		for _, mdl := range m.models {
			if mdl.ID == run.ModelID {
				modelHfID = mdl.HfID
				break
			}
		}
		if f.ModelID != "" && !strings.Contains(
			strings.ToLower(modelHfID),
			strings.ToLower(f.ModelID),
		) {
			continue
		}

		// Resolve instance type name.
		var instName string
		for _, it := range m.instTypes {
			if it.ID == run.InstanceTypeID {
				instName = it.Name
				break
			}
		}

		items = append(items, RunListItem{
			ID:               run.ID,
			ModelHfID:        modelHfID,
			InstanceTypeName: instName,
			Framework:        run.Framework,
			RunType:          run.RunType,
			Status:           run.Status,
			ErrorMessage:     run.ErrorMessage,
			CreatedAt:        run.CreatedAt,
			StartedAt:        run.StartedAt,
			CompletedAt:      run.CompletedAt,
		})
	}

	// Apply limit.
	limit := 50
	if f.Limit > 0 && f.Limit <= 200 {
		limit = f.Limit
	}
	if f.Offset > 0 && f.Offset < len(items) {
		items = items[f.Offset:]
	} else if f.Offset >= len(items) {
		return nil, nil
	}
	if len(items) > limit {
		items = items[:limit]
	}

	return items, nil
}

// DeleteRun removes a benchmark run and its metrics from the mock store.
func (m *MockRepo) DeleteRun(_ context.Context, runID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.metrics, runID)
	delete(m.runs, runID)
	return nil
}

// GetRunExportDetails returns the information needed to export a run's configuration.
func (m *MockRepo) GetRunExportDetails(_ context.Context, runID string) (*RunExportDetails, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	run, ok := m.runs[runID]
	if !ok {
		return nil, nil
	}

	// Resolve model.
	var model *Model
	for _, mdl := range m.models {
		if mdl.ID == run.ModelID {
			model = mdl
			break
		}
	}

	// Resolve instance type.
	var inst *InstanceType
	for _, it := range m.instTypes {
		if it.ID == run.InstanceTypeID {
			inst = it
			break
		}
	}

	if model == nil || inst == nil {
		return nil, nil
	}

	return &RunExportDetails{
		RunID:                runID,
		ModelHfID:            model.HfID,
		InstanceTypeName:     inst.Name,
		Framework:            run.Framework,
		FrameworkVersion:     run.FrameworkVersion,
		TensorParallelDegree: run.TensorParallelDegree,
		Quantization:         run.Quantization,
		MaxModelLen:          run.MaxModelLen,
		AcceleratorType:      inst.AcceleratorType,
		AcceleratorCount:     inst.AcceleratorCount,
		AcceleratorMemoryGiB: inst.AcceleratorMemoryGiB,
		VCPUs:                inst.VCPUs,
		MemoryGiB:            inst.MemoryGiB,
	}, nil
}

func (m *MockRepo) UpsertPricing(_ context.Context, p *Pricing) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := p.InstanceTypeID + "|" + p.Region + "|" + p.EffectiveDate
	m.pricing[key] = p
	return nil
}

func (m *MockRepo) ListPricing(_ context.Context, region string) ([]PricingRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var rows []PricingRow
	for _, p := range m.pricing {
		if p.Region != region {
			continue
		}
		name := p.InstanceTypeID
		for _, it := range m.instTypes {
			if it.ID == p.InstanceTypeID {
				name = it.Name
				break
			}
		}
		rows = append(rows, PricingRow{
			InstanceTypeName:     name,
			OnDemandHourlyUSD:    p.OnDemandHourlyUSD,
			Reserved1YrHourlyUSD: p.Reserved1YrHourlyUSD,
			Reserved3YrHourlyUSD: p.Reserved3YrHourlyUSD,
			EffectiveDate:        p.EffectiveDate,
		})
	}
	return rows, nil
}

func (m *MockRepo) ListInstanceTypes(_ context.Context) ([]InstanceType, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []InstanceType
	for _, it := range m.instTypes {
		result = append(result, *it)
	}
	return result, nil
}

// CreateOOMEvent inserts a new OOM event record.
func (m *MockRepo) CreateOOMEvent(_ context.Context, event *OOMEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	event.ID = fmt.Sprintf("oom-%08d", m.nextID)
	event.CreatedAt = time.Now()
	m.oomEvents = append(m.oomEvents, *event)
	return nil
}

// GetOOMHistory returns OOM events for a model+instance combination.
func (m *MockRepo) GetOOMHistory(_ context.Context, modelHfID, instanceType string, limit int) (*OOMHistory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if limit <= 0 {
		limit = 10
	}

	history := &OOMHistory{
		ModelHfID:    modelHfID,
		InstanceType: instanceType,
	}

	for _, ev := range m.oomEvents {
		if ev.ModelHfID == modelHfID && ev.InstanceType == instanceType {
			history.Events = append(history.Events, ev)
			history.TotalCount++
		}
	}

	if len(history.Events) > limit {
		history.Events = history.Events[:limit]
	}

	return history, nil
}

// ListCatalog returns catalog entries matching the given filter.
// This is a simplified in-memory implementation for testing.
func (m *MockRepo) ListCatalog(_ context.Context, f CatalogFilter) ([]CatalogEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var entries []CatalogEntry
	for runID, run := range m.runs {
		if run.Status != "completed" || run.Superseded {
			continue
		}
		met := m.metrics[runID]
		if met == nil {
			continue
		}

		// Resolve model.
		var model *Model
		for _, mdl := range m.models {
			if mdl.ID == run.ModelID {
				model = mdl
				break
			}
		}
		if model == nil {
			continue
		}

		// Resolve instance type.
		var inst *InstanceType
		for _, it := range m.instTypes {
			if it.ID == run.InstanceTypeID {
				inst = it
				break
			}
		}
		if inst == nil {
			continue
		}

		// Apply filters.
		if f.ModelHfID != "" && !strings.Contains(
			strings.ToLower(model.HfID),
			strings.ToLower(f.ModelHfID),
		) {
			continue
		}
		if f.ModelFamily != "" && (model.ModelFamily == nil || *model.ModelFamily != f.ModelFamily) {
			continue
		}
		if f.InstanceFamily != "" && inst.Family != f.InstanceFamily {
			continue
		}
		if f.AcceleratorType != "" && inst.AcceleratorType != f.AcceleratorType {
			continue
		}

		entries = append(entries, CatalogEntry{
			RunID:                     runID,
			ModelHfID:                 model.HfID,
			ModelFamily:               model.ModelFamily,
			ParameterCount:            model.ParameterCount,
			InstanceTypeName:          inst.Name,
			InstanceFamily:            inst.Family,
			AcceleratorType:           inst.AcceleratorType,
			AcceleratorName:           inst.AcceleratorName,
			AcceleratorCount:          inst.AcceleratorCount,
			AcceleratorMemoryGiB:      inst.AcceleratorMemoryGiB,
			Framework:                 run.Framework,
			FrameworkVersion:          run.FrameworkVersion,
			TensorParallelDegree:      run.TensorParallelDegree,
			Quantization:              run.Quantization,
			Concurrency:               run.Concurrency,
			InputSequenceLength:       run.InputSequenceLength,
			OutputSequenceLength:      run.OutputSequenceLength,
			CompletedAt:               run.CompletedAt,
			TTFTP50Ms:                 met.TTFTP50Ms,
			TTFTP99Ms:                 met.TTFTP99Ms,
			E2ELatencyP50Ms:           met.E2ELatencyP50Ms,
			E2ELatencyP99Ms:           met.E2ELatencyP99Ms,
			ITLP50Ms:                  met.ITLP50Ms,
			ITLP99Ms:                  met.ITLP99Ms,
			ThroughputPerRequestTPS:   met.ThroughputPerRequestTPS,
			ThroughputAggregateTPS:    met.ThroughputAggregateTPS,
			RequestsPerSecond:         met.RequestsPerSecond,
			AcceleratorUtilizationPct: met.AcceleratorUtilizationPct,
			AcceleratorMemoryPeakGiB:  met.AcceleratorMemoryPeakGiB,
		})
	}

	// Apply limit.
	limit := 100
	if f.Limit > 0 && f.Limit <= 500 {
		limit = f.Limit
	}
	if f.Offset > 0 && f.Offset < len(entries) {
		entries = entries[f.Offset:]
	} else if f.Offset >= len(entries) {
		return nil, nil
	}
	if len(entries) > limit {
		entries = entries[:limit]
	}

	return entries, nil
}

// Test Suite methods

func (m *MockRepo) CreateTestSuiteRun(_ context.Context, run *TestSuiteRun) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	id := fmt.Sprintf("suite-run-%08d", m.nextID)
	run.ID = id
	run.CreatedAt = time.Now()
	m.suiteRuns[id] = run
	return id, nil
}

func (m *MockRepo) GetTestSuiteRun(_ context.Context, id string) (*TestSuiteRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.suiteRuns[id]
	if !ok {
		return nil, nil
	}
	return run, nil
}

func (m *MockRepo) UpdateSuiteRunStatus(_ context.Context, id, status string, currentScenario *string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.suiteRuns[id]
	if !ok {
		return fmt.Errorf("suite run %s not found", id)
	}
	run.Status = status
	run.CurrentScenario = currentScenario
	now := time.Now()
	switch status {
	case "running":
		run.StartedAt = &now
	case "completed", "failed":
		run.CompletedAt = &now
	}
	return nil
}

func (m *MockRepo) CreateScenarioResult(_ context.Context, result *ScenarioResult) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	id := fmt.Sprintf("scenario-result-%08d", m.nextID)
	result.ID = id
	result.CreatedAt = time.Now()
	m.scenarioResults[id] = result
	return id, nil
}

func (m *MockRepo) UpdateScenarioResult(_ context.Context, result *ScenarioResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.scenarioResults[result.ID]
	if !ok {
		return fmt.Errorf("scenario result %s not found", result.ID)
	}
	now := time.Now()
	existing.Status = result.Status
	switch result.Status {
	case "running":
		existing.StartedAt = &now
	case "completed":
		existing.CompletedAt = &now
		existing.TTFTP50Ms = result.TTFTP50Ms
		existing.TTFTP90Ms = result.TTFTP90Ms
		existing.TTFTP99Ms = result.TTFTP99Ms
		existing.E2ELatencyP50Ms = result.E2ELatencyP50Ms
		existing.E2ELatencyP90Ms = result.E2ELatencyP90Ms
		existing.E2ELatencyP99Ms = result.E2ELatencyP99Ms
		existing.ITLP50Ms = result.ITLP50Ms
		existing.ITLP90Ms = result.ITLP90Ms
		existing.ITLP99Ms = result.ITLP99Ms
		existing.ThroughputTPS = result.ThroughputTPS
		existing.RequestsPerSecond = result.RequestsPerSecond
		existing.SuccessfulRequests = result.SuccessfulRequests
		existing.FailedRequests = result.FailedRequests
		existing.LoadgenConfig = result.LoadgenConfig
	case "failed":
		existing.CompletedAt = &now
		existing.ErrorMessage = result.ErrorMessage
	}
	return nil
}

func (m *MockRepo) GetScenarioResults(_ context.Context, suiteRunID string) ([]ScenarioResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var results []ScenarioResult
	for _, r := range m.scenarioResults {
		if r.SuiteRunID == suiteRunID {
			results = append(results, *r)
		}
	}
	return results, nil
}

func (m *MockRepo) ListTestSuiteRuns(_ context.Context, modelID, instanceTypeID string) ([]TestSuiteRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var runs []TestSuiteRun
	for _, run := range m.suiteRuns {
		if modelID != "" && run.ModelID != modelID {
			continue
		}
		if instanceTypeID != "" && run.InstanceTypeID != instanceTypeID {
			continue
		}
		runs = append(runs, *run)
	}
	return runs, nil
}

func (m *MockRepo) ListSuiteRunsWithNames(_ context.Context) ([]SuiteRunListItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var items []SuiteRunListItem
	for _, run := range m.suiteRuns {
		var modelHfID, instName string
		for _, mdl := range m.models {
			if mdl.ID == run.ModelID {
				modelHfID = mdl.HfID
				break
			}
		}
		for _, it := range m.instTypes {
			if it.ID == run.InstanceTypeID {
				instName = it.Name
				break
			}
		}
		items = append(items, SuiteRunListItem{
			ID:               run.ID,
			ModelHfID:        modelHfID,
			InstanceTypeName: instName,
			SuiteID:          run.SuiteID,
			Status:           run.Status,
			CreatedAt:        run.CreatedAt,
			StartedAt:        run.StartedAt,
			CompletedAt:      run.CompletedAt,
		})
	}
	return items, nil
}

func (m *MockRepo) DeleteSuiteRun(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.suiteRuns, id)
	// Also delete associated scenario results
	for k, r := range m.scenarioResults {
		if r.SuiteRunID == id {
			delete(m.scenarioResults, k)
		}
	}
	return nil
}

// Model Cache methods

func (m *MockRepo) CreateModelCache(_ context.Context, mc *ModelCache) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	id := fmt.Sprintf("cache-%08d", m.nextID)
	mc.ID = id
	mc.CreatedAt = time.Now()
	m.modelCache[id] = mc
	return id, nil
}

func (m *MockRepo) GetModelCache(_ context.Context, id string) (*ModelCache, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.modelCache[id], nil
}

func (m *MockRepo) GetModelCacheByHfID(_ context.Context, hfID, revision string) (*ModelCache, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, mc := range m.modelCache {
		if mc.HfID != nil && *mc.HfID == hfID && mc.HfRevision == revision {
			return mc, nil
		}
	}
	return nil, nil
}

func (m *MockRepo) ListModelCache(_ context.Context) ([]ModelCache, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var items []ModelCache
	for _, mc := range m.modelCache {
		items = append(items, *mc)
	}
	return items, nil
}

func (m *MockRepo) UpdateModelCacheStatus(_ context.Context, id, status string, errMsg *string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	mc, ok := m.modelCache[id]
	if !ok {
		return fmt.Errorf("model cache %s not found", id)
	}
	mc.Status = status
	mc.ErrorMessage = errMsg
	return nil
}

func (m *MockRepo) UpdateModelCacheComplete(_ context.Context, id string, sizeBytes int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	mc, ok := m.modelCache[id]
	if !ok {
		return fmt.Errorf("model cache %s not found", id)
	}
	mc.Status = "cached"
	mc.SizeBytes = &sizeBytes
	now := time.Now()
	mc.CachedAt = &now
	return nil
}

func (m *MockRepo) DeleteModelCache(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.modelCache, id)
	return nil
}

// Catalog matrix mock — returns whatever SeedCatalogMatrix populated.

// SeedCatalogMatrix lets tests preload the seeding matrix.
func (m *MockRepo) SeedCatalogMatrix(cm *CatalogMatrix) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.catalogMatrix = cm
}

// SeedCatalogStatus lets tests preload seed status rows.
func (m *MockRepo) SeedCatalogStatus(s *CatalogSeedStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.catalogSeeds == nil {
		m.catalogSeeds = make(map[string]*CatalogSeedStatus)
	}
	m.catalogSeeds[s.ID] = s
}

func (m *MockRepo) LoadCatalogMatrix(_ context.Context) (*CatalogMatrix, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.catalogMatrix == nil {
		return &CatalogMatrix{}, nil
	}
	cp := *m.catalogMatrix
	return &cp, nil
}

func (m *MockRepo) ModelCacheByHfID(_ context.Context) (map[string]ModelCache, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]ModelCache)
	for _, mc := range m.modelCache {
		if mc.HfID != nil {
			out[*mc.HfID] = *mc
		}
	}
	return out, nil
}

func (m *MockRepo) ListRunKeys(_ context.Context) ([]RunKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	type key struct{ a, b string }
	seen := make(map[key]bool)
	for _, run := range m.runs {
		if run.Status == "failed" {
			continue
		}
		var modelHfID, instName string
		for _, mdl := range m.models {
			if mdl.ID == run.ModelID {
				modelHfID = mdl.HfID
				break
			}
		}
		for _, it := range m.instTypes {
			if it.ID == run.InstanceTypeID {
				instName = it.Name
				break
			}
		}
		seen[key{modelHfID, instName}] = true
	}
	out := make([]RunKey, 0, len(seen))
	for k := range seen {
		out = append(out, RunKey{ModelHfID: k.a, InstanceTypeName: k.b})
	}
	return out, nil
}

func (m *MockRepo) CreateCatalogSeedStatus(_ context.Context, id string, total int, dryRun bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.catalogSeeds == nil {
		m.catalogSeeds = make(map[string]*CatalogSeedStatus)
	}
	now := time.Now()
	m.catalogSeeds[id] = &CatalogSeedStatus{
		ID:        id,
		Status:    "active",
		Total:     total,
		DryRun:    dryRun,
		StartedAt: now,
		UpdatedAt: now,
	}
	return nil
}

func (m *MockRepo) UpdateCatalogSeedProgress(_ context.Context, id string, completed int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.catalogSeeds[id]
	if !ok {
		return fmt.Errorf("seed %s not found", id)
	}
	s.Completed = completed
	s.UpdatedAt = time.Now()
	return nil
}

func (m *MockRepo) CompleteCatalogSeedStatus(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.catalogSeeds[id]
	if !ok {
		return fmt.Errorf("seed %s not found", id)
	}
	s.Status = "completed"
	now := time.Now()
	s.UpdatedAt = now
	s.CompletedAt = &now
	return nil
}

func (m *MockRepo) FailCatalogSeedStatus(_ context.Context, id, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.catalogSeeds[id]
	if !ok {
		return fmt.Errorf("seed %s not found", id)
	}
	s.Status = "failed"
	s.ErrorMessage = &errMsg
	now := time.Now()
	s.UpdatedAt = now
	s.CompletedAt = &now
	return nil
}

func (m *MockRepo) InterruptActiveCatalogSeeds(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for _, s := range m.catalogSeeds {
		if s.Status == "active" {
			s.Status = "interrupted"
			s.UpdatedAt = now
			s.CompletedAt = &now
		}
	}
	return nil
}

func (m *MockRepo) GetLatestCatalogSeedStatus(_ context.Context) (*CatalogSeedStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest *CatalogSeedStatus
	for _, s := range m.catalogSeeds {
		if latest == nil || s.StartedAt.After(latest.StartedAt) {
			cp := *s
			latest = &cp
		}
	}
	return latest, nil
}

func (m *MockRepo) GetActiveCatalogSeed(_ context.Context) (*CatalogSeedStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.catalogSeeds {
		if s.Status == "active" {
			cp := *s
			return &cp, nil
		}
	}
	return nil, nil
}

// --- ConfigRepo mock methods (PRD-32) ---

func (m *MockRepo) PutCatalogMatrix(_ context.Context, cm *CatalogMatrix, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Mock ignores optimistic concurrency — tests can call directly.
	cp := *cm
	m.catalogMatrix = &cp
	return nil
}

func (m *MockRepo) ListScenarioOverrides(_ context.Context) ([]ScenarioOverride, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ScenarioOverride, 0, len(m.scenarioOver))
	for _, o := range m.scenarioOver {
		out = append(out, *o)
	}
	return out, nil
}

func (m *MockRepo) GetScenarioOverride(_ context.Context, scenarioID string) (*ScenarioOverride, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if o, ok := m.scenarioOver[scenarioID]; ok {
		cp := *o
		return &cp, nil
	}
	return nil, nil
}

func (m *MockRepo) UpsertScenarioOverride(_ context.Context, o *ScenarioOverride) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.scenarioOver == nil {
		m.scenarioOver = make(map[string]*ScenarioOverride)
	}
	cp := *o
	cp.UpdatedAt = time.Now()
	m.scenarioOver[o.ScenarioID] = &cp
	return nil
}

func (m *MockRepo) DeleteScenarioOverride(_ context.Context, scenarioID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.scenarioOver, scenarioID)
	return nil
}

func (m *MockRepo) InsertAuditLog(_ context.Context, action, summary string, actor *string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.auditLog = append(m.auditLog, ConfigAuditEntry{
		ID:      int64(len(m.auditLog) + 1),
		At:      time.Now(),
		Action:  action,
		Actor:   actor,
		Summary: summary,
	})
	return nil
}

func (m *MockRepo) ListAuditLog(_ context.Context, limit int) ([]ConfigAuditEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 50
	}
	// Reverse chronological.
	n := len(m.auditLog)
	if n > limit {
		n = limit
	}
	out := make([]ConfigAuditEntry, n)
	for i := 0; i < n; i++ {
		out[i] = m.auditLog[len(m.auditLog)-1-i]
	}
	return out, nil
}

// --- ToolVersionsRepo mock methods (PRD-34) ---

// SeedToolVersions lets tests preload the tool_versions singleton.
func (m *MockRepo) SeedToolVersions(tv *ToolVersions) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *tv
	m.toolVersions = &cp
}

func (m *MockRepo) GetToolVersions(_ context.Context) (*ToolVersions, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.toolVersions == nil {
		// Match the migration-seeded defaults so tests that haven't called
		// SeedToolVersions still get a valid row.
		return &ToolVersions{
			FrameworkVersion:     "v0.19.0",
			InferencePerfVersion: "v0.2.0",
			UpdatedAt:            time.Now(),
		}, nil
	}
	cp := *m.toolVersions
	return &cp, nil
}

func (m *MockRepo) PutToolVersions(_ context.Context, tv *ToolVersions) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *tv
	cp.UpdatedAt = time.Now()
	m.toolVersions = &cp
	return nil
}
