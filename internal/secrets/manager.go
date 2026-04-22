// Package secrets wraps AWS Secrets Manager for AccelBench's platform
// credentials (PRD-31).
//
// Two secrets are managed:
//   - accelbench/config/hf-token      — { "token": "hf_..." }
//   - ecr-pullthroughcache/dockerhub  — { "username": "...", "accessToken": "..." }
//
// The Manager creates secrets on first PUT if they don't exist, mirrors the
// "describe-only" read surface the /api/config/credentials GET endpoint needs,
// and exposes a GetHFToken helper used by the orchestrator / cache / seeder
// for auto-injection.
package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

const (
	HFSecretID        = "accelbench/config/hf-token"
	DockerHubSecretID = "ecr-pullthroughcache/dockerhub"
)

// Manager is the concrete implementation backed by Secrets Manager.
type Manager struct {
	client *secretsmanager.Client
}

// New returns a Manager configured from the ambient AWS environment
// (pod identity in cluster, shared config locally).
func New(ctx context.Context) (*Manager, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	return &Manager{client: secretsmanager.NewFromConfig(cfg)}, nil
}

// Metadata is the describe-only view used by GET /api/config/credentials.
type Metadata struct {
	Set       bool       `json:"set"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

// DockerHubValue is the JSON payload stored at DockerHubSecretID.
type DockerHubValue struct {
	Username    string `json:"username"`
	AccessToken string `json:"accessToken"`
}

// HFTokenValue is the JSON payload stored at HFSecretID.
type HFTokenValue struct {
	Token string `json:"token"`
}

// Describe returns only whether the secret exists and when it was last changed.
// Never returns the secret value.
func (m *Manager) Describe(ctx context.Context, id string) (Metadata, error) {
	out, err := m.client.DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{
		SecretId: aws.String(id),
	})
	if err != nil {
		var nf *smtypes.ResourceNotFoundException
		if errors.As(err, &nf) {
			return Metadata{Set: false}, nil
		}
		return Metadata{}, fmt.Errorf("describe %s: %w", id, err)
	}
	md := Metadata{Set: true}
	// Prefer LastChangedDate (tracks PutSecretValue); fall back to CreatedDate.
	switch {
	case out.LastChangedDate != nil:
		t := *out.LastChangedDate
		md.UpdatedAt = &t
	case out.CreatedDate != nil:
		t := *out.CreatedDate
		md.UpdatedAt = &t
	}
	return md, nil
}

// GetHFToken returns the stored HF token, or "" if the secret doesn't exist
// or is malformed. Callers treat "" as "no platform token configured" and
// proceed without injection.
func (m *Manager) GetHFToken(ctx context.Context) (string, error) {
	raw, err := m.getSecretString(ctx, HFSecretID)
	if err != nil || raw == "" {
		return "", err
	}
	var v HFTokenValue
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return "", fmt.Errorf("hf-token secret malformed: %w", err)
	}
	return v.Token, nil
}

// PutHFToken stores a new HF token. Creates the secret on first use.
func (m *Manager) PutHFToken(ctx context.Context, token string) error {
	payload, err := json.Marshal(HFTokenValue{Token: token})
	if err != nil {
		return err
	}
	return m.putSecretString(ctx, HFSecretID, string(payload),
		"AccelBench platform HuggingFace token (PRD-31)")
}

// DeleteHFToken removes the secret (no recovery window for simplicity —
// operators can re-PUT immediately to restore service).
func (m *Manager) DeleteHFToken(ctx context.Context) error {
	_, err := m.client.DeleteSecret(ctx, &secretsmanager.DeleteSecretInput{
		SecretId:                   aws.String(HFSecretID),
		ForceDeleteWithoutRecovery: aws.Bool(true),
	})
	if err != nil {
		var nf *smtypes.ResourceNotFoundException
		if errors.As(err, &nf) {
			return nil // already gone — treat as success
		}
		return fmt.Errorf("delete %s: %w", HFSecretID, err)
	}
	return nil
}

// PutDockerHub stores (username, accessToken). Creates the secret if missing.
// Note PRD-29 already created this secret via terraform; PutDockerHub is the
// rotation path from the Configuration page UI.
func (m *Manager) PutDockerHub(ctx context.Context, username, accessToken string) error {
	payload, err := json.Marshal(DockerHubValue{Username: username, AccessToken: accessToken})
	if err != nil {
		return err
	}
	return m.putSecretString(ctx, DockerHubSecretID, string(payload),
		"Docker Hub credentials consumed by the ECR pull-through cache")
}

// --- internals ---------------------------------------------------------------

func (m *Manager) getSecretString(ctx context.Context, id string) (string, error) {
	out, err := m.client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(id),
	})
	if err != nil {
		var nf *smtypes.ResourceNotFoundException
		if errors.As(err, &nf) {
			return "", nil
		}
		return "", fmt.Errorf("get %s: %w", id, err)
	}
	if out.SecretString == nil {
		return "", nil
	}
	return *out.SecretString, nil
}

// putSecretString performs PutSecretValue, or CreateSecret on first use.
// Idempotent across repeated calls (same effective behavior).
func (m *Manager) putSecretString(ctx context.Context, id, payload, description string) error {
	_, err := m.client.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{
		SecretId:     aws.String(id),
		SecretString: aws.String(payload),
	})
	if err == nil {
		return nil
	}
	var nf *smtypes.ResourceNotFoundException
	if !errors.As(err, &nf) {
		return fmt.Errorf("put %s: %w", id, err)
	}
	// First-time set: create the secret with the value.
	_, err = m.client.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         aws.String(id),
		Description:  aws.String(description),
		SecretString: aws.String(payload),
	})
	if err != nil {
		return fmt.Errorf("create %s: %w", id, err)
	}
	return nil
}
