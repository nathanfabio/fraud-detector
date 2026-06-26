package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"fraud-detector/internal/config"
	"fraud-detector/internal/index"
)

func testConfig() *config.Normalization {
	return &config.Normalization{
		MaxAmount:            10000,
		MaxInstallments:      12,
		AmountVsAvgRatio:     10,
		MaxMinutes:           1440,
		MaxKm:                1000,
		MaxTxCount24h:        20,
		MaxMerchantAvgAmount: 10000,
	}
}

func TestNew(t *testing.T) {
	cfg := testConfig()
	h := New(cfg, nil, nil)
	if h == nil {
		t.Fatal("New returned nil")
	}
	if h.IsReady() {
		t.Error("handler without index should not be ready")
	}
}

func TestNewWithIndex(t *testing.T) {
	cfg := testConfig()
	idx := &index.IVFIndex{}
	h := New(cfg, nil, idx)
	if h == nil {
		t.Fatal("New returned nil")
	}
	if !h.IsReady() {
		t.Error("handler with index should be ready")
	}
}

func TestSetReady(t *testing.T) {
	cfg := testConfig()
	h := New(cfg, nil, nil)
	if h.IsReady() {
		t.Error("handler should not be ready initially")
	}
	h.SetReady()
	if !h.IsReady() {
		t.Error("handler should be ready after SetReady")
	}
}

func TestWarmupNilIndex(t *testing.T) {
	cfg := testConfig()
	h := New(cfg, nil, nil)
	h.Warmup()
}

func TestReadyHandler(t *testing.T) {
	cfg := testConfig()
	h := New(cfg, nil, nil)

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()
	h.Ready(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("ready without index = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	h.SetReady()
	w2 := httptest.NewRecorder()
	h.Ready(w2, req)

	if w2.Code != http.StatusOK {
		t.Errorf("ready with index = %d, want %d", w2.Code, http.StatusOK)
	}
}

func TestScoreNotReady(t *testing.T) {
	cfg := testConfig()
	h := New(cfg, nil, nil)

	req := httptest.NewRequest("POST", "/fraud-score", nil)
	w := httptest.NewRecorder()
	h.Score(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("score not ready = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestResponseTemplates(t *testing.T) {
	if len(ResponseTemplates) != 6 {
		t.Errorf("ResponseTemplates length = %d, want 6", len(ResponseTemplates))
	}
	for i, tmpl := range ResponseTemplates {
		if len(tmpl) == 0 {
			t.Errorf("ResponseTemplates[%d] is empty", i)
		}
	}
}
