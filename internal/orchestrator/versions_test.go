package orchestrator

import (
	"context"
	"testing"

	"github.com/accelbench/accelbench/internal/database"

	"k8s.io/client-go/kubernetes/fake"
)

func TestResolveInferencePerfImage_UsesDBTag(t *testing.T) {
	t.Setenv("INFERENCE_PERF_IMAGE", "") // clear any local override

	repo := database.NewMockRepo()
	repo.SeedToolVersions(&database.ToolVersions{
		FrameworkVersion:     "v0.19.0",
		InferencePerfVersion: "v0.3.0",
	})
	o := New(fake.NewSimpleClientset(), repo, "test-pod")

	got, err := o.resolveInferencePerfImage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := "quay.io/inference-perf/inference-perf:v0.3.0"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveInferencePerfImage_EnvOverrideWins(t *testing.T) {
	t.Setenv("INFERENCE_PERF_IMAGE", "localhost:5000/inference-perf:dev")

	repo := database.NewMockRepo()
	repo.SeedToolVersions(&database.ToolVersions{
		FrameworkVersion:     "v0.19.0",
		InferencePerfVersion: "v0.2.0", // should be ignored
	})
	o := New(fake.NewSimpleClientset(), repo, "test-pod")

	got, err := o.resolveInferencePerfImage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := "localhost:5000/inference-perf:dev"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// PRD-49: VLLM_IMAGE env override. Empty → manifest composes the legacy
// Docker-Hub template; non-empty → renderer uses the URI verbatim.
func TestResolveVLLMImageOverride(t *testing.T) {
	t.Setenv("VLLM_IMAGE", "")
	if got := ResolveVLLMImageOverride(); got != "" {
		t.Errorf("unset: got %q, want empty", got)
	}

	t.Setenv("VLLM_IMAGE", "public.ecr.aws/vllm/vllm-openai:v0.9.0")
	want := "public.ecr.aws/vllm/vllm-openai:v0.9.0"
	if got := ResolveVLLMImageOverride(); got != want {
		t.Errorf("set: got %q, want %q", got, want)
	}
}
