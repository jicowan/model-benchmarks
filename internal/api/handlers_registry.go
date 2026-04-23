package api

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
)

// registryResponse is the shape returned by GET /api/v1/config/registry.
type registryResponse struct {
	Enabled      bool                    `json:"enabled"`
	URI          string                  `json:"uri,omitempty"`
	Repositories []registryRepoSummary   `json:"repositories,omitempty"`
	HelmHint     string                  `json:"helm_hint,omitempty"`
}

type registryRepoSummary struct {
	Name        string     `json:"name"`
	SizeBytes   int64      `json:"size_bytes"`
	LastPulledAt *time.Time `json:"last_pulled_at,omitempty"`
}

// ecrClient is lazily initialized the first time the Registry card is loaded.
// Subsequent calls reuse the same client.
var (
	ecrClientOnce sync.Once
	ecrClient     *ecr.Client
	ecrClientErr  error
)

func getECRClient(ctx context.Context) (*ecr.Client, error) {
	ecrClientOnce.Do(func() {
		cfg, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			ecrClientErr = err
			return
		}
		ecrClient = ecr.NewFromConfig(cfg)
	})
	return ecrClient, ecrClientErr
}

// Simple 60s in-memory cache to avoid hammering ECR on page re-renders.
var (
	registryCacheMu       sync.Mutex
	registryCached        *registryResponse
	registryCachedAt      time.Time
	registryCacheLifetime = 60 * time.Second
)

func (s *Server) handleGetRegistry(w http.ResponseWriter, r *http.Request) {
	registryCacheMu.Lock()
	if registryCached != nil && time.Since(registryCachedAt) < registryCacheLifetime {
		cached := *registryCached
		registryCacheMu.Unlock()
		writeJSON(w, http.StatusOK, cached)
		return
	}
	registryCacheMu.Unlock()

	resp := registryResponse{}
	uri := strings.TrimSpace(os.Getenv("PULL_THROUGH_REGISTRY"))
	if uri == "" {
		resp.Enabled = false
		resp.HelmHint = "helm upgrade accelbench helm/accelbench -n accelbench --reuse-values " +
			"--set registry.pullThroughEnabled=true --set registry.pullThroughURI=<account>.dkr.ecr.<region>.amazonaws.com"
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp.Enabled = true
	resp.URI = uri

	client, err := getECRClient(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "aws config: "+err.Error())
		return
	}

	// Paginate through dockerhub/* repositories. Expect a small count.
	var repoNames []string
	var next *string
	for {
		out, err := client.DescribeRepositories(r.Context(), &ecr.DescribeRepositoriesInput{
			NextToken:  next,
			MaxResults: aws.Int32(100),
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "describe repositories: "+err.Error())
			return
		}
		for _, repo := range out.Repositories {
			if repo.RepositoryName == nil {
				continue
			}
			if strings.HasPrefix(*repo.RepositoryName, "dockerhub/") {
				repoNames = append(repoNames, *repo.RepositoryName)
			}
		}
		if out.NextToken == nil {
			break
		}
		next = out.NextToken
	}

	// Fetch images for each repo to compute size + last-pulled.
	for _, name := range repoNames {
		summary := registryRepoSummary{Name: name}
		images, err := client.DescribeImages(r.Context(), &ecr.DescribeImagesInput{
			RepositoryName: aws.String(name),
		})
		if err != nil {
			// Partial failure — log-and-skip rather than 500 the whole call.
			resp.Repositories = append(resp.Repositories, summary)
			continue
		}
		var total int64
		var latest *time.Time
		for _, img := range images.ImageDetails {
			if img.ImageSizeInBytes != nil {
				total += *img.ImageSizeInBytes
			}
			if img.LastRecordedPullTime != nil {
				t := *img.LastRecordedPullTime
				if latest == nil || t.After(*latest) {
					latest = &t
				}
			}
		}
		summary.SizeBytes = total
		summary.LastPulledAt = latest
		resp.Repositories = append(resp.Repositories, summary)
	}

	registryCacheMu.Lock()
	cp := resp
	registryCached = &cp
	registryCachedAt = time.Now()
	registryCacheMu.Unlock()

	writeJSON(w, http.StatusOK, resp)
}
