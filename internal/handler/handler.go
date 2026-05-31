package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"

	"fraud-detector/internal/config"
	"fraud-detector/internal/index"
	"fraud-detector/internal/vectorizer"
)

type FraudHandler struct {
	index     *index.IVFIndex
	normCfg   *config.Normalization
	mccRisk   map[string]float64
	ready     atomic.Bool
	semaphore chan struct{}
}

func New(cfg *config.Normalization, mccRisk map[string]float64, idx *index.IVFIndex) *FraudHandler {
	h := &FraudHandler{
		normCfg:   cfg,
		mccRisk:   mccRisk,
		index:     idx,
		semaphore: make(chan struct{}, 16),
	}
	if idx != nil {
		h.ready.Store(true)
	}
	return h
}

func (h *FraudHandler) SetIndex(idx *index.IVFIndex) {
	h.index = idx
}

func (h *FraudHandler) SetReady() {
	h.ready.Store(true)
}

func (h *FraudHandler) Ready(w http.ResponseWriter, _ *http.Request) {
	if h.ready.Load() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ready":true,"build":"v8-fdpass"}`))
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
}

func (h *FraudHandler) Score(w http.ResponseWriter, r *http.Request) {
	if !h.ready.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	select {
	case h.semaphore <- struct{}{}:
		defer func() { <-h.semaphore }()
	default:
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var req vectorizer.Request
	if err := json.Unmarshal(body, &req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	vec := vectorizer.Build(&req, h.normCfg, h.mccRisk)
	var idxVec index.Vector
	copy(idxVec[:], vec[:])
	fraudCount := h.index.Search(&idxVec)

	score := float64(fraudCount) / 5.0
	approved := score < 0.6

	resp := struct {
		Approved   bool    `json:"approved"`
		FraudScore float64 `json:"fraud_score"`
	}{approved, score}

	data, _ := json.Marshal(resp)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

func (h *FraudHandler) Warmup() {
	if h.index == nil {
		return
	}
	payloads := []string{
		`{"id":"w","transaction":{"amount":41.12,"installments":2,"requested_at":"2026-03-11T18:45:53Z"},"customer":{"avg_amount":82.24,"tx_count_24h":3,"known_merchants":["M3","M16"]},"merchant":{"id":"M16","mcc":"5411","avg_amount":60.25},"terminal":{"is_online":false,"card_present":true,"km_from_home":29.23},"last_transaction":null}`,
		`{"id":"x","transaction":{"amount":9505.97,"installments":10,"requested_at":"2026-03-14T05:15:12Z"},"customer":{"avg_amount":81.28,"tx_count_24h":20,"known_merchants":["M8","M7","M5"]},"merchant":{"id":"M68","mcc":"7802","avg_amount":54.86},"terminal":{"is_online":false,"card_present":true,"km_from_home":952.27},"last_transaction":null}`,
	}
	var req vectorizer.Request
	for round := 0; round < 16; round++ {
		for _, p := range payloads {
			json.Unmarshal([]byte(p), &req)
			vec := vectorizer.Build(&req, h.normCfg, h.mccRisk)
			var v index.Vector
			copy(v[:], vec[:])
			h.index.Search(&v)
		}
	}
}
