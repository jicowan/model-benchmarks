package orchestrator

import (
	"context"
	"testing"

	"github.com/accelbench/accelbench/internal/database"

	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// TestResolveS3Model covers the shared cached-model policy (PRD-65 Layer 2):
// explicit URI wins; else a cached HF model auto-detects to its S3 URI + Run:ai;
// else HF download with no streamer.
func TestResolveS3Model(t *testing.T) {
	strptr := func(s string) *string { return &s }

	t.Run("explicit S3 URI wins", func(t *testing.T) {
		o := New(k8sfake.NewSimpleClientset(), database.NewMockRepo(), "pod")
		cfg := RunConfig{RunID: "run12345", Request: &database.RunRequest{
			ModelHfID:  "org/model",
			ModelS3URI: "s3://bucket/explicit",
		}}
		uri, useRunai := o.resolveS3Model(context.Background(), cfg)
		if uri != "s3://bucket/explicit" || !useRunai {
			t.Errorf("explicit URI: got (%q, %v), want (s3://bucket/explicit, true)", uri, useRunai)
		}
	})

	t.Run("cached HF model auto-detects", func(t *testing.T) {
		repo := database.NewMockRepo()
		_, _ = repo.CreateModelCache(context.Background(), &database.ModelCache{
			HfID: strptr("org/model"), HfRevision: "main",
			S3URI: "s3://bucket/cached", Status: "cached",
		})
		o := New(k8sfake.NewSimpleClientset(), repo, "pod")
		cfg := RunConfig{RunID: "run12345", Request: &database.RunRequest{
			ModelHfID: "org/model", // no explicit URI, no revision → defaults to "main"
		}}
		uri, useRunai := o.resolveS3Model(context.Background(), cfg)
		if uri != "s3://bucket/cached" || !useRunai {
			t.Errorf("cached: got (%q, %v), want (s3://bucket/cached, true)", uri, useRunai)
		}
	})

	t.Run("uncached model falls back to HF", func(t *testing.T) {
		o := New(k8sfake.NewSimpleClientset(), database.NewMockRepo(), "pod")
		cfg := RunConfig{RunID: "run12345", Request: &database.RunRequest{
			ModelHfID: "org/not-cached",
		}}
		uri, useRunai := o.resolveS3Model(context.Background(), cfg)
		if uri != "" || useRunai {
			t.Errorf("uncached: got (%q, %v), want (\"\", false)", uri, useRunai)
		}
	})

	t.Run("cache entry not yet cached (still downloading) → HF", func(t *testing.T) {
		repo := database.NewMockRepo()
		_, _ = repo.CreateModelCache(context.Background(), &database.ModelCache{
			HfID: strptr("org/model"), HfRevision: "main",
			S3URI: "s3://bucket/partial", Status: "downloading",
		})
		o := New(k8sfake.NewSimpleClientset(), repo, "pod")
		cfg := RunConfig{RunID: "run12345", Request: &database.RunRequest{
			ModelHfID: "org/model",
		}}
		uri, useRunai := o.resolveS3Model(context.Background(), cfg)
		if uri != "" || useRunai {
			t.Errorf("non-cached status: got (%q, %v), want (\"\", false)", uri, useRunai)
		}
	})
}
