package config

import (
	"os"
	"testing"
)

func TestLoadNormalization(t *testing.T) {
	path := "test_normalization.json"
	data := `{"max_amount":10000,"max_installments":12,"amount_vs_avg_ratio":10,"max_minutes":1440,"max_km":1000,"max_tx_count_24h":20,"max_merchant_avg_amount":10000}`
	os.WriteFile(path, []byte(data), 0644)
	defer os.Remove(path)

	cfg, err := LoadNormalization(path)
	if err != nil {
		t.Fatalf("LoadNormalization failed: %v", err)
	}
	if cfg.MaxAmount != 10000 {
		t.Errorf("MaxAmount = %f, want 10000", cfg.MaxAmount)
	}
	if cfg.MaxInstallments != 12 {
		t.Errorf("MaxInstallments = %f, want 12", cfg.MaxInstallments)
	}
	if cfg.AmountVsAvgRatio != 10 {
		t.Errorf("AmountVsAvgRatio = %f, want 10", cfg.AmountVsAvgRatio)
	}
	if cfg.MaxMinutes != 1440 {
		t.Errorf("MaxMinutes = %f, want 1440", cfg.MaxMinutes)
	}
	if cfg.MaxKm != 1000 {
		t.Errorf("MaxKm = %f, want 1000", cfg.MaxKm)
	}
	if cfg.MaxTxCount24h != 20 {
		t.Errorf("MaxTxCount24h = %f, want 20", cfg.MaxTxCount24h)
	}
	if cfg.MaxMerchantAvgAmount != 10000 {
		t.Errorf("MaxMerchantAvgAmount = %f, want 10000", cfg.MaxMerchantAvgAmount)
	}
}

func TestLoadNormalizationNotFound(t *testing.T) {
	_, err := LoadNormalization("/nonexistent/path.json")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestLoadNormalizationInvalidJSON(t *testing.T) {
	path := "test_invalid.json"
	os.WriteFile(path, []byte(`not json`), 0644)
	defer os.Remove(path)

	_, err := LoadNormalization(path)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestLoadMCCRisk(t *testing.T) {
	path := "test_mcc_risk.json"
	data := `{"5411":0.15,"7801":0.80,"7995":0.85}`
	os.WriteFile(path, []byte(data), 0644)
	defer os.Remove(path)

	mccRisk, err := LoadMCCRisk(path)
	if err != nil {
		t.Fatalf("LoadMCCRisk failed: %v", err)
	}
	if mccRisk["5411"] != 0.15 {
		t.Errorf("5411 = %f, want 0.15", mccRisk["5411"])
	}
	if mccRisk["7801"] != 0.80 {
		t.Errorf("7801 = %f, want 0.80", mccRisk["7801"])
	}
	if mccRisk["7995"] != 0.85 {
		t.Errorf("7995 = %f, want 0.85", mccRisk["7995"])
	}
}
