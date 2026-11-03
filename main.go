package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"campaign-events/internal/handlers"
	"campaign-events/internal/repository"
	"campaign-events/internal/services"
)

func main() {
	addr := ":8080"
	if port := os.Getenv("PORT"); port != "" {
		addr = ":" + port
	}

	repo := repository.NewMemoryRepository()
	svc := services.NewIngestService(repo)
	h := handlers.New(svc)

	mux := http.NewServeMux()
	h.Register(mux)

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("campaign-events listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
