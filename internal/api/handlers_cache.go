package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/accelbench/accelbench/internal/database"
	"github.com/accelbench/accelbench/internal/manifest"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
)

const (
	cacheNamespace = "accelbench"
	cacheJobTTL    = 2 * time.Hour
	cacheJobPoll   = 30 * time.Second
)

func (s *Server) handleListModelCache(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := database.ModelCacheFilter{
		Status: q.Get("status"),
		Sort:   q.Get("sort"),
		Order:  q.Get("order"),
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Offset = n
		}
	}

	items, total, err := s.repo.ListModelCache(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list model cache failed")
		return
	}
	if items == nil {
		items = []database.ModelCache{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rows":  items,
		"total": total,
	})
}

// handleModelCacheStats serves GET /api/v1/model-cache/stats (PRD-35).
// The Models page stat cards used to compute these client-side from the list;
// after PRD-36 paginated the list those counts broke. This replaces them
// with a server-side aggregate.
func (s *Server) handleModelCacheStats(w http.ResponseWriter, r *http.Request) {
	const cacheKey = "model-cache-stats"
	if data := s.cache.Get(cacheKey); data != nil {
		serveCacheHit(w, data)
		return
	}
	stats, err := s.repo.ModelCacheStats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "model cache stats: "+err.Error())
		return
	}
	s.writeCachedJSON(w, cacheKey, http.StatusOK, stats)
}

func (s *Server) handleGetModelCache(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	mc, err := s.repo.GetModelCache(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if mc == nil {
		writeError(w, http.StatusNotFound, "cache entry not found")
		return
	}
	writeJSON(w, http.StatusOK, mc)
}

func (s *Server) handleCreateModelCache(w http.ResponseWriter, r *http.Request) {
	var req database.CacheModelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ModelHfID == "" {
		writeError(w, http.StatusBadRequest, "model_hf_id is required")
		return
	}
	if req.HfRevision == "" {
		req.HfRevision = "main"
	}

	ctx := r.Context()

	existing, _ := s.repo.GetModelCacheByHfID(ctx, req.ModelHfID, req.HfRevision)
	if existing != nil && (existing.Status == "caching" || existing.Status == "cached") {
		writeError(w, http.StatusConflict, fmt.Sprintf("model already %s (id: %s)", existing.Status, existing.ID))
		return
	}
	if existing != nil && existing.Status == "failed" {
		_ = s.repo.DeleteModelCache(ctx, existing.ID)
	}

	cacheImage := os.Getenv("CACHE_JOB_IMAGE")
	if cacheImage == "" {
		writeError(w, http.StatusInternalServerError, "CACHE_JOB_IMAGE not configured")
		return
	}
	s3Bucket := os.Getenv("MODELS_S3_BUCKET")
	if s3Bucket == "" {
		writeError(w, http.StatusInternalServerError, "MODELS_S3_BUCKET not configured")
		return
	}
	awsRegion := os.Getenv("AWS_REGION")
	if awsRegion == "" {
		awsRegion = "us-east-2"
	}

	modelPath := modelPathFromHfID(req.ModelHfID)
	s3URI := fmt.Sprintf("s3://%s/models/%s", s3Bucket, modelPath)
	displayName := req.ModelHfID

	mc := &database.ModelCache{
		HfID:        &req.ModelHfID,
		HfRevision:  req.HfRevision,
		S3URI:       s3URI,
		DisplayName: displayName,
		Status:      "pending",
	}

	id, err := s.repo.CreateModelCache(ctx, mc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("create cache entry: %v", err))
		return
	}

	jobName := fmt.Sprintf("cache-%s", id[:8])
	hfToken := req.HfToken
	if hfToken == "" && s.secrets != nil {
		// PRD-31: fall back to the platform HF token from Secrets Manager.
		// Errors are logged and swallowed — if the token isn't retrievable
		// the cache job will fail with a clearer HF 401 than a secrets error.
		if tok, err := s.secrets.GetHFToken(ctx); err == nil {
			hfToken = tok
		} else {
			log.Printf("resolve platform HF token for cache job: %v", err)
		}
	}

	yamlStr, err := manifest.RenderCacheJob(manifest.CacheJobParams{
		Name:       jobName,
		Namespace:  cacheNamespace,
		CacheID:    id,
		CacheImage: cacheImage,
		ModelHfID:  req.ModelHfID,
		HfRevision: req.HfRevision,
		ModelPath:  modelPath,
		S3Bucket:   s3Bucket,
		HfToken:    hfToken,
		AWSRegion:  awsRegion,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("render cache job: %v", err))
		return
	}

	if err := s.applyJobYAML(ctx, cacheNamespace, yamlStr); err != nil {
		errMsg := fmt.Sprintf("create cache job: %v", err)
		s.repo.UpdateModelCacheStatus(ctx, id, "failed", &errMsg)
		writeError(w, http.StatusInternalServerError, errMsg)
		return
	}

	jn := jobName
	_ = s.repo.UpdateModelCacheStatus(ctx, id, "caching", nil)
	mc.JobName = &jn

	go s.watchCacheJob(id, jobName)

	s.cache.Invalidate("model-cache-stats")
	writeJSON(w, http.StatusAccepted, map[string]string{"id": id, "status": "caching"})
}

// stopCacheJob deletes the Job with Foreground propagation and returns only
// once the Job and its pods are gone. Foreground makes the apiserver hold
// the Job object until the GC has marked all dependents (pods) for deletion
// and they've terminated. That's the behavior users expect when clicking
// "cancel" or "delete" on a running cache job: the pod stops writing to S3
// before the handler returns, so the subsequent S3 prefix delete doesn't
// race the still-uploading pod.
func (s *Server) stopCacheJob(ctx context.Context, jobName string) error {
	propagation := metav1.DeletePropagationForeground
	err := s.client.BatchV1().Jobs(cacheNamespace).Delete(ctx, jobName, metav1.DeleteOptions{
		PropagationPolicy: &propagation,
	})
	if err != nil {
		// Already gone is fine — the caller's intent is satisfied.
		if strings.Contains(err.Error(), "not found") {
			return nil
		}
		return err
	}
	// Poll until the Job is gone. Foreground deletion can still take up to
	// terminationGracePeriodSeconds (30s) while the pod receives SIGTERM
	// and exits.
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		_, err := s.client.BatchV1().Jobs(cacheNamespace).Get(ctx, jobName, metav1.GetOptions{})
		if err != nil && strings.Contains(err.Error(), "not found") {
			return nil
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("cache job %s did not terminate within 90s", jobName)
}

func (s *Server) handleCancelModelCache(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	mc, err := s.repo.GetModelCache(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if mc == nil {
		writeError(w, http.StatusNotFound, "cache entry not found")
		return
	}
	if mc.Status != "caching" && mc.Status != "pending" {
		writeError(w, http.StatusConflict, fmt.Sprintf("cannot cancel: status is %q", mc.Status))
		return
	}

	if mc.JobName != nil {
		if err := s.stopCacheJob(ctx, *mc.JobName); err != nil {
			log.Printf("[cache %s] stop job: %v", id[:8], err)
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("stop job: %v", err))
			return
		}
	}

	errMsg := "cancelled by user"
	if err := s.repo.UpdateModelCacheStatus(ctx, id, "cancelled", &errMsg); err != nil {
		log.Printf("[cache %s] mark cancelled: %v", id[:8], err)
	}

	s.cache.Invalidate("model-cache-stats")
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

func (s *Server) handleDeleteModelCache(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	mc, err := s.repo.GetModelCache(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if mc == nil {
		writeError(w, http.StatusNotFound, "cache entry not found")
		return
	}

	// Mark deleting first so watchCacheJob observes the intent and won't
	// flip the row to "failed" or "cached" while we're tearing it down.
	_ = s.repo.UpdateModelCacheStatus(ctx, id, "deleting", nil)

	// Stop the job synchronously — pod must be gone before we clear S3,
	// otherwise the still-running uploads race the prefix delete.
	if (mc.Status == "caching" || mc.Status == "pending") && mc.JobName != nil {
		if err := s.stopCacheJob(ctx, *mc.JobName); err != nil {
			log.Printf("[cache %s] stop job: %v", id[:8], err)
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("stop job: %v", err))
			return
		}
	}

	go s.deleteS3Prefix(mc.S3URI)

	_ = s.repo.DeleteModelCache(context.Background(), id)

	s.cache.Invalidate("model-cache-stats")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleBulkDeleteModelCache(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "ids is required")
		return
	}
	if len(req.IDs) > 100 {
		writeError(w, http.StatusBadRequest, "max 100 ids per request")
		return
	}

	ctx := r.Context()
	results := make([]map[string]string, 0, len(req.IDs))
	for _, id := range req.IDs {
		mc, err := s.repo.GetModelCache(ctx, id)
		if err != nil || mc == nil {
			results = append(results, map[string]string{"id": id, "status": "error", "error": "not found"})
			continue
		}

		_ = s.repo.UpdateModelCacheStatus(ctx, id, "deleting", nil)

		if (mc.Status == "caching" || mc.Status == "pending") && mc.JobName != nil {
			if err := s.stopCacheJob(ctx, *mc.JobName); err != nil {
				log.Printf("[cache %s] stop job: %v", id[:8], err)
				results = append(results, map[string]string{"id": id, "status": "error", "error": err.Error()})
				continue
			}
		}

		go s.deleteS3Prefix(mc.S3URI)
		_ = s.repo.DeleteModelCache(context.Background(), id)
		results = append(results, map[string]string{"id": id, "status": "deleted"})
	}

	s.cache.Invalidate("model-cache-stats")
	writeJSON(w, http.StatusOK, map[string]interface{}{"results": results})
}

func (s *Server) handleRegisterCustomModel(w http.ResponseWriter, r *http.Request) {
	var req database.RegisterCustomModelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.S3URI == "" || req.DisplayName == "" {
		writeError(w, http.StatusBadRequest, "s3_uri and display_name are required")
		return
	}
	if !strings.HasPrefix(req.S3URI, "s3://") {
		writeError(w, http.StatusBadRequest, "s3_uri must start with s3://")
		return
	}

	now := time.Now()
	mc := &database.ModelCache{
		S3URI:       req.S3URI,
		DisplayName: req.DisplayName,
		HfRevision:  "main",
		Status:      "cached",
		CachedAt:    &now,
	}

	id, err := s.repo.CreateModelCache(r.Context(), mc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("register model: %v", err))
		return
	}

	mc.ID = id
	s.cache.Invalidate("model-cache-stats")
	writeJSON(w, http.StatusCreated, mc)
}

func (s *Server) watchCacheJob(cacheID, jobName string) {
	ctx := context.Background()
	deadline := time.Now().Add(cacheJobTTL)

	for time.Now().Before(deadline) {
		job, err := s.client.BatchV1().Jobs(cacheNamespace).Get(ctx, jobName, metav1.GetOptions{})
		if err != nil {
			// Job gone? That's expected after cancel/delete — stop
			// watching instead of looping until the 2h TTL. Only the
			// row owner decides the terminal status.
			if strings.Contains(err.Error(), "not found") {
				log.Printf("[cache %s] job %s gone, stopping watch", cacheID[:8], jobName)
				return
			}
			log.Printf("[cache %s] failed to get job: %v", cacheID[:8], err)
			time.Sleep(cacheJobPoll)
			continue
		}

		for _, cond := range job.Status.Conditions {
			if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
				sizeBytes := s.readCacheJobSize(ctx, jobName)
				if err := s.repo.UpdateModelCacheComplete(ctx, cacheID, sizeBytes); err != nil {
					log.Printf("[cache %s] failed to mark complete: %v", cacheID[:8], err)
				} else {
					log.Printf("[cache %s] caching complete, size=%d bytes", cacheID[:8], sizeBytes)
				}
				return
			}
			if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
				errMsg := fmt.Sprintf("cache job failed: %s", cond.Message)
				s.repo.UpdateModelCacheStatus(ctx, cacheID, "failed", &errMsg)
				log.Printf("[cache %s] %s", cacheID[:8], errMsg)
				return
			}
		}

		time.Sleep(cacheJobPoll)
	}

	errMsg := "cache job timed out after 2 hours"
	s.repo.UpdateModelCacheStatus(ctx, cacheID, "failed", &errMsg)
	log.Printf("[cache %s] %s", cacheID[:8], errMsg)
}

func (s *Server) readCacheJobSize(ctx context.Context, jobName string) int64 {
	pods, err := s.client.CoreV1().Pods(cacheNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("job-name=%s", jobName),
	})
	if err != nil || len(pods.Items) == 0 {
		return 0
	}

	req := s.client.CoreV1().Pods(cacheNamespace).GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{
		Container: "cache",
		TailLines: int64Ptr(5),
	})
	stream, err := req.Stream(ctx)
	if err != nil {
		return 0
	}
	defer stream.Close()

	data, _ := io.ReadAll(stream)
	re := regexp.MustCompile(`size_bytes=(\d+)`)
	matches := re.FindSubmatch(data)
	if len(matches) >= 2 {
		size, _ := strconv.ParseInt(string(matches[1]), 10, 64)
		return size
	}
	return 0
}

func (s *Server) deleteS3Prefix(s3URI string) {
	parts := strings.SplitN(strings.TrimPrefix(s3URI, "s3://"), "/", 2)
	if len(parts) != 2 {
		return
	}
	bucket := parts[0]
	prefix := parts[1]

	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Printf("[cache] failed to load AWS config for S3 delete: %v", err)
		return
	}

	client := s3.NewFromConfig(cfg)

	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: &bucket,
		Prefix: &prefix,
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			log.Printf("[cache] failed to list S3 objects: %v", err)
			return
		}
		if len(page.Contents) == 0 {
			break
		}

		var objects []types.ObjectIdentifier
		for _, obj := range page.Contents {
			objects = append(objects, types.ObjectIdentifier{Key: obj.Key})
		}

		_, err = client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: &bucket,
			Delete: &types.Delete{Objects: objects},
		})
		if err != nil {
			log.Printf("[cache] failed to delete S3 objects: %v", err)
			return
		}
	}

	log.Printf("[cache] deleted S3 prefix: %s", s3URI)
}

func (s *Server) applyJobYAML(ctx context.Context, ns, yamlStr string) error {
	decoder := k8syaml.NewYAMLOrJSONDecoder(io.NopCloser(strings.NewReader(yamlStr)), 4096)
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return fmt.Errorf("decode YAML: %w", err)
	}
	var job batchv1.Job
	if err := json.Unmarshal(raw, &job); err != nil {
		return fmt.Errorf("unmarshal job: %w", err)
	}
	_, err := s.client.BatchV1().Jobs(ns).Create(ctx, &job, metav1.CreateOptions{})
	return err
}

func modelPathFromHfID(hfID string) string {
	return strings.ReplaceAll(hfID, "/", "/")
}

func int64Ptr(v int64) *int64 {
	return &v
}
