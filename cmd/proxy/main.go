package main

import (
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
)

func main() {
	backendsEnv := os.Getenv("BACKENDS")
	if backendsEnv == "" {
		backendsEnv = "http://api1:8080,http://api2:8080"
	}
	backends := strings.Split(backendsEnv, ",")

	var counter atomic.Uint64

	transport := &http.Transport{
		MaxIdleConns:        512,
		MaxIdleConnsPerHost: 256,
		IdleConnTimeout:     30e9,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   250e6,
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, _ *http.Request) {
		for _, b := range backends {
			resp, err := client.Get(b + "/ready")
			if err != nil || resp.StatusCode >= 300 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			resp.Body.Close()
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	mux.HandleFunc("POST /fraud-score", func(w http.ResponseWriter, r *http.Request) {
		backend := backends[counter.Add(1)%uint64(len(backends))]

		body, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		reader := strings.NewReader(string(body))
		resp, err := client.Post(backend+"/fraud-score", "application/json", reader)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
	})

	srv := &http.Server{
		Handler:           mux,
		Addr:              ":80",
		ReadHeaderTimeout: 250e6,
		ReadTimeout:       250e6,
		WriteTimeout:      250e6,
		IdleTimeout:       30e9,
	}

	log.Println("Proxy listening on :80")
	log.Fatal(srv.ListenAndServe())
}
