package server

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"

	"fraud-detector/internal/handler"
)

var httpResponses [6][]byte
var readyHTTP []byte
var badRequestHTTP = []byte("HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n")
var serviceUnavailableHTTP = []byte("HTTP/1.1 503 Service Unavailable\r\nContent-Length: 0\r\n\r\n")

var connBodyPool = sync.Pool{
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

func HandleConn(conn net.Conn, h *handler.FraudHandler) {
	defer conn.Close()

	reader := bufio.NewReaderSize(conn, 2048)
	req, err := http.ReadRequest(reader)
	if err != nil {
		return
	}

	switch {
	case req.Method == "GET" && req.URL.Path == "/ready":
		if h.IsReady() {
			conn.Write(readyHTTP)
		} else {
			conn.Write(serviceUnavailableHTTP)
		}

	case req.Method == "POST" && req.URL.Path == "/fraud-score":
		if !h.IsReady() {
			conn.Write(serviceUnavailableHTTP)
			return
		}
		buf := connBodyPool.Get().([]byte)
		n, err := io.ReadFull(req.Body, buf[:cap(buf)])
		req.Body.Close()
		if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
			connBodyPool.Put(buf)
			conn.Write(badRequestHTTP)
			return
		}
		if n == 0 {
			connBodyPool.Put(buf)
			conn.Write(badRequestHTTP)
			return
		}
		fraudCount := h.Process(buf[:n])
		connBodyPool.Put(buf)
		conn.Write(httpResponses[fraudCount])

	default:
		conn.Write(badRequestHTTP)
	}
}
