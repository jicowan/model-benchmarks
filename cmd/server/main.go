package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/accelbench/accelbench/internal/api"
	"github.com/accelbench/accelbench/internal/database"

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

	srv := api.NewServer(repo, k8sClient)

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
