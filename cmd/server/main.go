package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"fraud-detector/internal/config"
	"fraud-detector/internal/handler"
	"fraud-detector/internal/search"
)

func main() {
	preprocInput := flag.String("preproc-in", "", "Input JSON.GZ file for preprocessing")
	preprocOutput := flag.String("preproc-out", "", "Output binary file from preprocessing")
	flag.Parse()

	if *preprocInput != "" && *preprocOutput != "" {
		log.Printf("Preprocessing %s -> %s", *preprocInput, *preprocOutput)
		if err := search.Preprocess(*preprocInput, *preprocOutput); err != nil {
			log.Fatalf("Preprocess failed: %v", err)
		}
		log.Println("Preprocessing complete")
		return
	}

	normCfg, err := config.LoadNormalization("resources/normalization.json")
	if err != nil {
		log.Fatalf("Failed to load normalization config: %v", err)
	}

	mccRisk, err := config.LoadMCCRisk("resources/mcc_risk.json")
	if err != nil {
		log.Fatalf("Failed to load MCC risk: %v", err)
	}

	h := handler.New(normCfg, mccRisk, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ready", h.Ready)
	mux.HandleFunc("POST /fraud-score", h.Score)

	port := "8080"
	if envPort := os.Getenv("PORT"); envPort != "" {
		port = envPort
	}

	go func() {
		refPath := "resources/references.bin"
		if envPath := os.Getenv("REFERENCES_PATH"); envPath != "" {
			refPath = envPath
		}
		log.Printf("Loading reference dataset from %s...", refPath)
		refData, err := search.LoadBinary(refPath)
		if err != nil {
			log.Fatalf("Failed to load references: %v", err)
		}
		log.Printf("Loaded %d reference vectors", refData.Total())
		h.SetReady(refData)
	}()

	log.Printf("Starting server on port %s", port)
	if err := http.ListenAndServe(fmt.Sprintf(":%s", port), mux); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
