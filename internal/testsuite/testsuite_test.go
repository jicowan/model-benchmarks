package testsuite

import (
	"testing"
)

func TestBuiltinSuites(t *testing.T) {
	expectedSuites := []string{"quick", "throughput", "stress", "standard", "comprehensive"}

	for _, id := range expectedSuites {
		s := Get(id)
		if s == nil {
			t.Errorf("expected suite %q to exist", id)
			continue
		}
		if s.ID != id {
			t.Errorf("suite %q has ID %q", id, s.ID)
		}
		if s.Name == "" {
			t.Errorf("suite %q has empty name", id)
		}
		if len(s.Scenarios) == 0 {
			t.Errorf("suite %q has no scenarios", id)
		}
	}
}

func TestGet_NotFound(t *testing.T) {
	s := Get("nonexistent")
	if s != nil {
		t.Error("expected nil for nonexistent suite")
	}
}

func TestList(t *testing.T) {
	suites := List()
	if len(suites) != 5 {
		t.Errorf("expected 5 suites, got %d", len(suites))
	}

	// Verify order (by duration)
	expected := []string{"quick", "throughput", "stress", "standard", "comprehensive"}
	for i, s := range suites {
		if s.ID != expected[i] {
			t.Errorf("suite %d: expected %q, got %q", i, expected[i], s.ID)
		}
	}
}

func TestSuiteDurations(t *testing.T) {
	tests := []struct {
		id       string
		expected int
	}{
		{"quick", 120},
		{"throughput", 120},
		{"stress", 240},
		{"standard", 420},
		{"comprehensive", 780},
	}

	for _, tc := range tests {
		s := Get(tc.id)
		if s == nil {
			t.Errorf("suite %q not found", tc.id)
			continue
		}
		if s.TotalDuration != tc.expected {
			t.Errorf("suite %q: TotalDuration = %d, want %d", tc.id, s.TotalDuration, tc.expected)
		}
	}
}

func TestStandardSuiteScenarios(t *testing.T) {
	s := Get("standard")
	if s == nil {
		t.Fatal("standard suite not found")
	}

	expected := []string{"chatbot", "batch", "production"}
	if len(s.Scenarios) != len(expected) {
		t.Errorf("expected %d scenarios, got %d", len(expected), len(s.Scenarios))
	}

	for i, scenarioID := range s.Scenarios {
		if scenarioID != expected[i] {
			t.Errorf("scenario %d: expected %q, got %q", i, expected[i], scenarioID)
		}
	}
}

func TestComprehensiveSuiteScenarios(t *testing.T) {
	s := Get("comprehensive")
	if s == nil {
		t.Fatal("comprehensive suite not found")
	}

	expected := []string{"chatbot", "batch", "stress", "production", "long-context"}
	if len(s.Scenarios) != len(expected) {
		t.Errorf("expected %d scenarios, got %d", len(expected), len(s.Scenarios))
	}

	for i, scenarioID := range s.Scenarios {
		if scenarioID != expected[i] {
			t.Errorf("scenario %d: expected %q, got %q", i, expected[i], scenarioID)
		}
	}
}
