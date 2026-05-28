package vectorizer

import (
	"math"
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

type Vec14 [14]int8

func quantize(v float64) int8 {
	if v == -1 {
		return -1
	}
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	return int8(math.Round(v * 127.0))
}

func Build(req *Request, cfg *config.Normalization, mccRisk map[string]float64) Vec14 {
	var vec Vec14

	requestedAt, err := time.Parse(time.RFC3339, req.TransactionData.RequestedAt)
	if err != nil {
		requestedAt = time.Now().UTC()
	}

	vec[0] = quantize(req.TransactionData.Amount / cfg.MaxAmount)
	vec[1] = quantize(float64(req.TransactionData.Installments) / cfg.MaxInstallments)

	if req.Customer.AvgAmount > 0 {
		vec[2] = quantize((req.TransactionData.Amount / req.Customer.AvgAmount) / cfg.AmountVsAvgRatio)
	} else {
		vec[2] = quantize(req.TransactionData.Amount / cfg.AmountVsAvgRatio)
	}

	vec[3] = int8(math.Round(float64(requestedAt.Hour()) / 23.0 * 127.0))

	dow := requestedAt.Weekday()
	dayOfWeek := (int(dow) + 6) % 7
	vec[4] = int8(math.Round(float64(dayOfWeek) / 6.0 * 127.0))

	if req.LastTransaction != nil {
		lastTs, err := time.Parse(time.RFC3339, req.LastTransaction.Timestamp)
		if err == nil {
			minutes := requestedAt.Sub(lastTs).Minutes()
			vec[5] = quantize(minutes / cfg.MaxMinutes)
		} else {
			vec[5] = -1
		}
		vec[6] = quantize(req.LastTransaction.KmFromCurrent / cfg.MaxKm)
	} else {
		vec[5] = -1
		vec[6] = -1
	}

	vec[7] = quantize(req.Terminal.KmFromHome / cfg.MaxKm)
	vec[8] = quantize(float64(req.Customer.TxCount24h) / cfg.MaxTxCount24h)

	if req.Terminal.IsOnline {
		vec[9] = 127
	} else {
		vec[9] = 0
	}

	if req.Terminal.CardPresent {
		vec[10] = 127
	} else {
		vec[10] = 0
	}

	if isUnknownMerchant(req.Merchant.ID, req.Customer.KnownMerchants) {
		vec[11] = 127
	} else {
		vec[11] = 0
	}

	risk, ok := mccRisk[req.Merchant.MCC]
	if !ok {
		risk = 0.5
	}
	vec[12] = int8(math.Round(risk * 127.0))

	vec[13] = quantize(req.Merchant.AvgAmount / cfg.MaxMerchantAvgAmount)

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

func ManhattanDist(a, b Vec14) int32 {
	var sum int32
	da := int32(a[0]) - int32(b[0])
	if da < 0 {
		sum -= da
	} else {
		sum += da
	}
	da = int32(a[1]) - int32(b[1])
	if da < 0 {
		sum -= da
	} else {
		sum += da
	}
	da = int32(a[2]) - int32(b[2])
	if da < 0 {
		sum -= da
	} else {
		sum += da
	}
	da = int32(a[3]) - int32(b[3])
	if da < 0 {
		sum -= da
	} else {
		sum += da
	}
	da = int32(a[4]) - int32(b[4])
	if da < 0 {
		sum -= da
	} else {
		sum += da
	}
	da = int32(a[5]) - int32(b[5])
	if da < 0 {
		sum -= da
	} else {
		sum += da
	}
	da = int32(a[6]) - int32(b[6])
	if da < 0 {
		sum -= da
	} else {
		sum += da
	}
	da = int32(a[7]) - int32(b[7])
	if da < 0 {
		sum -= da
	} else {
		sum += da
	}
	da = int32(a[8]) - int32(b[8])
	if da < 0 {
		sum -= da
	} else {
		sum += da
	}
	da = int32(a[9]) - int32(b[9])
	if da < 0 {
		sum -= da
	} else {
		sum += da
	}
	da = int32(a[10]) - int32(b[10])
	if da < 0 {
		sum -= da
	} else {
		sum += da
	}
	da = int32(a[11]) - int32(b[11])
	if da < 0 {
		sum -= da
	} else {
		sum += da
	}
	da = int32(a[12]) - int32(b[12])
	if da < 0 {
		sum -= da
	} else {
		sum += da
	}
	da = int32(a[13]) - int32(b[13])
	if da < 0 {
		sum -= da
	} else {
		sum += da
	}
	return sum
}
