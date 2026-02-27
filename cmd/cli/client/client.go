package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/accelbench/accelbench/internal/database"
)

// Client wraps HTTP calls to the AccelBench API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// New creates a Client targeting the given base URL (e.g. "http://localhost:8080").
func New(baseURL string) *Client {
	return &Client{
		baseURL:    baseURL,
		httpClient: http.DefaultClient,
	}
}

// ListCatalog queries GET /api/v1/catalog with optional filters.
func (c *Client) ListCatalog(ctx context.Context, f database.CatalogFilter) ([]database.CatalogEntry, error) {
	params := url.Values{}
	if f.ModelHfID != "" {
		params.Set("model", f.ModelHfID)
	}
	if f.ModelFamily != "" {
		params.Set("model_family", f.ModelFamily)
	}
	if f.InstanceFamily != "" {
		params.Set("instance_family", f.InstanceFamily)
	}
	if f.AcceleratorType != "" {
		params.Set("accelerator_type", f.AcceleratorType)
	}
	if f.SortBy != "" {
		params.Set("sort", f.SortBy)
		if f.SortDesc {
			params.Set("order", "desc")
		}
	}
	if f.Limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", f.Limit))
	}
	if f.Offset > 0 {
		params.Set("offset", fmt.Sprintf("%d", f.Offset))
	}

	u := c.baseURL + "/api/v1/catalog"
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	var entries []database.CatalogEntry
	if err := c.doGet(ctx, u, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// CreateRun submits POST /api/v1/runs and returns the run ID and status.
func (c *Client) CreateRun(ctx context.Context, req database.RunRequest) (string, string, error) {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/runs", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		return "", "", c.readError(resp)
	}

	var result struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", fmt.Errorf("decode response: %w", err)
	}
	return result.ID, result.Status, nil
}

// GetRun fetches GET /api/v1/runs/{id}.
func (c *Client) GetRun(ctx context.Context, id string) (*database.BenchmarkRun, error) {
	var run database.BenchmarkRun
	if err := c.doGet(ctx, c.baseURL+"/api/v1/runs/"+id, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

// GetMetrics fetches GET /api/v1/runs/{id}/metrics.
func (c *Client) GetMetrics(ctx context.Context, id string) (*database.BenchmarkMetrics, error) {
	var m database.BenchmarkMetrics
	if err := c.doGet(ctx, c.baseURL+"/api/v1/runs/"+id+"/metrics", &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (c *Client) doGet(ctx context.Context, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.readError(resp)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) readError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	var apiErr struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &apiErr) == nil && apiErr.Error != "" {
		return fmt.Errorf("API error %d: %s", resp.StatusCode, apiErr.Error)
	}
	return fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
}
