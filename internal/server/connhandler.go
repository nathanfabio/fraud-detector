package server

import (
	"bytes"
	"fmt"
	"net"
	"sync"

	"fraud-detector/internal/handler"
)

var httpResponses [6][]byte
var readyHTTP []byte
var badRequestHTTP = []byte("HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n")
var serviceUnavailableHTTP = []byte("HTTP/1.1 503 Service Unavailable\r\nContent-Length: 0\r\n\r\n")

var connBufPool = sync.Pool{
	New: func() interface{} { return make([]byte, 4096) },
}

func InitHTTP(readyJSON []byte, templates [6][]byte) {
	for i, body := range templates {
		hdr := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n", len(body))
		httpResponses[i] = make([]byte, len(hdr)+len(body))
		copy(httpResponses[i], hdr)
		copy(httpResponses[i][len(hdr):], body)
	}
	hdr := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n", len(readyJSON))
	readyHTTP = make([]byte, len(hdr)+len(readyJSON))
	copy(readyHTTP, hdr)
	copy(readyHTTP[len(hdr):], readyJSON)
}

var (
	prefixGET     = []byte("GET /ready")
	prefixPOST    = []byte("POST /fraud-score")
	hdrsEnd       = []byte("\r\n\r\n")
	contentLenHdr = []byte("Content-Length:")
)

func HandleConn(conn net.Conn, h *handler.FraudHandler) {
	defer conn.Close()

	buf := connBufPool.Get().([]byte)
	buf = buf[:cap(buf)]
	n, err := conn.Read(buf)
	if err != nil || n < 4 {
		connBufPool.Put(buf)
		return
	}
	data := buf[:n]

	if bytes.HasPrefix(data, prefixGET) {
		if h.IsReady() {
			conn.Write(readyHTTP)
		} else {
			conn.Write(serviceUnavailableHTTP)
		}
		connBufPool.Put(buf)
		return
	}

	if !bytes.HasPrefix(data, prefixPOST) {
		conn.Write(badRequestHTTP)
		connBufPool.Put(buf)
		return
	}

	if !h.IsReady() {
		conn.Write(serviceUnavailableHTTP)
		connBufPool.Put(buf)
		return
	}

	hdrsIdx := bytes.Index(data, hdrsEnd)
	for hdrsIdx < 0 && n < cap(buf) {
		nn, rerr := conn.Read(buf[n:])
		if rerr != nil {
			conn.Write(badRequestHTTP)
			connBufPool.Put(buf)
			return
		}
		n += nn
		hdrsIdx = bytes.Index(buf[:n], hdrsEnd)
	}
	if hdrsIdx < 0 {
		conn.Write(badRequestHTTP)
		connBufPool.Put(buf)
		return
	}

	bodyStart := hdrsIdx + 4
	cl := parseContentLength(buf[:hdrsIdx])

	need := bodyStart + cl
	for n < need && n < cap(buf) {
		nn, rerr := conn.Read(buf[n:])
		if rerr != nil {
			break
		}
		n += nn
	}

	var body []byte
	if need <= n {
		body = buf[bodyStart:need]
	} else {
		body = buf[bodyStart:n]
	}

	if len(body) == 0 {
		conn.Write(badRequestHTTP)
		connBufPool.Put(buf)
		return
	}

	fraudCount := h.Process(body)
	connBufPool.Put(buf)
	conn.Write(httpResponses[fraudCount])
}

func parseContentLength(hdrs []byte) int {
	idx := bytes.Index(hdrs, contentLenHdr)
	if idx < 0 {
		return 0
	}
	idx += 15
	for idx < len(hdrs) && hdrs[idx] == ' ' {
		idx++
	}
	val := 0
	for idx < len(hdrs) && hdrs[idx] >= '0' && hdrs[idx] <= '9' {
		val = val*10 + int(hdrs[idx]-'0')
		idx++
	}
	return val
}
