package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/accelbench/accelbench/internal/api"
	"github.com/accelbench/accelbench/internal/auth"
	"github.com/accelbench/accelbench/internal/cache"
	"github.com/accelbench/accelbench/internal/database"
	"github.com/accelbench/accelbench/internal/secrets"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/ec2"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	ctx := context.Background()

	repo, err := database.NewRepository(ctx, dbURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer repo.Close()

	k8sCfg, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("load in-cluster config: %v", err)
	}
	k8sClient, err := kubernetes.NewForConfig(k8sCfg)
	if err != nil {
		log.Fatalf("create kubernetes client: %v", err)
	}
	dynClient, err := dynamic.NewForConfig(k8sCfg)
	if err != nil {
		log.Fatalf("create dynamic kubernetes client: %v", err)
	}

	// PRD-40: pod identity for replica coordination. os.Hostname() returns
	// the pod's name inside Kubernetes — stable for the pod's lifetime and
	// unique across replicas.
	hostname, err := os.Hostname()
	if err != nil {
		log.Fatalf("get hostname: %v", err)
	}
	log.Printf("api pod hostname = %s", hostname)

	srv := api.NewServer(repo, k8sClient, hostname)

	// PRD-38: shared response cache for slow-changing read endpoints.
	// 60-second TTL; mutation handlers invalidate their keys explicitly.
	responseCache := cache.New(60 * time.Second)
	defer responseCache.Stop()
	srv.SetCache(responseCache)

	// PRD-31: wire up AWS Secrets Manager for HF / Docker Hub credentials.
	// Non-fatal if construction fails — the credentials endpoints will
	// return 500 and auto-injection is skipped.
	if sm, err := secrets.New(ctx); err != nil {
		log.Printf("secrets manager unavailable: %v", err)
	} else {
		srv.SetSecretsStore(sm)
	}

	// Load AWS SDK config once and reuse it across SDK clients. Non-fatal
	// if this fails — individual features degrade gracefully.
	awsCfg, awsCfgErr := config.LoadDefaultConfig(ctx)
	if awsCfgErr != nil {
		log.Printf("aws config unavailable: %v", awsCfgErr)
	}

	// PRD-33: EC2 client + dynamic K8s client for the Capacity Reservations
	// card. If AWS config failed the reservations endpoints will return 500
	// but other handlers keep working.
	if awsCfgErr == nil {
		srv.SetReservationsClients(ec2.NewFromConfig(awsCfg), dynClient)
	}

	// PRD-43: Cognito auth. Needs COGNITO_USER_POOL_ID + COGNITO_CLIENT_ID.
	// AUTH_DISABLED=1 bypasses auth entirely (local dev + CI). Without the
	// IDs, we force Disabled=true and log loudly so operators notice.
	authCfg := auth.Config{
		UserPoolID: os.Getenv("COGNITO_USER_POOL_ID"),
		ClientID:   os.Getenv("COGNITO_CLIENT_ID"),
		Region:     os.Getenv("AWS_REGION"),
		Disabled:   os.Getenv("AUTH_DISABLED") == "1",
	}
	if authCfg.Region == "" {
		authCfg.Region = "us-east-2"
	}
	if !authCfg.Disabled && (authCfg.UserPoolID == "" || authCfg.ClientID == "") {
		log.Printf("warning: COGNITO_USER_POOL_ID / COGNITO_CLIENT_ID unset; forcing AUTH_DISABLED")
		authCfg.Disabled = true
	}
	if authCfg.Disabled {
		log.Printf("AUTH DISABLED — DO NOT USE IN PRODUCTION")
		srv.SetAuth(authCfg, nil, nil)
	} else {
		var idp api.CognitoIDP
		var verifier *auth.Verifier
		if awsCfgErr == nil {
			idp = cognitoidentityprovider.NewFromConfig(awsCfg)
			verifier = auth.NewVerifier(authCfg, auth.NewJWKSFetcher(authCfg))
		} else {
			log.Printf("warning: aws config unavailable; auth middleware will reject every request")
		}
		srv.SetAuth(authCfg, idp, verifier)
	}

	// PRD-40: heartbeat + ownership-aware orphan recovery. Replaces the old
	// startup-only "mark everything running as failed" recovery path, which
	// used to false-positive during rolling deploys.
	srv.Orchestrator().StartHeartbeatLoop(ctx)
	srv.Orchestrator().StartOrphanRecoveryLoop(ctx)

	// PRD-37: materialized Catalog view. Runs a synchronous refresh
	// before the listener starts so the first Catalog request doesn't
	// hit an empty view, then ticks every 5 minutes in the background.
	StartCatalogRefreshLoop(ctx, repo)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})
	srv.RegisterRoutes(mux)

	log.Printf("AccelBench API server starting on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
