package vectorizer

import (
	"time"

	"fraud-detector/internal/config"
)

type Request struct {
	ID              string           `json:"id"`
	TransactionData Transaction      `json:"transaction"`
	Customer        Customer         `json:"customer"`
	Merchant        Merchant         `json:"merchant"`
	Terminal        Terminal         `json:"terminal"`
	LastTransaction *LastTransaction `json:"last_transaction"`
}

type Transaction struct {
	Amount       float64 `json:"amount"`
	Installments int     `json:"installments"`
	RequestedAt  string  `json:"requested_at"`
}

type Customer struct {
	AvgAmount      float64  `json:"avg_amount"`
	TxCount24h     int      `json:"tx_count_24h"`
	KnownMerchants []string `json:"known_merchants"`
}

type Merchant struct {
	ID        string  `json:"id"`
	MCC       string  `json:"mcc"`
	AvgAmount float64 `json:"avg_amount"`
}

type Terminal struct {
	IsOnline    bool    `json:"is_online"`
	CardPresent bool    `json:"card_present"`
	KmFromHome  float64 `json:"km_from_home"`
}

type LastTransaction struct {
	Timestamp     string  `json:"timestamp"`
	KmFromCurrent float64 `json:"km_from_current"`
}

func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func Build(req *Request, cfg *config.Normalization, mccRisk map[string]float64) []float64 {
	vec := make([]float64, 14)

	requestedAt, err := time.Parse(time.RFC3339, req.TransactionData.RequestedAt)
	if err != nil {
		requestedAt = time.Now().UTC()
	}

	vec[0] = clamp(req.TransactionData.Amount / cfg.MaxAmount)

	vec[1] = clamp(float64(req.TransactionData.Installments) / cfg.MaxInstallments)

	if req.Customer.AvgAmount > 0 {
		vec[2] = clamp((req.TransactionData.Amount / req.Customer.AvgAmount) / cfg.AmountVsAvgRatio)
	} else {
		vec[2] = clamp(req.TransactionData.Amount / cfg.AmountVsAvgRatio)
	}

	vec[3] = float64(requestedAt.Hour()) / 23.0

	dow := requestedAt.Weekday()
	dayOfWeek := (int(dow) + 6) % 7
	vec[4] = float64(dayOfWeek) / 6.0

	if req.LastTransaction != nil {
		lastTs, err := time.Parse(time.RFC3339, req.LastTransaction.Timestamp)
		if err == nil {
			minutes := requestedAt.Sub(lastTs).Minutes()
			vec[5] = clamp(minutes / cfg.MaxMinutes)
		} else {
			vec[5] = -1
		}
		vec[6] = clamp(req.LastTransaction.KmFromCurrent / cfg.MaxKm)
	} else {
		vec[5] = -1
		vec[6] = -1
	}

	vec[7] = clamp(req.Terminal.KmFromHome / cfg.MaxKm)

	vec[8] = clamp(float64(req.Customer.TxCount24h) / cfg.MaxTxCount24h)

	if req.Terminal.IsOnline {
		vec[9] = 1
	} else {
		vec[9] = 0
	}

	if req.Terminal.CardPresent {
		vec[10] = 1
	} else {
		vec[10] = 0
	}

	if isUnknownMerchant(req.Merchant.ID, req.Customer.KnownMerchants) {
		vec[11] = 1
	} else {
		vec[11] = 0
	}

	risk, ok := mccRisk[req.Merchant.MCC]
	if !ok {
		risk = 0.5
	}
	vec[12] = risk

	vec[13] = clamp(req.Merchant.AvgAmount / cfg.MaxMerchantAvgAmount)

	return vec
}

func isUnknownMerchant(merchantID string, known []string) bool {
	for _, m := range known {
		if m == merchantID {
			return false
		}
	}
	return true
}
