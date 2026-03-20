package database

import "testing"

func TestExtractModelFamily(t *testing.T) {
	tests := []struct {
		hfID     string
		expected string
	}{
		{"meta-llama/Llama-3.1-8B-Instruct", "llama"},
		{"mistralai/Mistral-7B-Instruct-v0.3", "mistral"},
		{"Qwen/Qwen2.5-7B-Instruct", "qwen"},
		{"deepseek-ai/DeepSeek-R1-Distill-Llama-8B", "deepseek"}, // org takes priority
		{"google/gemma-7b", "gemma"},
		{"microsoft/phi-2", "phi"},
		{"TinyLlama/TinyLlama-1.1B-Chat-v1.0", "llama"}, // org contains llama
		{"unknown-org/some-model", ""},                   // no match
	}

	for _, tt := range tests {
		t.Run(tt.hfID, func(t *testing.T) {
			got := extractModelFamily(tt.hfID)
			if got != tt.expected {
				t.Errorf("extractModelFamily(%q) = %q, want %q", tt.hfID, got, tt.expected)
			}
		})
	}
}
