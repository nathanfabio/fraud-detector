package server

import (
	"net"
	"os"
	"syscall"
)

type fdListener struct {
	conn *net.UnixConn
	oob  []byte
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
	return &fdListener{
		conn: lbConn,
		oob:  make([]byte, syscall.CmsgSpace(4)),
	}, nil
}

func (l *fdListener) Accept() (net.Conn, error) {
	var buf [1]byte
	_, oobn, _, _, err := l.conn.ReadMsgUnix(buf[:], l.oob)
	if err != nil {
		return nil, err
	}
	scms, err := syscall.ParseSocketControlMessage(l.oob[:oobn])
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
