package recommend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// FetchModelConfigFromS3 builds a ModelConfig by reading the model's config.json
// and model.safetensors.index.json from a cached S3 prefix. This avoids needing
// an HF token for gated models that have already been cached to S3.
//
// s3URI is the prefix under which files live, e.g. s3://bucket/models/meta-llama/Llama-3.1-8B.
func FetchModelConfigFromS3(ctx context.Context, s3URI string) (*ModelConfig, error) {
	bucket, prefix, ok := parseS3URI(s3URI)
	if !ok {
		return nil, fmt.Errorf("invalid s3 URI: %s", s3URI)
	}

	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	client := s3.NewFromConfig(awsCfg)

	configKey := strings.TrimSuffix(prefix, "/") + "/config.json"
	raw, err := getS3Object(ctx, client, bucket, configKey)
	if err != nil {
		return nil, fmt.Errorf("read config.json from s3://%s/%s: %w", bucket, configKey, err)
	}
	var cfg hfConfigJSON
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse config.json: %w", err)
	}

	// Multimodal models (Gemma 4, LLaVA, etc.) nest LLM config under text_config.
	srcCfg := &cfg
	if cfg.TextConfig != nil && cfg.HiddenSize == 0 {
		srcCfg = cfg.TextConfig
	}

	mc := &ModelConfig{
		HiddenSize:            srcCfg.HiddenSize,
		NumAttentionHeads:     srcCfg.NumAttentionHeads,
		NumKeyValueHeads:      srcCfg.NumKeyValueHeads,
		NumHiddenLayers:       srcCfg.NumHiddenLayers,
		MaxPositionEmbeddings: srcCfg.MaxPositionEmbeddings,
		TorchDtype:            srcCfg.TorchDtype,
		ModelType:             cfg.ModelType,
		TransformersVersion:   cfg.TransformersVersion,
		// config.json doesn't carry HF's pipeline_tag, so PipelineTag stays
		// empty and isUnsupportedModelKind falls back to sniffing
		// Architectures for *ForMaskedLM / *ForSequenceClassification / etc.
		Architectures: cfg.Architectures,
	}
	if srcCfg.SlidingWindow != nil && *srcCfg.SlidingWindow > 0 {
		mc.SlidingWindow = *srcCfg.SlidingWindow
	}
	if mc.NumKeyValueHeads == 0 {
		mc.NumKeyValueHeads = mc.NumAttentionHeads
	}

	qcfg := cfg.QuantizationConfig
	if qcfg == nil && srcCfg.QuantizationConfig != nil {
		qcfg = srcCfg.QuantizationConfig
	}
	if qcfg != nil {
		mc.PreQuantized = true
		mc.PreQuantMethod = qcfg.QuantMethod
		mc.PreQuantBits = extractQuantBits(qcfg)
	}

	// Try to read parameter count and dtype breakdown from the safetensors
	// index. For single-shard models the index may not exist — in that case
	// estimate from architecture.
	indexKey := strings.TrimSuffix(prefix, "/") + "/model.safetensors.index.json"
	if idxRaw, err := getS3Object(ctx, client, bucket, indexKey); err == nil {
		params, actualBytes := parseSafetensorsIndex(idxRaw)
		mc.ParameterCount = params
		mc.ActualMemoryBytes = actualBytes
	}
	if mc.ParameterCount == 0 {
		mc.ParameterCount = estimateParameterCount(srcCfg)
	}

	return mc, nil
}

// parseSafetensorsIndex extracts total parameters and actual-memory bytes from
// a safetensors index file. The index has a weight_map (tensor name -> shard
// file) and metadata.total_size (bytes). We return both so callers can reason
// about mixed-precision models when possible; if the per-tensor dtype breakdown
// isn't available we fall back to total_size as ActualMemoryBytes.
func parseSafetensorsIndex(raw []byte) (int64, int64) {
	var idx struct {
		Metadata struct {
			TotalSize      int64 `json:"total_size"`
			TotalParameters int64 `json:"total_parameters"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &idx); err != nil {
		return 0, 0
	}
	return idx.Metadata.TotalParameters, idx.Metadata.TotalSize
}

func parseS3URI(uri string) (bucket, prefix string, ok bool) {
	if !strings.HasPrefix(uri, "s3://") {
		return "", "", false
	}
	parts := strings.SplitN(strings.TrimPrefix(uri, "s3://"), "/", 2)
	if len(parts) != 2 || parts[0] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func getS3Object(ctx context.Context, client *s3.Client, bucket, key string) ([]byte, error) {
	out, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: &bucket, Key: &key})
	if err != nil {
		return nil, err
	}
	defer out.Body.Close()
	return io.ReadAll(out.Body)
}
