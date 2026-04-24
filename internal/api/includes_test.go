package api

import "testing"

func TestParseIncludes_Empty(t *testing.T) {
	if parseIncludes("") != nil {
		t.Error("empty string should return nil")
	}
}

func TestParseIncludes_Single(t *testing.T) {
	s := parseIncludes("metrics")
	if !s.Has("metrics") {
		t.Error("should contain metrics")
	}
	if s.Has("pricing") {
		t.Error("should not contain pricing")
	}
}

func TestParseIncludes_Multiple(t *testing.T) {
	s := parseIncludes("metrics,pricing,instance")
	for _, tok := range []string{"metrics", "pricing", "instance"} {
		if !s.Has(tok) {
			t.Errorf("missing %q", tok)
		}
	}
}

func TestParseIncludes_Whitespace(t *testing.T) {
	s := parseIncludes(" metrics , pricing ")
	if !s.Has("metrics") || !s.Has("pricing") {
		t.Error("should trim whitespace")
	}
}

func TestParseIncludes_TrailingComma(t *testing.T) {
	s := parseIncludes("metrics,")
	if !s.Has("metrics") {
		t.Error("should contain metrics")
	}
	if len(s) != 1 {
		t.Errorf("length = %d, want 1", len(s))
	}
}

func TestIncludeSet_Has_Nil(t *testing.T) {
	var s IncludeSet
	if s.Has("anything") {
		t.Error("nil set should return false")
	}
}
