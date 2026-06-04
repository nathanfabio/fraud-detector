package main

import (
	"log"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"time"

	"fraud-detector/internal/netx"

	"golang.org/x/sys/unix"
)

const (
	maxBackends = 8
	maxEvents   = 128
	retrySleep  = 100 * time.Millisecond
	epollIn     = 0x001
	epollEt     = 0x80000000
)

var backendsFD []int
var rrCursor int

func listenTCP(port int) (int, error) {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
	unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
	unix.SetsockoptInt(fd, unix.SOL_TCP, unix.TCP_DEFER_ACCEPT, 1)
	unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_BUSY_POLL, 50)
	unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_PREFER_BUSY_POLL, 1)
	unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_BUSY_POLL_BUDGET, 8)

	addr := &unix.SockaddrInet4{Port: port}
	if err := unix.Bind(fd, addr); err != nil {
		unix.Close(fd)
		return -1, err
	}
	if err := unix.Listen(fd, 4096); err != nil {
		unix.Close(fd)
		return -1, err
	}
	unix.SetNonblock(fd, true)
	return fd, nil
}

func acceptLoop(listenFD int) {
	for {
		cfd, _, err := unix.Accept4(listenFD, unix.SOCK_CLOEXEC)
		if err == unix.EINTR {
			continue
		}
		if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			return
		}
		if err != nil {
			return
		}
		be := backendsFD[rrCursor%len(backendsFD)]
		rrCursor++
		_ = netx.SendFD(be, cfd)
		unix.Close(cfd)
	}
}

func serverLoop(epfd, listenFD int) {
	events := make([]unix.EpollEvent, maxEvents)
	for {
		n, err := unix.EpollWait(epfd, events, -1)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			continue
		}
		for i := 0; i < n; i++ {
			if int(events[i].Fd) == listenFD {
				acceptLoop(listenFD)
			}
		}
	}
}

func die(msg string) {
	os.Stderr.WriteString(msg + "\n")
	os.Exit(1)
}

func main() {
	runtime.GOMAXPROCS(1)
	debug.SetGCPercent(-1)
	debug.SetMemoryLimit(6 << 20)

	args := os.Args[1:]
	if len(args) < 2 {
		die("usage: lb <port> <uds_path1> [uds_path2 ...]")
	}
	port, _ := strconv.Atoi(args[0])
	paths := args[1:]

	if len(paths) > maxBackends {
		paths = paths[:maxBackends]
	}

	unix.Prctl(unix.PR_SET_TIMERSLACK, 1, 0, 0, 0)

	listenFD, err := listenTCP(port)
	if err != nil {
		die("lb: listen_tcp failed: " + err.Error())
	}

	for _, p := range paths {
		bfd := -1
		for r := 0; r < 300; r++ {
			fd, e := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
			if e == nil {
				if unix.Connect(fd, &unix.SockaddrUnix{Name: p}) == nil {
					bfd = fd
					break
				}
				unix.Close(fd)
			}
			time.Sleep(retrySleep)
		}
		if bfd < 0 {
			die("lb: backend connect failed: " + p)
		}
		backendsFD = append(backendsFD, bfd)
		log.Printf("Connected to %s (fd=%d)", p, bfd)
	}

	epfd, err := unix.EpollCreate1(unix.EPOLL_CLOEXEC)
	if err != nil {
		die("lb: epoll_create1 failed")
	}
	netx.SetEpollBusyPoll(epfd)
	if err := unix.EpollCtl(epfd, unix.EPOLL_CTL_ADD, listenFD,
		&unix.EpollEvent{Events: epollIn | epollEt, Fd: int32(listenFD)}); err != nil {
		die("lb: epoll_ctl add listen failed")
	}

	log.Printf("LB listening on :%d", port)
	serverLoop(epfd, listenFD)
}
