package vectorizer

import (
	"testing"

	"fraud-detector/internal/config"
)

var testCfg = &config.Normalization{
	MaxAmount:            10000,
	MaxInstallments:      12,
	AmountVsAvgRatio:     10,
	MaxMinutes:           1440,
	MaxKm:                1000,
	MaxTxCount24h:        20,
	MaxMerchantAvgAmount: 10000,
}

func TestFastParseFloat(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected float64
		endPos   int
	}{
		{"integer", "123", 123, 3},
		{"float", "123.45", 123.45, 6},
		{"zero", "0", 0, 1},
		{"negative", "-50", -50, 3},
		{"negative_float", "-12.34", -12.34, 6},
		{"trailing_comma", "42,", 42, 2},
		{"decimal_start", ".5", 0.5, 2},
		{"leading_zeros", "007", 7, 3},
		{"empty_string", "", 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := []byte(tt.input)
			got, pos := fastParseFloat(data, 0)
			if got != tt.expected || pos != tt.endPos {
				t.Errorf("fastParseFloat(%q) = (%f, %d), want (%f, %d)",
					tt.input, got, pos, tt.expected, tt.endPos)
			}
		})
	}
}

func TestExtractBytes(t *testing.T) {
	data := []byte(`"hello","world"`)
	got, pos := extractBytes(data, 0)
	if string(got) != "hello" || pos != 7 {
		t.Errorf("extractBytes = (%q, %d), want (hello, 7)", got, pos)
	}

	got2, pos2 := extractBytes(data, 8)
	if string(got2) != "world" || pos2 != 15 {
		t.Errorf("extractBytes second = (%q, %d), want (world, 15)", got2, pos2)
	}

	empty := []byte(`  `)
	got3, _ := extractBytes(empty, 0)
	if got3 != nil {
		t.Errorf("extractBytes on non-quoted should return nil, got %q", got3)
	}
}

func TestExtractBool(t *testing.T) {
	got, pos := extractBool([]byte("true"), 0)
	if !got || pos != 4 {
		t.Errorf("extractBool(true) = (%v, %d), want (true, 4)", got, pos)
	}

	got2, pos2 := extractBool([]byte("false,"), 0)
	if got2 || pos2 != 5 {
		t.Errorf("extractBool(false) = (%v, %d), want (false, 5)", got2, pos2)
	}

	got3, _ := extractBool([]byte("null"), 0)
	if got3 {
		t.Errorf("extractBool(null) should return false")
	}
}

func TestExtractRawArray(t *testing.T) {
	data := []byte(`["a","b","c"]x`)
	got, pos := extractRawArray(data, 0)
	expected := `"a","b","c"`
	if string(got) != expected || pos != 13 {
		t.Errorf("extractRawArray = (%q, %d), want (%q, 13)", got, pos, expected)
	}

	empty, _ := extractRawArray([]byte(`[]`), 0)
	if string(empty) != "" {
		t.Errorf("extractRawArray empty = %q, want empty", empty)
	}
}

func TestMerchantInKnown(t *testing.T) {
	known := []byte(`"M1","M2","M3"`)
	if !merchantInKnown([]byte("M1"), known) {
		t.Error("M1 should be found")
	}
	if !merchantInKnown([]byte("M3"), known) {
		t.Error("M3 should be found")
	}
	if merchantInKnown([]byte("M4"), known) {
		t.Error("M4 should not be found")
	}
	if merchantInKnown(nil, known) {
		t.Error("nil merchant should not be found")
	}
	if merchantInKnown([]byte("M1"), nil) {
		t.Error("should not be found in nil list")
	}
}

func TestFastMCCRisk(t *testing.T) {
	if got := fastMCCRisk([]byte("5411")); got != 0.15 {
		t.Errorf("fastMCCRisk(5411) = %f, want 0.15", got)
	}
	if got := fastMCCRisk([]byte("7801")); got != 0.80 {
		t.Errorf("fastMCCRisk(7801) = %f, want 0.80", got)
	}
	if got := fastMCCRisk([]byte("7995")); got != 0.85 {
		t.Errorf("fastMCCRisk(7995) = %f, want 0.85", got)
	}
	if got := fastMCCRisk([]byte("9999")); got != 0.5 {
		t.Errorf("fastMCCRisk(9999) = %f, want 0.5 (default)", got)
	}
	if got := fastMCCRisk([]byte("AB")); got != 0.5 {
		t.Errorf("fastMCCRisk(short) = %f, want 0.5", got)
	}
	if got := fastMCCRisk(nil); got != 0.5 {
		t.Errorf("fastMCCRisk(nil) = %f, want 0.5", got)
	}
}

func TestParseTimestamp(t *testing.T) {
	y, mo, d, h, mi, s, ok := parseTimestamp("2026-03-11T18:45:53Z")
	if !ok {
		t.Fatal("parseTimestamp returned not ok")
	}
	if y != 2026 || mo != 3 || d != 11 || h != 18 || mi != 45 || s != 53 {
		t.Errorf("got %d-%d-%dT%d:%d:%d, want 2026-3-11T18:45:53", y, mo, d, h, mi, s)
	}

	_, _, _, _, _, _, ok = parseTimestamp("short")
	if ok {
		t.Error("expect false for short string")
	}

	_, _, _, _, _, _, ok = parseTimestamp("2026-13-01T00:00:00Z")
	if ok {
		t.Error("expect false for invalid month")
	}
}

func TestDayOfWeek(t *testing.T) {
	monday := dayOfWeek(2026, 3, 2)

	z := dayOfWeek(1970, 1, 1)
	if z < 0 || z > 6 {
		t.Errorf("dayOfWeek returned %d, want 0-6", z)
	}

	tuesday := dayOfWeek(2026, 3, 3)
	wednesday := dayOfWeek(2026, 3, 4)
	thursday := dayOfWeek(2026, 3, 5)
	friday := dayOfWeek(2026, 3, 6)
	saturday := dayOfWeek(2026, 3, 7)
	sunday := dayOfWeek(2026, 3, 8)

	if tuesday != (monday+1)%7 {
		t.Error("dayOfWeek: expected tuesday == monday+1")
	}
	_ = wednesday
	_ = thursday
	_ = friday
	_ = saturday
	_ = sunday

	if monday == sunday {
		t.Error("monday should not equal sunday")
	}
}

func TestTotalMinutes(t *testing.T) {
	got := totalMinutes(2026, 3, 11, 18, 45, 0)
	if got <= 0 {
		t.Errorf("totalMinutes returned %d, expected positive", got)
	}

	got2 := totalMinutes(2026, 3, 11, 18, 46, 0)
	if got2 != got+1 {
		t.Errorf("totalMinutes diff = %d, want 1", got2-got)
	}
}

func TestSkipRawValue(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		wantPos int
	}{
		{"string", `"hello",rest`, 7},
		{"number", `1234,rest`, 4},
		{"true", `true,rest`, 4},
		{"false", `false,rest`, 5},
		{"null", `null,rest`, 4},
		{"negative", `-42,rest`, 3},
		{"float", `3.14e10,rest`, 7},
		{"object", `{"a":"b"}x`, 9},
		{"array", `[1,2,3]x`, 7},
		{"nested_obj", `{"a":{"b":1}}x`, 13},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := []byte(tt.input)
			got := skipRawValue(data, 0)
			if got != tt.wantPos {
				t.Errorf("skipRawValue(%q) = %d, want %d", tt.input, got, tt.wantPos)
			}
		})
	}
}

func TestParseAndBuildFull(t *testing.T) {
	body := []byte(`{
		"transaction": {
			"amount": 1000.50,
			"installments": 3,
			"requested_at": "2026-03-11T18:45:53Z"
		},
		"customer": {
			"avg_amount": 2000,
			"tx_count_24h": 5,
			"known_merchants": ["M1","M2","M3"]
		},
		"merchant": {
			"id": "M2",
			"mcc": "5411",
			"avg_amount": 500
		},
		"terminal": {
			"is_online": true,
			"card_present": false,
			"km_from_home": 50.5
		},
		"last_transaction": {
			"timestamp": "2026-03-11T18:30:00Z",
			"km_from_current": 10.2
		}
	}`)

	vec := ParseAndBuild(body, testCfg)

	if vec[0] != quantize(1000.50/testCfg.MaxAmount) {
		t.Errorf("vec[0] (amount) = %d", vec[0])
	}
	if vec[3] < 0 || vec[3] > 10000 {
		t.Errorf("vec[3] out of range: %d", vec[3])
	}
	if vec[9] != 10000 {
		t.Errorf("vec[9] (is_online) = %d, want 10000", vec[9])
	}
	if vec[11] != 0 {
		t.Errorf("vec[11] (known merchant) = %d, want 0", vec[11])
	}
	if vec[12] != int16(0.15*10000) {
		t.Errorf("vec[12] (mcc risk) = %d, want %d", vec[12], int16(0.15*10000))
	}
}

func TestParseAndBuildNoLastTx(t *testing.T) {
	body := []byte(`{
		"transaction": {
			"amount": 500,
			"installments": 1,
			"requested_at": "2026-06-15T12:00:00Z"
		},
		"customer": {
			"avg_amount": 1000,
			"tx_count_24h": 2,
			"known_merchants": []
		},
		"merchant": {
			"id": "M99",
			"mcc": "9999",
			"avg_amount": 200
		},
		"terminal": {
			"is_online": false,
			"card_present": true,
			"km_from_home": 100
		},
		"last_transaction": null
	}`)

	vec := ParseAndBuild(body, testCfg)

	if vec[5] != -1 {
		t.Errorf("vec[5] = %d, want -1", vec[5])
	}
	if vec[6] != -1 {
		t.Errorf("vec[6] = %d, want -1", vec[6])
	}
	if vec[10] != 10000 {
		t.Errorf("vec[10] (card_present) = %d, want 10000", vec[10])
	}
}

func TestParseAndBuildUnknownMCC(t *testing.T) {
	body := []byte(`{
		"transaction": {
			"amount": 100,
			"installments": 1,
			"requested_at": "2026-01-01T00:00:00Z"
		},
		"customer": {
			"avg_amount": 500,
			"tx_count_24h": 1,
			"known_merchants": []
		},
		"merchant": {
			"id": "M1",
			"mcc": "0000",
			"avg_amount": 100
		},
		"terminal": {
			"is_online": false,
			"card_present": false,
			"km_from_home": 0
		},
		"last_transaction": null
	}`)

	vec := ParseAndBuild(body, testCfg)

	if vec[12] != int16(0.5*10000) {
		t.Errorf("vec[12] (unknown mcc) = %d, want %d", vec[12], int16(0.5*10000))
	}
}

func TestParseAndBuildDuplicateAvgAmount(t *testing.T) {
	body := []byte(`{
		"transaction": {
			"amount": 300,
			"installments": 2,
			"requested_at": "2026-06-01T10:00:00Z"
		},
		"customer": {
			"avg_amount": 1500,
			"tx_count_24h": 3,
			"known_merchants": ["M1"]
		},
		"merchant": {
			"id": "M1",
			"mcc": "5812",
			"avg_amount": 800
		},
		"terminal": {
			"is_online": true,
			"card_present": true,
			"km_from_home": 25
		},
		"last_transaction": {
			"timestamp": "2026-05-31T10:00:00Z",
			"km_from_current": 5
		}
	}`)

	vec := ParseAndBuild(body, testCfg)

	if vec[0] == 0 {
		t.Error("vec[0] (amount) should not be zero")
	}
	if vec[11] != 0 {
		t.Errorf("vec[11] (known merchant M1) = %d, want 0", vec[11])
	}
	if vec[12] != int16(0.30*10000) {
		t.Errorf("vec[12] (mcc 5812 risk) = %d, want %d", vec[12], int16(0.30*10000))
	}
	if vec[13] == 0 {
		t.Error("vec[13] (merchant avg_amount) should not be zero")
	}
}
