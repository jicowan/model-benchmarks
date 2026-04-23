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
	o := New(fake.NewSimpleClientset(), repo)

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
	o := New(fake.NewSimpleClientset(), repo)

	got, err := o.resolveInferencePerfImage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := "localhost:5000/inference-perf:dev"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
