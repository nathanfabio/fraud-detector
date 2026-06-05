package vectorizer

import (
	"bytes"
	"math"
	"time"

	"fraud-detector/internal/config"
)

var (
	keyAmount          = []byte("amount")
	keyInstallments    = []byte("installments")
	keyRequestedAt     = []byte("requested_at")
	keyAvgAmount       = []byte("avg_amount")
	keyTxCount24h      = []byte("tx_count_24h")
	keyKnownMerchants  = []byte("known_merchants")
	keyMCC             = []byte("mcc")
	keyIsOnline        = []byte("is_online")
	keyCardPresent     = []byte("card_present")
	keyKmFromHome      = []byte("km_from_home")
	keyKmFromCurrent   = []byte("km_from_current")
	keyLastTransaction = []byte("last_transaction")
	keyTimestamp       = []byte("timestamp")
	keyID              = []byte("id")
	keyTransaction     = []byte("transaction")
	keyCustomer        = []byte("customer")
	keyTerminal        = []byte("terminal")
	keyMerchant        = []byte("merchant")
)

func ParseAndBuild(body []byte, cfg *config.Normalization) Vec14 {
	var txAmount, txInstallments float64
	var custAvgAmount, txCount24h, merchantAvgAmount float64
	var kmFromHome, kmFromCurrent float64
	var isOnline, cardPresent, unknownMerchant bool
	var requestedAtStr, lastTxAtStr string
	var mccBytes []byte
	var merchantIDBytes []byte
	var knownMerchantsRaw []byte
	hasLastTx := false
	inMerchant := false

	pos := 0
	n := len(body)

	for pos < n {
		c := body[pos]
		if c == '"' {
			keyEnd := bytes.IndexByte(body[pos+1:], '"')
			if keyEnd < 0 {
				break
			}
			key := body[pos+1 : pos+1+keyEnd]
			pos += keyEnd + 2
			for pos < n && body[pos] != ':' {
				pos++
			}
			pos++
			for pos < n && (body[pos] == ' ' || body[pos] == '\t' || body[pos] == '\n') {
				pos++
			}

			switch {
			case bytes.Equal(key, keyID):
				if pos < n && body[pos] == '"' && inMerchant {
					merchantIDBytes, pos = extractBytes(body, pos)
				} else {
					pos = skipRawValue(body, pos)
				}
			case bytes.Equal(key, keyAmount):
				if txAmount == 0 {
					txAmount, pos = fastParseFloat(body, pos)
				} else {
					pos = skipRawValue(body, pos)
				}
			case bytes.Equal(key, keyInstallments):
				txInstallments, pos = fastParseFloat(body, pos)
			case bytes.Equal(key, keyRequestedAt):
				requestedAtStr, pos = extractString(body, pos)
			case bytes.Equal(key, keyAvgAmount):
				if custAvgAmount == 0 {
					custAvgAmount, pos = fastParseFloat(body, pos)
				} else {
					merchantAvgAmount, pos = fastParseFloat(body, pos)
				}
			case bytes.Equal(key, keyTxCount24h):
				txCount24h, pos = fastParseFloat(body, pos)
			case bytes.Equal(key, keyKnownMerchants):
				knownMerchantsRaw, pos = extractRawArray(body, pos)
			case bytes.Equal(key, keyMCC):
				mccBytes, pos = extractBytes(body, pos)
			case bytes.Equal(key, keyIsOnline):
				isOnline, pos = extractBool(body, pos)
			case bytes.Equal(key, keyCardPresent):
				cardPresent, pos = extractBool(body, pos)
			case bytes.Equal(key, keyKmFromHome):
				kmFromHome, pos = fastParseFloat(body, pos)
			case bytes.Equal(key, keyKmFromCurrent):
				kmFromCurrent, pos = fastParseFloat(body, pos)
			case bytes.Equal(key, keyLastTransaction):
				if pos < n && body[pos] == 'n' {
					pos += 4
				} else {
					hasLastTx = true
					pos++
				}
			case bytes.Equal(key, keyTimestamp):
				if hasLastTx && lastTxAtStr == "" {
					lastTxAtStr, pos = extractString(body, pos)
				} else {
					pos = skipRawValue(body, pos)
				}
			case bytes.Equal(key, keyTransaction),
				bytes.Equal(key, keyCustomer),
				bytes.Equal(key, keyTerminal):
				if pos < n && body[pos] == '{' {
					pos++
				}
			case bytes.Equal(key, keyMerchant):
				if pos < n && body[pos] == '{' {
					inMerchant = true
					pos++
				}
			default:
				pos = skipRawValue(body, pos)
			}
		} else if c == '}' {
			inMerchant = false
			pos++
		} else {
			pos++
		}
	}

	unknownMerchant = !merchantInKnown(merchantIDBytes, knownMerchantsRaw)

	return buildVector(txAmount, txInstallments, custAvgAmount, txCount24h,
		merchantAvgAmount, kmFromHome, kmFromCurrent,
		isOnline, cardPresent, unknownMerchant,
		requestedAtStr, lastTxAtStr, hasLastTx,
		mccBytes, cfg)
}

func extractBytes(data []byte, pos int) ([]byte, int) {
	if pos >= len(data) || data[pos] != '"' {
		return nil, pos
	}
	end := bytes.IndexByte(data[pos+1:], '"')
	if end < 0 {
		return nil, pos
	}
	return data[pos+1 : pos+1+end], pos + end + 2
}

func extractString(data []byte, pos int) (string, int) {
	b, npos := extractBytes(data, pos)
	if b == nil {
		return "", pos
	}
	return string(b), npos
}

func fastParseFloat(data []byte, pos int) (float64, int) {
	start := pos
	negative := false
	decimal := false
	decDiv := float64(1)

	if pos < len(data) && data[pos] == '-' {
		negative = true
		pos++
	}

	var result float64
	for pos < len(data) {
		c := data[pos]
		if c >= '0' && c <= '9' {
			result = result*10 + float64(c-'0')
			if decimal {
				decDiv *= 10
			}
			pos++
		} else if c == '.' {
			decimal = true
			pos++
		} else {
			break
		}
	}

	if pos == start || (negative && pos == start+1) {
		return 0, start
	}

	if negative {
		result = -result
	}
	if decimal {
		result /= decDiv
	}
	return result, pos
}

func extractBool(data []byte, pos int) (bool, int) {
	if pos+4 <= len(data) && data[pos] == 't' && data[pos+1] == 'r' && data[pos+2] == 'u' && data[pos+3] == 'e' {
		return true, pos + 4
	}
	if pos+5 <= len(data) && data[pos] == 'f' {
		return false, pos + 5
	}
	return false, pos
}

func extractRawArray(data []byte, pos int) ([]byte, int) {
	if pos >= len(data) || data[pos] != '[' {
		return nil, pos
	}
	start := pos + 1
	end := bytes.IndexByte(data[start:], ']')
	if end < 0 {
		return nil, pos
	}
	return data[start : start+end], start + end + 1
}

func merchantInKnown(merchantID, knownRaw []byte) bool {
	if len(merchantID) == 0 || len(knownRaw) == 0 {
		return false
	}
	pos := 0
	for pos < len(knownRaw) {
		for pos < len(knownRaw) && (knownRaw[pos] == ' ' || knownRaw[pos] == '\t' || knownRaw[pos] == '\n' || knownRaw[pos] == ',') {
			pos++
		}
		if pos >= len(knownRaw) {
			break
		}
		if knownRaw[pos] == '"' {
			pos++
			sid := pos
			for pos < len(knownRaw) && knownRaw[pos] != '"' {
				pos++
			}
			if pos-sid == len(merchantID) && bytes.Equal(knownRaw[sid:pos], merchantID) {
				return true
			}
			pos++
		} else {
			pos++
		}
	}
	return false
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
				end := bytes.IndexByte(data[pos+1:], '"')
				if end >= 0 {
					pos += end + 2
					continue
				}
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
				end := bytes.IndexByte(data[pos+1:], '"')
				if end >= 0 {
					pos += end + 2
					continue
				}
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
	mccBytes []byte,
	cfg *config.Normalization,
) Vec14 {
	var vec Vec14

	requestedAt, err := time.Parse(time.RFC3339, requestedAtStr)
	if err != nil {
		requestedAt = time.Now().UTC()
	}
	reqHour := requestedAt.Hour()
	reqDow := (int(requestedAt.Weekday()) + 6) % 7

	vec[0] = quantize(txAmount / cfg.MaxAmount)
	vec[1] = quantize(txInstallments / cfg.MaxInstallments)

	if custAvgAmount > 0 {
		vec[2] = quantize((txAmount / custAvgAmount) / cfg.AmountVsAvgRatio)
	} else {
		vec[2] = quantize(txAmount / cfg.AmountVsAvgRatio)
	}

	vec[3] = int16(math.Round(float64(reqHour) / 23.0 * 10000.0))
	vec[4] = int16(math.Round(float64(reqDow) / 6.0 * 10000.0))

	if hasLastTx && lastTxAtStr != "" {
		lastTs, err := time.Parse(time.RFC3339, lastTxAtStr)
		if err == nil {
			minutes := requestedAt.Sub(lastTs).Minutes()
			if minutes < 0 {
				minutes = 0
			}
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
		vec[9] = 10000
	}
	if cardPresent {
		vec[10] = 10000
	}
	if unknownMerchant {
		vec[11] = 10000
	}

	risk := fastMCCRisk(mccBytes)
	vec[12] = int16(math.Round(risk * 10000.0))

	vec[13] = quantize(merchantAvgAmount / cfg.MaxMerchantAvgAmount)

	return vec
}

func fastMCCRisk(b []byte) float64 {
	if len(b) != 4 {
		return 0.5
	}
	k := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	switch k {
	case 0x35343131:
		return 0.15
	case 0x35383132:
		return 0.30
	case 0x35393132:
		return 0.20
	case 0x35393434:
		return 0.45
	case 0x37383031:
		return 0.80
	case 0x37383032:
		return 0.75
	case 0x37393935:
		return 0.85
	case 0x34353131:
		return 0.35
	case 0x35333131:
		return 0.25
	case 0x35393939:
		return 0.50
	}
	return 0.5
}
