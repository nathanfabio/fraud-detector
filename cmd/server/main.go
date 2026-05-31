package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"runtime/debug"

	"fraud-detector/internal/config"
	"fraud-detector/internal/handler"
	"fraud-detector/internal/index"
	"fraud-detector/internal/server"
)

func main() {
	buildIndexIn := flag.String("build-index-in", "", "JSON.GZ to build index from")
	buildIndexOut := flag.String("build-index-out", "", "Output path for built index")
	flag.Parse()

	if *buildIndexIn != "" && *buildIndexOut != "" {
		log.Printf("Building IVF index: %s -> %s", *buildIndexIn, *buildIndexOut)
		if err := index.BuildIndex(*buildIndexIn, *buildIndexOut); err != nil {
			log.Fatalf("Build failed: %v", err)
		}
		log.Println("Index built successfully")
		return
	}

	debug.SetGCPercent(-1)
	debug.SetMemoryLimit(155 * 1024 * 1024)

	normCfg, err := config.LoadNormalization("resources/normalization.json")
	if err != nil {
		log.Fatalf("Failed to load normalization: %v", err)
	}
	mccRisk, err := config.LoadMCCRisk("resources/mcc_risk.json")
	if err != nil {
		log.Fatalf("Failed to load MCC risk: %v", err)
	}

	h := handler.New(normCfg, mccRisk, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ready", h.Ready)
	mux.HandleFunc("POST /fraud-score", h.Score)

	hostname, _ := os.Hostname()
	sockPath := fmt.Sprintf("/run/sock/%s.sock", hostname)

	os.MkdirAll("/run/sock", 0777)
	os.Remove(sockPath)
	fdLn, err := server.ListenFD(sockPath)
	if err != nil {
		log.Printf("FD listener: %v (falling back to TCP-only)", err)
	} else {
		log.Printf("FD listener: %s", sockPath)
		go func() {
			srv := &http.Server{
				Handler:           mux,
				ReadHeaderTimeout: 5e9,
				ReadTimeout:       10e9,
				WriteTimeout:      10e9,
				IdleTimeout:       30e9,
			}
			srv.Serve(fdLn)
		}()
	}

	port := "8080"
	if envPort := os.Getenv("PORT"); envPort != "" {
		port = envPort
	}

	go func() {
		indexPath := "resources/index.bin"
		if envPath := os.Getenv("INDEX_PATH"); envPath != "" {
			indexPath = envPath
		}
		log.Printf("Loading index from %s...", indexPath)
		idx, err := index.LoadBinary(indexPath)
		if err != nil {
			log.Fatalf("Failed to load index: %v", err)
		}
		totalVecs := 0
		for _, p := range idx.Parts {
			if p != nil {
				totalVecs += len(p.Vectors)
			}
		}
		log.Printf("Index loaded: %d vectors across %d partitions", totalVecs, len(idx.Parts))
		h.SetIndex(idx)
		h.Warmup()
		log.Println("Warmup complete")
		h.SetReady()
	}()

	tcpListener, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		log.Fatalf("TCP listen failed: %v", err)
	}

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5e9,
		ReadTimeout:       10e9,
		WriteTimeout:      10e9,
		IdleTimeout:       30e9,
	}
	log.Printf("Server on TCP :%s", port)
	if err := srv.Serve(tcpListener); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
