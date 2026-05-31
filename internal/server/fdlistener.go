package server

import (
	"net"
	"os"
	"sync"
	"syscall"
)

type fdListener struct {
	mu   sync.Mutex
	conn *net.UnixConn
}

func ListenFD(sockPath string) (net.Listener, error) {
	os.Remove(sockPath)
	addr := &net.UnixAddr{Name: sockPath, Net: "unix"}
	ul, err := net.ListenUnix("unix", addr)
	if err != nil {
		return nil, err
	}
	os.Chmod(sockPath, 0666)

	lbConn, err := ul.AcceptUnix()
	ul.Close()
	if err != nil {
		return nil, err
	}
	return &fdListener{conn: lbConn}, nil
}

func (l *fdListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	buf := make([]byte, 1)
	oob := make([]byte, syscall.CmsgSpace(4))
	_, oobn, _, _, err := l.conn.ReadMsgUnix(buf, oob)
	if err != nil {
		return nil, err
	}
	scms, err := syscall.ParseSocketControlMessage(oob[:oobn])
	if err != nil || len(scms) == 0 {
		return nil, net.ErrClosed
	}
	fds, err := syscall.ParseUnixRights(&scms[0])
	if err != nil || len(fds) == 0 {
		return nil, net.ErrClosed
	}

	file := os.NewFile(uintptr(fds[0]), "client")
	defer file.Close()
	conn, err := net.FileConn(file)
	return conn, err
}

func (l *fdListener) Close() error {
	return l.conn.Close()
}

func (l *fdListener) Addr() net.Addr {
	return l.conn.LocalAddr()
}
