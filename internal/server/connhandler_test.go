package server

import (
	"testing"
)

func TestParseContentLength(t *testing.T) {
	tests := []struct {
		name     string
		headers  string
		expected int
	}{
		{"simple", "Content-Length: 42\r\n", 42},
		{"with_spaces", "Content-Length:   123\r\n", 123},
		{"no_header", "Host: example.com\r\n", 0},
		{"mixed_case", "Content-Length: 9999\r\nOther: val\r\n", 9999},
		{"empty", "", 0},
		{"zero", "Content-Length: 0\r\n", 0},
		{"large", "Content-Length: 4096\r\n", 4096},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseContentLength([]byte(tt.headers))
			if got != tt.expected {
				t.Errorf("parseContentLength(%q) = %d, want %d", tt.headers, got, tt.expected)
			}
		})
	}
}

func TestInitHTTP(t *testing.T) {
	readyJSON := []byte(`{"ready":true}`)
	templates := [6][]byte{
		[]byte(`{"approved":true,"fraud_score":0}`),
		[]byte(`{"approved":true,"fraud_score":0.2}`),
		[]byte(`{"approved":true,"fraud_score":0.4}`),
		[]byte(`{"approved":false,"fraud_score":0.6}`),
		[]byte(`{"approved":false,"fraud_score":0.8}`),
		[]byte(`{"approved":false,"fraud_score":1}`),
	}

	InitHTTP(readyJSON, templates)

	for i, r := range httpResponses {
		if len(r) == 0 {
			t.Errorf("httpResponses[%d] is empty", i)
		}
	}

	if len(readyHTTP) == 0 {
		t.Error("readyHTTP is empty")
	}
	if len(badRequestHTTP) == 0 {
		t.Error("badRequestHTTP is empty")
	}
	if len(serviceUnavailableHTTP) == 0 {
		t.Error("serviceUnavailableHTTP is empty")
	}
}
