package main

import (
	"bytes"
	"flag"
	"log"
	"os"
	"runtime"
	"runtime/debug"
	"unsafe"

	"fraud-detector/internal/config"
	"fraud-detector/internal/handler"
	"fraud-detector/internal/index"
	"fraud-detector/internal/netx"

	"golang.org/x/sys/unix"
)

const (
	bufSize    = 4096
	maxFDs     = 1024
	maxEvents  = 128
	epollIn    = 0x001
	epollRdhup = 0x2000
)

var (
	states   []connState
	ctrlFD   int
	epollFD  int
	h        *handler.FraudHandler
	hdrSep   = []byte("\r\n\r\n")
	clKey    = []byte("content-length:")

	responses = [6][]byte{
		[]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 35\r\n\r\n{\"approved\":true,\"fraud_score\":0.0}"),
		[]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 35\r\n\r\n{\"approved\":true,\"fraud_score\":0.2}"),
		[]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 35\r\n\r\n{\"approved\":true,\"fraud_score\":0.4}"),
		[]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 36\r\n\r\n{\"approved\":false,\"fraud_score\":0.6}"),
		[]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 36\r\n\r\n{\"approved\":false,\"fraud_score\":0.8}"),
		[]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 36\r\n\r\n{\"approved\":false,\"fraud_score\":1.0}"),
	}
	readyResp = []byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
	errResp   = []byte("HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n")
)

type connState struct {
	buf [bufSize]byte
	pos int
}

func recvNB(fd int, p []byte) (int, unix.Errno) {
	r0, _, e := unix.RawSyscall6(unix.SYS_RECVFROM, uintptr(fd),
		uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)), uintptr(unix.MSG_DONTWAIT), 0, 0)
	return int(r0), e
}

func sendRaw(fd int, p []byte) (int, unix.Errno) {
	r0, _, e := unix.RawSyscall6(unix.SYS_SENDTO, uintptr(fd),
		uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)), uintptr(unix.MSG_NOSIGNAL), 0, 0)
	return int(r0), e
}

func sendAll(fd int, p []byte) {
	off := 0
	for off < len(p) {
		n, errno := sendRaw(fd, p[off:])
		if errno == unix.EINTR || errno == unix.EAGAIN || errno == unix.EWOULDBLOCK {
			continue
		}
		if errno != 0 || n <= 0 {
			return
		}
		off += n
	}
}

func parseContentLength(hdr []byte) int {
	idx := indexFold(hdr, clKey)
	if idx < 0 {
		return 0
	}
	j := idx + len(clKey)
	for j < len(hdr) && (hdr[j] == ' ' || hdr[j] == '\t') {
		j++
	}
	n := 0
	for j < len(hdr) && hdr[j] >= '0' && hdr[j] <= '9' {
		n = n*10 + int(hdr[j]-'0')
		if n > bufSize {
			return bufSize + 1
		}
		j++
	}
	return n
}

func indexFold(hay, needle []byte) int {
	if len(needle) == 0 {
		return 0
	}
	last := len(hay) - len(needle)
	for i := 0; i <= last; i++ {
		k := 0
		for ; k < len(needle); k++ {
			c := hay[i+k]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != needle[k] {
				break
			}
		}
		if k == len(needle) {
			return i
		}
	}
	return -1
}

func handleRequest(req []byte, bodyOff int) []byte {
	n := len(req)
	if n >= 5 && req[0] == 'P' && req[1] == 'O' && req[2] == 'S' && req[3] == 'T' && req[4] == ' ' {
		body := req[bodyOff:]
		if len(body) == 0 {
			return errResp
		}
		fraudCount := h.Process(body)
		if fraudCount > 5 {
			fraudCount = 5
		}
		return responses[fraudCount]
	}
	if n >= 4 && req[0] == 'G' && req[1] == 'E' && req[2] == 'T' && req[3] == ' ' {
		return readyResp
	}
	return errResp
}

func closeClient(fd int) {
	unix.EpollCtl(epollFD, unix.EPOLL_CTL_DEL, fd, nil)
	unix.Close(fd)
	if fd < maxFDs {
		states[fd].pos = 0
	}
}

func handleClient(fd int) {
	st := &states[fd]
	if st.pos >= bufSize {
		closeClient(fd)
		return
	}
	n, errno := recvNB(fd, st.buf[st.pos:])
	if errno == unix.EAGAIN || errno == unix.EWOULDBLOCK || errno == unix.EINTR {
		return
	}
	if n == 0 || errno != 0 {
		closeClient(fd)
		return
	}
	st.pos += n

	for st.pos > 0 {
		hdrEnd := bytes.Index(st.buf[:st.pos], hdrSep)
		if hdrEnd < 0 {
			return
		}
		bodyOff := hdrEnd + 4
		cl := parseContentLength(st.buf[:bodyOff])
		total := bodyOff + cl
		if total > bufSize {
			closeClient(fd)
			return
		}
		if st.pos < total {
			return
		}
		sendAll(fd, handleRequest(st.buf[:total], bodyOff))
		rem := st.pos - total
		if rem > 0 {
			copy(st.buf[:rem], st.buf[total:st.pos])
		}
		st.pos = rem
	}
}

var (
	ctrlOOB  [256]byte
	fdScratch = make([]int, 0, 64)
)

func handleCtrl() {
	fds, ok, err := netx.RecvFDs(ctrlFD, ctrlOOB[:], fdScratch[:0])
	if !ok || err != nil {
		return
	}
	for _, fd := range fds {
		if fd >= maxFDs {
			unix.Close(fd)
			continue
		}
		unix.SetsockoptInt(fd, unix.SOL_TCP, unix.TCP_NODELAY, 1)
		unix.SetsockoptInt(fd, unix.SOL_TCP, unix.TCP_QUICKACK, 1)
		states[fd].pos = 0
		unix.EpollCtl(epollFD, unix.EPOLL_CTL_ADD, fd,
			&unix.EpollEvent{Events: epollIn | epollRdhup, Fd: int32(fd)})
	}
}

func bindControlUDS(path string) (int, error) {
	unix.Unlink(path)
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	if err := unix.Bind(fd, &unix.SockaddrUnix{Name: path}); err != nil {
		unix.Close(fd)
		return -1, err
	}
	unix.Chmod(path, 0666)
	if err := unix.Listen(fd, 8); err != nil {
		unix.Close(fd)
		return -1, err
	}
	for {
		cfd, _, err := unix.Accept4(fd, unix.SOCK_CLOEXEC)
		if err == unix.EINTR {
			continue
		}
		unix.Close(fd)
		if err != nil {
			return -1, err
		}
		return cfd, nil
	}
}

type schedParam struct{ priority int32 }

func setRealtimePriority() {
	p := schedParam{priority: 10}
	unix.Syscall(unix.SYS_SCHED_SETSCHEDULER, 0, uintptr(1), uintptr(unsafe.Pointer(&p)))
}

func die(msg string) {
	os.Stderr.WriteString(msg + "\n")
	os.Exit(1)
}

func main() {
	buildIndexIn := flag.String("build-index-in", "", "JSON.GZ to build index from")
	buildIndexOut := flag.String("build-index-out", "", "Output path for built index")
	flag.Parse()

	if *buildIndexIn != "" && *buildIndexOut != "" {
		log.Printf("Building IVF index: %s -> %s", *buildIndexIn, *buildIndexOut)
		if err := index.BuildIndex(*buildIndexIn, *buildIndexOut); err != nil {
			log.Fatalf("Build failed: %v", err)
		}
		log.Println("Index built successfully")
		return
	}

	runtime.GOMAXPROCS(1)
	runtime.LockOSThread()
	setRealtimePriority()

	debug.SetGCPercent(-1)
	debug.SetMemoryLimit(160 << 20)

	unix.Prctl(unix.PR_SET_TIMERSLACK, 1, 0, 0, 0)
	unix.Mlockall(unix.MCL_CURRENT | unix.MCL_FUTURE)

	normCfg, err := config.LoadNormalization("resources/normalization.json")
	if err != nil {
		die("Failed to load normalization: " + err.Error())
	}
	mccRisk, err := config.LoadMCCRisk("resources/mcc_risk.json")
	if err != nil {
		die("Failed to load MCC risk: " + err.Error())
	}

	h = handler.New(normCfg, mccRisk, nil)

	indexPath := "resources/index.bin"
	if envPath := os.Getenv("INDEX_PATH"); envPath != "" {
		indexPath = envPath
	}
	idx, err := index.LoadBinary(indexPath)
	if err != nil {
		die("Failed to load index: " + err.Error())
	}
	totalVecs := 0
	for _, p := range idx.Parts {
		if p != nil {
			totalVecs += len(p.Vectors)
		}
	}
	log.Printf("Index loaded: %d vectors across %d partitions", totalVecs, len(idx.Parts))
	h.SetIndex(idx)
	h.Warmup()
	h.SetReady()
	log.Println("Warmup complete, ready")

	states = make([]connState, maxFDs)
	fdScratch = make([]int, 0, 64)

	hostname, _ := os.Hostname()
	sockPath := "/run/sock/" + hostname + ".sock"
	os.MkdirAll("/run/sock", 0777)

	cfd, err := bindControlUDS(sockPath)
	if err != nil {
		die("bind failed: " + err.Error())
	}
	ctrlFD = cfd

	epollFD, err = unix.EpollCreate1(unix.EPOLL_CLOEXEC)
	if err != nil {
		die("epoll_create1 failed")
	}
	netx.SetEpollBusyPoll(epollFD)
	if err := unix.EpollCtl(epollFD, unix.EPOLL_CTL_ADD, ctrlFD,
		&unix.EpollEvent{Events: epollIn, Fd: int32(ctrlFD)}); err != nil {
		die("epoll_ctl add ctrl failed")
	}

		log.Printf("Epoll loop starting on %s", sockPath)
	events := make([]unix.EpollEvent, maxEvents)
	for {
		n, err := unix.EpollWait(epollFD, events, 1)
		if err == unix.EINTR {
			continue
		}
		if n <= 0 {
			continue
		}
		for i := 0; i < n; i++ {
			fd := int(events[i].Fd)
			if fd == ctrlFD {
				handleCtrl()
			} else {
				handleClient(fd)
			}
		}
	}
}
