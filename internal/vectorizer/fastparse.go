package vectorizer

import (
	"bytes"
	"fraud-detector/internal/config"
	"math"
	"strconv"
	"time"
)

func ParseAndBuild(body []byte, cfg *config.Normalization, mccRisk map[string]float64) Vec14 {
	var txAmount, txInstallments float64
	var custAvgAmount, txCount24h, merchantAvgAmount float64
	var kmFromHome, kmFromCurrent float64
	var isOnline, cardPresent, unknownMerchant bool
	var requestedAtStr, lastTxAtStr string
	var merchantID, mcc string
	var knownMerchants []string
	hasLastTx := false

	pos := 0
	n := len(body)

	for pos < n {
		c := body[pos]
		if c == '"' {
			end := bytes.IndexByte(body[pos+1:], '"')
			if end < 0 {
				break
			}
			key := body[pos+1 : pos+1+end]
			pos += end + 2
			for pos < n && body[pos] != ':' {
				pos++
			}
			pos++
			for pos < n && (body[pos] == ' ' || body[pos] == '\t' || body[pos] == '\n') {
				pos++
			}

			switch string(key) {
			case "id":
				if pos < n && body[pos] == '"' {
					merchantID, pos = extractString(body, pos)
				}
			case "amount":
				if txAmount == 0 {
					txAmount, pos = extractFloat(body, pos)
				} else {
					pos = skipRawValue(body, pos)
				}
			case "installments":
				txInstallments, pos = extractFloat(body, pos)
			case "requested_at":
				requestedAtStr, pos = extractString(body, pos)
			case "avg_amount":
				if custAvgAmount == 0 {
					custAvgAmount, pos = extractFloat(body, pos)
				} else {
					merchantAvgAmount, pos = extractFloat(body, pos)
				}
			case "tx_count_24h":
				txCount24h, pos = extractFloat(body, pos)
			case "known_merchants":
				knownMerchants, pos = extractStringArray(body, pos)
			case "mcc":
				mcc, pos = extractString(body, pos)
			case "is_online":
				isOnline, pos = extractBool(body, pos)
			case "card_present":
				cardPresent, pos = extractBool(body, pos)
			case "km_from_home":
				kmFromHome, pos = extractFloat(body, pos)
			case "km_from_current":
				kmFromCurrent, pos = extractFloat(body, pos)
			case "last_transaction":
				if pos < n && body[pos] == 'n' {
					pos += 4
				} else {
					hasLastTx = true
					pos++
				}
			case "timestamp":
				if hasLastTx && lastTxAtStr == "" {
					lastTxAtStr, pos = extractString(body, pos)
				}
			case "transaction", "customer", "merchant", "terminal":
				if pos < n && body[pos] == '{' {
					pos++
				}
			default:
				pos = skipRawValue(body, pos)
			}
		} else {
			pos++
		}
	}

	checkMerchant := func(id string, list []string) bool {
		for _, m := range list {
			if m == id {
				return false
			}
		}
		return true
	}
	unknownMerchant = checkMerchant(merchantID, knownMerchants)

	return buildVector(txAmount, txInstallments, custAvgAmount, txCount24h,
		merchantAvgAmount, kmFromHome, kmFromCurrent,
		isOnline, cardPresent, unknownMerchant,
		requestedAtStr, lastTxAtStr, hasLastTx,
		mcc, cfg, mccRisk)
}

func extractString(data []byte, pos int) (string, int) {
	if pos >= len(data) || data[pos] != '"' {
		return "", pos
	}
	end := bytes.IndexByte(data[pos+1:], '"')
	if end < 0 {
		return "", pos
	}
	return string(data[pos+1 : pos+1+end]), pos + end + 2
}

func extractFloat(data []byte, pos int) (float64, int) {
	start := pos
	for pos < len(data) && (data[pos] >= '0' && data[pos] <= '9' || data[pos] == '-' || data[pos] == '.' || data[pos] == 'e' || data[pos] == 'E' || data[pos] == '+') {
		pos++
	}
	if pos == start {
		return 0, pos
	}
	val, _ := strconv.ParseFloat(string(data[start:pos]), 64)
	return val, pos
}

func extractBool(data []byte, pos int) (bool, int) {
	if pos+4 <= len(data) && string(data[pos:pos+4]) == "true" {
		return true, pos + 4
	}
	if pos+5 <= len(data) && string(data[pos:pos+5]) == "false" {
		return false, pos + 5
	}
	return false, pos
}

func extractStringArray(data []byte, pos int) ([]string, int) {
	if pos >= len(data) || data[pos] != '[' {
		return nil, pos
	}
	pos++
	var result []string
	for pos < len(data) {
		for pos < len(data) && (data[pos] == ' ' || data[pos] == '\t' || data[pos] == '\n' || data[pos] == ',') {
			pos++
		}
		if pos >= len(data) {
			break
		}
		if data[pos] == ']' {
			pos++
			break
		}
		if data[pos] == '"' {
			var s string
			s, pos = extractString(data, pos)
			result = append(result, s)
		} else {
			pos++
		}
	}
	return result, pos
}

func skipRawValue(data []byte, pos int) int {
	if pos >= len(data) {
		return pos
	}
	c := data[pos]
	switch c {
	case '"':
		_, pos = extractString(data, pos)
	case '{':
		depth := 1
		pos++
		for pos < len(data) && depth > 0 {
			if data[pos] == '{' {
				depth++
			} else if data[pos] == '}' {
				depth--
			} else if data[pos] == '"' {
				_, pos = extractString(data, pos)
				continue
			}
			pos++
		}
	case '[':
		depth := 1
		pos++
		for pos < len(data) && depth > 0 {
			if data[pos] == '[' {
				depth++
			} else if data[pos] == ']' {
				depth--
			} else if data[pos] == '"' {
				_, pos = extractString(data, pos)
				continue
			}
			pos++
		}
	case 't':
		pos += 4
	case 'f':
		pos += 5
	case 'n':
		pos += 4
	default:
		for pos < len(data) {
			b := data[pos]
			if (b >= '0' && b <= '9') || b == '-' || b == '.' || b == 'e' || b == 'E' || b == '+' {
				pos++
			} else {
				break
			}
		}
	}
	return pos
}

func buildVector(
	txAmount, txInstallments, custAvgAmount, txCount24h, merchantAvgAmount float64,
	kmFromHome, kmFromCurrent float64,
	isOnline, cardPresent, unknownMerchant bool,
	requestedAtStr, lastTxAtStr string, hasLastTx bool,
	mcc string,
	cfg *config.Normalization, mccRisk map[string]float64,
) Vec14 {
	var vec Vec14

	requestedAt, err := time.Parse(time.RFC3339, requestedAtStr)
	if err != nil {
		requestedAt = time.Now().UTC()
	}

	vec[0] = quantize(txAmount / cfg.MaxAmount)
	vec[1] = quantize(txInstallments / cfg.MaxInstallments)

	if custAvgAmount > 0 {
		vec[2] = quantize((txAmount / custAvgAmount) / cfg.AmountVsAvgRatio)
	} else {
		vec[2] = quantize(txAmount / cfg.AmountVsAvgRatio)
	}

	vec[3] = int8(math.Round(float64(requestedAt.Hour()) / 23.0 * 127.0))

	dow := requestedAt.Weekday()
	dayOfWeek := (int(dow) + 6) % 7
	vec[4] = int8(math.Round(float64(dayOfWeek) / 6.0 * 127.0))

	if hasLastTx && lastTxAtStr != "" {
		lastTs, err := time.Parse(time.RFC3339, lastTxAtStr)
		if err == nil {
			minutes := requestedAt.Sub(lastTs).Minutes()
			vec[5] = quantize(minutes / cfg.MaxMinutes)
		} else {
			vec[5] = -1
		}
		vec[6] = quantize(kmFromCurrent / cfg.MaxKm)
	} else {
		vec[5] = -1
		vec[6] = -1
	}

	vec[7] = quantize(kmFromHome / cfg.MaxKm)
	vec[8] = quantize(txCount24h / cfg.MaxTxCount24h)

	if isOnline {
		vec[9] = 127
	}
	if cardPresent {
		vec[10] = 127
	}
	if unknownMerchant {
		vec[11] = 127
	}

	risk, ok := mccRisk[mcc]
	if !ok {
		risk = 0.5
	}
	vec[12] = int8(math.Round(risk * 127.0))

	vec[13] = quantize(merchantAvgAmount / cfg.MaxMerchantAvgAmount)

	return vec
}
