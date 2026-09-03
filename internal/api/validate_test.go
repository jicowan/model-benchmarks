package api

import (
	"strings"
	"testing"

	"github.com/accelbench/accelbench/internal/database"
)

func strp(s string) *string { return &s }

func TestValidateRunRequest_Accepts(t *testing.T) {
	good := []database.RunRequest{
		{ModelHfID: "meta-llama/Llama-3.1-8B-Instruct", HfToken: "hf_abcDEF123"},
		{ModelHfID: "gpt2"},
		{ModelHfID: "s3://accelbench-models/models/org/model", ModelS3URI: "s3://accelbench-models/models/org/model"},
		{ModelHfID: "Qwen/Qwen2.5-1.5B", ModelHfRevision: "refs/pr/12", FrameworkVersion: "v0.10.1.1"},
		{ModelHfID: "org/m", Quantization: strp("int8"), KVCacheDtype: "fp8", StreamerMode: "off"},
		{ModelHfID: "org/m", DeploymentMode: "disaggregated", NetworkMode: "tcp", NodePoolOverride: "multinode-us-east-2a"},
		{ModelHfID: "org/m", APIType: "chat_completion", DatasetName: "sharegpt", ScenarioID: "chatbot"},
	}
	for i, req := range good {
		if err := validateRunRequest(&req); err != nil {
			t.Errorf("case %d: unexpected error: %v", i, err)
		}
	}
}

func TestValidateRunRequest_Rejects(t *testing.T) {
	cases := []struct {
		name string
		req  database.RunRequest
		want string
	}{
		{"newline in token", database.RunRequest{ModelHfID: "org/m", HfToken: "x\"\n  privileged: true"}, "hf_token"},
		{"space in token", database.RunRequest{ModelHfID: "org/m", HfToken: "hf_a b"}, "hf_token"},
		{"quote in model id", database.RunRequest{ModelHfID: `org/m"`}, "model_hf_id"},
		{"three segments", database.RunRequest{ModelHfID: "a/b/c"}, "model_hf_id"},
		{"yaml in model id", database.RunRequest{ModelHfID: "org/m\n- hostPath"}, "model_hf_id"},
		{"bad s3 uri", database.RunRequest{ModelHfID: "org/m", ModelS3URI: "s3://Bad Bucket/x"}, "model_s3_uri"},
		{"s3 uri with quote", database.RunRequest{ModelHfID: "org/m", ModelS3URI: `s3://b/k"`}, "model_s3_uri"},
		{"image tag with slash", database.RunRequest{ModelHfID: "org/m", FrameworkVersion: "v1/../x"}, "framework_version"},
		{"image tag with space", database.RunRequest{ModelHfID: "org/m", FrameworkVersion: "v1 latest"}, "framework_version"},
		{"unknown quantization", database.RunRequest{ModelHfID: "org/m", Quantization: strp("gguf")}, "quantization"},
		{"unknown kv dtype", database.RunRequest{ModelHfID: "org/m", KVCacheDtype: "bf16"}, "kv_cache_dtype"},
		{"upper-case mode", database.RunRequest{ModelHfID: "org/m", DeploymentMode: "Distributed"}, "deployment_mode"},
		{"bad revision", database.RunRequest{ModelHfID: "org/m", ModelHfRevision: "main; rm -rf"}, "model_hf_revision"},
		{"long model id", database.RunRequest{ModelHfID: strings.Repeat("a", 300)}, "model_hf_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRunRequest(&tc.req)
			if err == nil {
				t.Fatalf("expected error mentioning %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention field %q", err.Error(), tc.want)
			}
		})
	}
}

func TestValidateSuiteRunRequest(t *testing.T) {
	ok := database.SuiteRunRequest{ModelHfID: "org/m", SuiteID: "standard", ScenarioIDs: []string{"chatbot", "batch"}}
	if err := validateSuiteRunRequest(&ok); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	bad := database.SuiteRunRequest{ModelHfID: "org/m", ScenarioIDs: []string{"chatbot", "x y"}}
	if err := validateSuiteRunRequest(&bad); err == nil || !strings.Contains(err.Error(), "scenario_ids") {
		t.Fatalf("expected scenario_ids error, got %v", err)
	}
	badTok := database.SuiteRunRequest{ModelHfID: "org/m", HfToken: "a\tb"}
	if err := validateSuiteRunRequest(&badTok); err == nil {
		t.Fatal("expected hf_token error")
	}
}

func TestValidateCacheModelRequest(t *testing.T) {
	if err := validateCacheModelRequest(&database.CacheModelRequest{ModelHfID: "org/m", HfRevision: "main"}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if err := validateCacheModelRequest(&database.CacheModelRequest{ModelHfID: "s3://b/k"}); err == nil {
		t.Fatal("s3 uri must be rejected as a cache source")
	}
	if err := validateCacheModelRequest(&database.CacheModelRequest{ModelHfID: "org/m", HfRevision: "a b"}); err == nil {
		t.Fatal("expected hf_revision error")
	}
}
