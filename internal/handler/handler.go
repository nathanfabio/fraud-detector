package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"sync/atomic"

	"fraud-detector/internal/config"
	"fraud-detector/internal/index"
	"fraud-detector/internal/vectorizer"
)

var bodyPool = sync.Pool{
	New: func() interface{} { return make([]byte, 4096) },
}

var ResponseTemplates = [6][]byte{
	[]byte(`{"approved":true,"fraud_score":0}`),
	[]byte(`{"approved":true,"fraud_score":0.2}`),
	[]byte(`{"approved":true,"fraud_score":0.4}`),
	[]byte(`{"approved":false,"fraud_score":0.6}`),
	[]byte(`{"approved":false,"fraud_score":0.8}`),
	[]byte(`{"approved":false,"fraud_score":1}`),
}

type FraudHandler struct {
	index   *index.IVFIndex
	normCfg *config.Normalization
	mccRisk map[string]float64
	ready   atomic.Bool
}

func New(cfg *config.Normalization, mccRisk map[string]float64, idx *index.IVFIndex) *FraudHandler {
	h := &FraudHandler{
		normCfg: cfg,
		mccRisk: mccRisk,
		index:   idx,
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

func (h *FraudHandler) IsReady() bool {
	return h.ready.Load()
}

func (h *FraudHandler) Process(body []byte) int {
	vec := vectorizer.ParseAndBuild(body, h.normCfg)
	var idxVec index.Vector
	copy(idxVec[:], vec[:])
	return h.index.Search(&idxVec)
}

func (h *FraudHandler) Ready(w http.ResponseWriter, _ *http.Request) {
	if h.ready.Load() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ready":true,"build":"v12-opt"}`))
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
}

func (h *FraudHandler) Score(w http.ResponseWriter, r *http.Request) {
	if !h.ready.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	buf := bodyPool.Get().([]byte)
	cbuf := buf[:cap(buf)]
	n, err := io.ReadFull(r.Body, cbuf)
	r.Body.Close()
	if n == 0 || (err != nil && err != io.ErrUnexpectedEOF && err != io.EOF) {
		bodyPool.Put(buf)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	body := cbuf[:n]

	fraudCount := h.Process(body)
	bodyPool.Put(buf)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(ResponseTemplates[fraudCount])
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
			if err := json.Unmarshal([]byte(p), &req); err != nil {
				continue
			}
			vec := vectorizer.Build(&req, h.normCfg, h.mccRisk)
			var v index.Vector
			copy(v[:], vec[:])
			h.index.Search(&v)
		}
	}
}
