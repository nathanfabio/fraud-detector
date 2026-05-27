package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"fraud-detector/internal/config"
	"fraud-detector/internal/handler"
	"fraud-detector/internal/search"
)

func main() {
	normCfg, err := config.LoadNormalization("resources/normalization.json")
	if err != nil {
		log.Fatalf("Failed to load normalization config: %v", err)
	}

	mccRisk, err := config.LoadMCCRisk("resources/mcc_risk.json")
	if err != nil {
		log.Fatalf("Failed to load MCC risk: %v", err)
	}

	refPath := "resources/references.json.gz"
	if envPath := os.Getenv("REFERENCES_PATH"); envPath != "" {
		refPath = envPath
	}

	log.Println("Loading reference dataset...")
	refData, err := search.LoadReferences(refPath)
	if err != nil {
		log.Fatalf("Failed to load references: %v", err)
	}
	log.Printf("Loaded %d reference vectors", refData.Count)

	h := handler.New(normCfg, mccRisk, refData)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ready", h.Ready)
	mux.HandleFunc("POST /fraud-score", h.Score)

	port := "8080"
	if envPort := os.Getenv("PORT"); envPort != "" {
		port = envPort
	}

	log.Printf("Starting server on port %s", port)
	if err := http.ListenAndServe(fmt.Sprintf(":%s", port), mux); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
