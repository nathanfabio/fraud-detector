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

	debug.SetGCPercent(50)
	debug.SetMemoryLimit(140 * 1024 * 1024)

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

	var unixListener net.Listener
	if err := os.MkdirAll("/run/sock", 0777); err == nil {
		os.Remove(sockPath)
		unixListener, err = net.Listen("unix", sockPath)
		if err != nil {
			log.Printf("Unix socket: %v", err)
			unixListener = nil
		} else {
			os.Chmod(sockPath, 0666)
			log.Printf("Unix socket: %s", sockPath)
		}
	}

	port := "8080"
	if envPort := os.Getenv("PORT"); envPort != "" {
		port = envPort
	}

	if unixListener != nil {
		go func() {
			srv := &http.Server{
				Handler:           mux,
				ReadHeaderTimeout: 5e9,
				ReadTimeout:       10e9,
				WriteTimeout:      10e9,
				IdleTimeout:       30e9,
			}
			srv.Serve(unixListener)
		}()
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
		log.Printf("Index loaded: %d vectors, %d clusters", len(idx.Vectors), idx.NumClusters)
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
