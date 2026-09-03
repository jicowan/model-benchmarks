package api

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/accelbench/accelbench/internal/database"
)

// PRD-68 P1: request-field validation.
//
// Every string a caller supplies on a run / suite / model-cache request ends
// up in one of three places: a Postgres column, a rendered Kubernetes
// manifest, or a container argument. The manifests are now rendered through
// yamlQuote (see internal/manifest), so a hostile value can no longer break
// out of its YAML scalar — but a value that is syntactically safe and
// semantically nonsense (a model id with spaces, a 4 KB "revision") still
// costs a Karpenter node before vLLM rejects it. These validators reject
// such input at the API boundary with a 400 and a field-specific message.
//
// The patterns are deliberately conservative; widen them here (with a test)
// if a legitimate value is rejected.

var (
	// HF repo id: "org/name" or "name"; each segment is [A-Za-z0-9._-]
	// starting with an alphanumeric. Matches what huggingface.co accepts.
	hfRepoIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}(/[A-Za-z0-9][A-Za-z0-9._-]{0,127})?$`)

	// s3://bucket/key — bucket per the S3 naming rules, key restricted to
	// the safe character set (no spaces / quotes / control chars).
	s3URIRe = regexp.MustCompile(`^s3://[a-z0-9][a-z0-9.-]{1,61}[a-z0-9](/[A-Za-z0-9._/-]{0,900})?$`)

	// Git revision / branch / tag as HF accepts it.
	hfRevisionRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)

	// OCI image tag (also what we use for framework_version).
	imageTagRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

	// Lower-case identifier used for enums we don't own an exhaustive list
	// of (dataset name, api type, deployment/network mode, node pool).
	identRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,62}$`)

	// Printable ASCII, no whitespace — HF tokens are "hf_..." base62.
	tokenRe = regexp.MustCompile(`^[\x21-\x7E]{0,512}$`)

	allowedQuantization = map[string]bool{"": true, "fp16": true, "fp8": true, "int8": true, "int4": true}
	allowedKVCacheDtype = map[string]bool{"": true, "auto": true, "fp8": true, "fp8_e5m2": true, "fp8_e4m3": true}
	allowedStreamerMode = map[string]bool{"": true, "auto": true, "off": true}
)

// validationError is a 400-class error with a field-specific message.
type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }

func vErr(format string, args ...any) error {
	return &validationError{msg: fmt.Sprintf(format, args...)}
}

// validateModelRef accepts either an HF repo id or an s3:// URI (the API
// derives model_hf_id from model_s3_uri for S3-only models).
func validateModelRef(field, v string) error {
	if v == "" {
		return nil
	}
	if strings.HasPrefix(v, "s3://") {
		return validateS3URI(field, v)
	}
	if !hfRepoIDRe.MatchString(v) {
		return vErr("%s must be a HuggingFace repo id like org/model (got %q)", field, truncate(v))
	}
	return nil
}

func validateS3URI(field, v string) error {
	if v == "" {
		return nil
	}
	if !s3URIRe.MatchString(v) {
		return vErr("%s must be an s3://bucket/prefix URI using only [A-Za-z0-9._/-] (got %q)", field, truncate(v))
	}
	return nil
}

func validateRevision(field, v string) error {
	if v == "" {
		return nil
	}
	if !hfRevisionRe.MatchString(v) {
		return vErr("%s must be a git revision / branch / tag (got %q)", field, truncate(v))
	}
	return nil
}

func validateImageTag(field, v string) error {
	if v == "" {
		return nil
	}
	if !imageTagRe.MatchString(v) {
		return vErr("%s must be a valid image tag (got %q)", field, truncate(v))
	}
	return nil
}

func validateIdent(field, v string) error {
	if v == "" {
		return nil
	}
	if !identRe.MatchString(v) {
		return vErr("%s must be a lower-case identifier (got %q)", field, truncate(v))
	}
	return nil
}

func validateToken(field, v string) error {
	if !tokenRe.MatchString(v) {
		return vErr("%s contains whitespace or non-printable characters", field)
	}
	return nil
}

func validateEnum(field, v string, allowed map[string]bool) error {
	if allowed[v] {
		return nil
	}
	keys := make([]string, 0, len(allowed))
	for k := range allowed {
		if k != "" {
			keys = append(keys, k)
		}
	}
	return vErr("%s must be one of %s (got %q)", field, strings.Join(keys, ", "), truncate(v))
}

// validateRunRequest checks every free-text field on a RunRequest. Numeric
// knobs are range-checked elsewhere (validateStreamerKnobs, the recommender).
func validateRunRequest(req *database.RunRequest) error {
	checks := []error{
		validateModelRef("model_hf_id", req.ModelHfID),
		validateRevision("model_hf_revision", req.ModelHfRevision),
		validateS3URI("model_s3_uri", req.ModelS3URI),
		validateImageTag("framework_version", req.FrameworkVersion),
		validateIdent("framework", req.Framework),
		validateIdent("dataset_name", req.DatasetName),
		validateIdent("run_type", req.RunType),
		validateIdent("scenario_id", req.ScenarioID),
		validateIdent("api_type", req.APIType),
		validateIdent("deployment_mode", req.DeploymentMode),
		validateIdent("network_mode", req.NetworkMode),
		validateIdent("node_pool_override", req.NodePoolOverride),
		validateEnum("kv_cache_dtype", req.KVCacheDtype, allowedKVCacheDtype),
		validateEnum("streamer_mode", req.StreamerMode, allowedStreamerMode),
		validateToken("hf_token", req.HfToken),
	}
	if req.Quantization != nil {
		checks = append(checks, validateEnum("quantization", *req.Quantization, allowedQuantization))
	}
	checks = append(checks, validateIdent("pd_decider_strategy", req.PDDeciderStrategy))
	for _, err := range checks {
		if err != nil {
			return err
		}
	}
	return nil
}

// validateSuiteRunRequest is the SuiteRunRequest counterpart.
func validateSuiteRunRequest(req *database.SuiteRunRequest) error {
	checks := []error{
		validateModelRef("model_hf_id", req.ModelHfID),
		validateRevision("model_hf_revision", req.ModelHfRevision),
		validateS3URI("model_s3_uri", req.ModelS3URI),
		validateImageTag("framework_version", req.FrameworkVersion),
		validateIdent("framework", req.Framework),
		validateIdent("suite_id", req.SuiteID),
		validateEnum("kv_cache_dtype", req.KVCacheDtype, allowedKVCacheDtype),
		validateEnum("streamer_mode", req.StreamerMode, allowedStreamerMode),
		validateToken("hf_token", req.HfToken),
	}
	if req.Quantization != nil {
		checks = append(checks, validateEnum("quantization", *req.Quantization, allowedQuantization))
	}
	for _, id := range req.ScenarioIDs {
		checks = append(checks, validateIdent("scenario_ids", id))
	}
	for _, err := range checks {
		if err != nil {
			return err
		}
	}
	return nil
}

// validateCacheModelRequest covers POST /model-cache.
func validateCacheModelRequest(req *database.CacheModelRequest) error {
	if err := validateModelRef("model_hf_id", req.ModelHfID); err != nil {
		return err
	}
	if strings.HasPrefix(req.ModelHfID, "s3://") {
		return vErr("model_hf_id must be a HuggingFace repo id, not an S3 URI")
	}
	if err := validateRevision("hf_revision", req.HfRevision); err != nil {
		return err
	}
	return validateToken("hf_token", req.HfToken)
}

func truncate(s string) string {
	if len(s) > 64 {
		return s[:64] + "…"
	}
	return s
}
