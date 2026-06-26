package vectorizer

import (
	"testing"

	"fraud-detector/internal/config"
)

func TestQuantize(t *testing.T) {
	tests := []struct {
		name     string
		input    float64
		expected int16
	}{
		{"zero", 0, 0},
		{"one", 1.0, 10000},
		{"half", 0.5, 5000},
		{"negative_one", -1, -1},
		{"negative_clamped", -0.5, 0},
		{"over_one_clamped", 1.5, 10000},
		{"small_fraction", 0.0001, 1},
		{"round_up", 0.50005, 5001},
		{"round_down", 0.49994, 4999},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := quantize(tt.input)
			if got != tt.expected {
				t.Errorf("quantize(%v) = %d, want %d", tt.input, got, tt.expected)
			}
		})
	}
}

func TestEuclideanDistSq(t *testing.T) {
	a := Vec14{1000, 2000, 0, 5000, 3000, -1, -1, 2000, 1000, 0, 10000, 0, 5000, 0}
	b := Vec14{1000, 2000, 0, 5000, 3000, -1, -1, 2000, 1000, 0, 10000, 0, 5000, 0}
	got := EuclideanDistSq(a, b)
	if got != 0 {
		t.Errorf("EuclideanDistSq of identical vectors = %d, want 0", got)
	}

	c := Vec14{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	got = EuclideanDistSq(a, c)

	sumSq := int32(0)
	for i := 0; i < 14; i++ {
		d := int32(a[i]) - int32(c[i])
		sumSq += d * d
	}
	if got != sumSq {
		t.Errorf("EuclideanDistSq = %d, want %d", got, sumSq)
	}
}

func TestIsUnknownMerchant(t *testing.T) {
	known := []string{"M1", "M2", "M3"}

	if isUnknownMerchant("M1", known) {
		t.Error("M1 should be known")
	}
	if !isUnknownMerchant("M4", known) {
		t.Error("M4 should be unknown")
	}
	if !isUnknownMerchant("", known) {
		t.Error("empty should be unknown")
	}
	if len(known) > 0 {
		if !isUnknownMerchant("M1", nil) {
			t.Error("nil known list: M1 should be unknown")
		}
	}
}

func TestBuild(t *testing.T) {
	cfg := &config.Normalization{
		MaxAmount:            10000,
		MaxInstallments:      12,
		AmountVsAvgRatio:     10,
		MaxMinutes:           1440,
		MaxKm:                1000,
		MaxTxCount24h:        20,
		MaxMerchantAvgAmount: 10000,
	}
	mccRisk := map[string]float64{
		"5411": 0.15,
		"7995": 0.85,
	}

	req := &Request{
		ID: "tx-123",
		TransactionData: Transaction{
			Amount:       1000,
			Installments: 3,
			RequestedAt:  "2026-03-11T18:45:53Z",
		},
		Customer: Customer{
			AvgAmount:      2000,
			TxCount24h:     5,
			KnownMerchants: []string{"M1", "M2"},
		},
		Merchant: Merchant{
			ID:        "M2",
			MCC:       "5411",
			AvgAmount: 500,
		},
		Terminal: Terminal{
			IsOnline:    true,
			CardPresent: false,
			KmFromHome:  50,
		},
		LastTransaction: &LastTransaction{
			Timestamp:     "2026-03-11T18:30:00Z",
			KmFromCurrent: 10,
		},
	}

	vec := Build(req, cfg, mccRisk)

	if vec[0] != quantize(1000.0/10000) {
		t.Errorf("vec[0] = %d, want %d", vec[0], quantize(1000.0/10000))
	}

	if vec[3] < 0 || vec[3] > 10000 {
		t.Errorf("vec[3] (hour) out of range: %d", vec[3])
	}

	if vec[4] < 0 || vec[4] > 10000 {
		t.Errorf("vec[4] (dow) out of range: %d", vec[4])
	}

	if vec[9] != 10000 {
		t.Errorf("vec[9] (is_online=true) = %d, want 10000", vec[9])
	}

	if vec[10] != 0 {
		t.Errorf("vec[10] (card_present=false) = %d, want 0", vec[10])
	}

	if vec[11] != 0 {
		t.Errorf("vec[11] (known merchant) = %d, want 0", vec[11])
	}

	expectedRisk := int16(0.15 * 10000)
	if vec[12] != expectedRisk {
		t.Errorf("vec[12] (mcc risk) = %d, want %d", vec[12], expectedRisk)
	}

	if mccRisk[req.Merchant.MCC] != 0.15 {
		t.Errorf("MCC risk lookup mismatch")
	}
}

func TestBuildNoLastTransaction(t *testing.T) {
	cfg := &config.Normalization{
		MaxAmount:            10000,
		MaxInstallments:      12,
		AmountVsAvgRatio:     10,
		MaxMinutes:           1440,
		MaxKm:                1000,
		MaxTxCount24h:        20,
		MaxMerchantAvgAmount: 10000,
	}

	req := &Request{
		TransactionData: Transaction{
			Amount:       500,
			Installments: 1,
			RequestedAt:  "2026-06-15T12:00:00Z",
		},
		Customer: Customer{
			AvgAmount:      1000,
			TxCount24h:     2,
			KnownMerchants: nil,
		},
		Merchant: Merchant{
			ID:        "M99",
			MCC:       "9999",
			AvgAmount: 200,
		},
		Terminal: Terminal{
			IsOnline:    false,
			CardPresent: true,
			KmFromHome:  100,
		},
	}

	vec := Build(req, cfg, map[string]float64{})

	if vec[5] != -1 {
		t.Errorf("vec[5] (no last tx) = %d, want -1", vec[5])
	}
	if vec[6] != -1 {
		t.Errorf("vec[6] (no last tx) = %d, want -1", vec[6])
	}
	if vec[10] != 10000 {
		t.Errorf("vec[10] (card_present=true) = %d, want 10000", vec[10])
	}
	if vec[11] != 10000 {
		t.Errorf("vec[11] (unknown merchant) = %d, want 10000", vec[11])
	}
}

func TestBuildUnknownMCC(t *testing.T) {
	cfg := &config.Normalization{
		MaxAmount:            10000,
		MaxInstallments:      12,
		AmountVsAvgRatio:     10,
		MaxMinutes:           1440,
		MaxKm:                1000,
		MaxTxCount24h:        20,
		MaxMerchantAvgAmount: 10000,
	}

	req := &Request{
		TransactionData: Transaction{
			Amount:       100,
			Installments: 1,
			RequestedAt:  "2026-01-01T00:00:00Z",
		},
		Customer: Customer{
			AvgAmount:  500,
			TxCount24h: 1,
		},
		Merchant: Merchant{
			ID:        "M1",
			MCC:       "0000",
			AvgAmount: 100,
		},
		Terminal: Terminal{
			IsOnline:    false,
			CardPresent: false,
			KmFromHome:  0,
		},
	}

	vec := Build(req, cfg, map[string]float64{})

	expectedRisk := int16(0.5 * 10000)
	if vec[12] != expectedRisk {
		t.Errorf("vec[12] (unknown mcc) = %d, want %d", vec[12], expectedRisk)
	}
}
