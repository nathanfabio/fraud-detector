package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"syscall"
)

func main() {
	apiSocks := []string{"/run/sock/api1.sock", "/run/sock/api2.sock"}

	type apiConn struct {
		fd   int
		file *os.File
	}
	var apiConns []apiConn

	for _, path := range apiSocks {
		addr := &net.UnixAddr{Name: path, Net: "unix"}
		conn, err := net.DialUnix("unix", nil, addr)
		if err != nil {
			log.Fatalf("Failed to connect to %s: %v", path, err)
		}
		file, err := conn.File()
		if err != nil {
			log.Fatalf("Failed to get fd for %s: %v", path, err)
		}
		conn.Close()
		ac := apiConn{fd: int(file.Fd()), file: file}
		apiConns = append(apiConns, ac)
		fmt.Printf("Connected to %s (fd=%d)\n", path, ac.fd)
	}
	_ = apiConns

	tcpListener, err := net.Listen("tcp", ":80")
	if err != nil {
		log.Fatalf("TCP listen failed: %v", err)
	}
	fmt.Println("LB listening on :80")

	idx := 0
	for {
		clientConn, err := tcpListener.Accept()
		if err != nil {
			log.Printf("Accept error: %v", err)
			continue
		}

		tcpConn := clientConn.(*net.TCPConn)
		clientFile, err := tcpConn.File()
		if err != nil {
			log.Printf("Failed to get client fd: %v", err)
			clientConn.Close()
			continue
		}
		clientConn.Close()

		clientFd := int(clientFile.Fd())
		targetFd := apiConns[idx].fd
		idx = (idx + 1) % len(apiConns)

		go func(tfd, cfd int, cf *os.File) {
			defer cf.Close()
			rights := syscall.UnixRights(cfd)
			if err := syscall.Sendmsg(tfd, []byte{0}, rights, nil, 0); err != nil {
				log.Printf("Sendmsg failed: %v", err)
			}
		}(targetFd, clientFd, clientFile)
	}
}
