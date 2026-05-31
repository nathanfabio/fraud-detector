package main

import (
	"fmt"
	"log"
	"net"
	"syscall"
)

func main() {
	apiSocks := []string{"/run/sock/api1.sock", "/run/sock/api2.sock"}

	var apiFds []int
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
		apiFds = append(apiFds, int(file.Fd()))
		fmt.Printf("Connected to %s (fd=%d)\n", path, apiFds[len(apiFds)-1])
	}

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
		targetFd := apiFds[idx]
		idx = (idx + 1) % len(apiFds)

		rights := syscall.UnixRights(clientFd)
		err = syscall.Sendmsg(targetFd, []byte{0}, rights, nil, 0)
		if err != nil {
			log.Printf("Sendmsg failed: %v", err)
		}
		clientFile.Close()
	}
}
