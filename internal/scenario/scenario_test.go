package scenario

import (
	"testing"
)

func TestBuiltinScenarios(t *testing.T) {
	expectedScenarios := []string{"chatbot", "batch", "stress", "production", "long-context"}

	for _, id := range expectedScenarios {
		s := Get(id)
		if s == nil {
			t.Errorf("expected scenario %q to exist", id)
			continue
		}
		if s.ID != id {
			t.Errorf("scenario %q has ID %q", id, s.ID)
		}
		if s.Name == "" {
			t.Errorf("scenario %q has empty name", id)
		}
		if len(s.Stages) == 0 {
			t.Errorf("scenario %q has no stages", id)
		}
	}
}

func TestGet_NotFound(t *testing.T) {
	s := Get("nonexistent")
	if s != nil {
		t.Error("expected nil for nonexistent scenario")
	}
}

func TestList(t *testing.T) {
	scenarios := List()
	if len(scenarios) != 5 {
		t.Errorf("expected 5 scenarios, got %d", len(scenarios))
	}

	// Verify order
	expected := []string{"chatbot", "batch", "stress", "production", "long-context"}
	for i, s := range scenarios {
		if s.ID != expected[i] {
			t.Errorf("scenario %d: expected %q, got %q", i, expected[i], s.ID)
		}
	}
}

func TestTotalDuration(t *testing.T) {
	tests := []struct {
		id       string
		expected int
	}{
		{"chatbot", 120},
		{"batch", 120},
		{"stress", 240}, // 4 stages x 60s
		{"production", 180},
		{"long-context", 120},
	}

	for _, tc := range tests {
		s := Get(tc.id)
		if s == nil {
			t.Errorf("scenario %q not found", tc.id)
			continue
		}
		if got := s.TotalDuration(); got != tc.expected {
			t.Errorf("scenario %q: TotalDuration() = %d, want %d", tc.id, got, tc.expected)
		}
	}
}

func TestToInferencePerfConfig(t *testing.T) {
	s := Get("chatbot")
	if s == nil {
		t.Fatal("chatbot scenario not found")
	}

	cfg := s.ToInferencePerfConfig("test-model", "test-host", 8000)

	if cfg.ModelHfID != "test-model" {
		t.Errorf("ModelHfID = %q, want test-model", cfg.ModelHfID)
	}
	if cfg.TargetHost != "test-host" {
		t.Errorf("TargetHost = %q, want test-host", cfg.TargetHost)
	}
	if cfg.TargetPort != 8000 {
		t.Errorf("TargetPort = %d, want 8000", cfg.TargetPort)
	}
	if !cfg.Streaming {
		t.Error("expected Streaming = true for chatbot")
	}
	if cfg.LoadType != "constant" {
		t.Errorf("LoadType = %q, want constant", cfg.LoadType)
	}
	if len(cfg.Stages) != 1 {
		t.Errorf("expected 1 stage, got %d", len(cfg.Stages))
	}
	if cfg.InputMean != 256 {
		t.Errorf("InputMean = %d, want 256", cfg.InputMean)
	}
	if cfg.OutputMean != 128 {
		t.Errorf("OutputMean = %d, want 128", cfg.OutputMean)
	}
}

func TestStressScenario_MultiStage(t *testing.T) {
	s := Get("stress")
	if s == nil {
		t.Fatal("stress scenario not found")
	}

	if len(s.Stages) != 4 {
		t.Errorf("expected 4 stages, got %d", len(s.Stages))
	}

	expectedRates := []int{2, 5, 10, 20}
	for i, stage := range s.Stages {
		if stage.Rate != expectedRates[i] {
			t.Errorf("stage %d: rate = %d, want %d", i, stage.Rate, expectedRates[i])
		}
		if stage.Duration != 60 {
			t.Errorf("stage %d: duration = %d, want 60", i, stage.Duration)
		}
	}
}

func TestProductionScenario_PoissonLoad(t *testing.T) {
	s := Get("production")
	if s == nil {
		t.Fatal("production scenario not found")
	}

	if s.LoadType != "poisson" {
		t.Errorf("LoadType = %q, want poisson", s.LoadType)
	}
}
