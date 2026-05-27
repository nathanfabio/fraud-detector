package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"fraud-detector/internal/config"
	"fraud-detector/internal/search"
	"fraud-detector/internal/vectorizer"
)

type FraudScore float64

func (f FraudScore) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("%.1f", f)), nil
}

type Response struct {
	Approved   bool       `json:"approved"`
	FraudScore FraudScore `json:"fraud_score"`
}

type FraudHandler struct {
	Config  *config.Normalization
	MCCRisk map[string]float64
	RefData *search.ReferenceData
}

func New(cfg *config.Normalization, mccRisk map[string]float64, refData *search.ReferenceData) *FraudHandler {
	return &FraudHandler{
		Config:  cfg,
		MCCRisk: mccRisk,
		RefData: refData,
	}
}

func (h *FraudHandler) Ready(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func (h *FraudHandler) Score(w http.ResponseWriter, r *http.Request) {
	var req vectorizer.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	vec := vectorizer.Build(&req, h.Config, h.MCCRisk)
	fraudCount, total := h.RefData.FindNearest(vec)

	score := FraudScore(float64(fraudCount) / float64(total))
	approved := float64(score) < 0.6

	resp := Response{
		Approved:   approved,
		FraudScore: score,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}
