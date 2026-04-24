package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/accelbench/accelbench/internal/api"
	"github.com/accelbench/accelbench/internal/database"
	"github.com/accelbench/accelbench/internal/secrets"

	"github.com/aws/aws-sdk-go-v2/config"
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

	// PRD-31: wire up AWS Secrets Manager for HF / Docker Hub credentials.
	// Non-fatal if construction fails — the credentials endpoints will
	// return 500 and auto-injection is skipped.
	if sm, err := secrets.New(ctx); err != nil {
		log.Printf("secrets manager unavailable: %v", err)
	} else {
		srv.SetSecretsStore(sm)
	}

	// PRD-33: EC2 client + dynamic K8s client for the Capacity Reservations
	// card. Non-fatal if the AWS config load fails — the reservations
	// endpoints will return 500 if called, but other handlers keep working.
	if awsCfg, err := config.LoadDefaultConfig(ctx); err != nil {
		log.Printf("aws config unavailable, capacity reservations disabled: %v", err)
	} else {
		srv.SetReservationsClients(ec2.NewFromConfig(awsCfg), dynClient)
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
