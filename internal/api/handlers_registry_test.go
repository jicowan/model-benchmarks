package api

import "testing"

// TestIsPullThroughRepo (PRD-66 Part 2a follow-up): the Registry card surfaces
// repos under any configured pull-through prefix — Docker Hub AND GHCR — not
// just dockerhub/*, so the cached llm-d-aws (PP) image shows once pulled.
func TestIsPullThroughRepo(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"dockerhub/vllm/vllm-openai", true},
		{"ghcr/llm-d/llm-d-aws", true},
		{"dockerhub/lmsysorg/sglang", true},
		{"accelbench-api", false},   // a first-party repo, not a cache
		{"quay/some/image", false},  // prefix we don't configure
		{"ghcr", false},             // prefix without trailing slash isn't a repo path
		{"", false},
	}
	for _, c := range cases {
		if got := isPullThroughRepo(c.name); got != c.want {
			t.Errorf("isPullThroughRepo(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}
