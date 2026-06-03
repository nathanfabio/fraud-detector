package netx

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

const epiocsparams = 0x40087001

type epollParams struct {
	busyPollUsecs  uint32
	busyPollBudget uint16
	preferBusyPoll uint8
	_              uint8
}

func SetEpollBusyPoll(epfd int) {
	p := epollParams{busyPollUsecs: 50, busyPollBudget: 8, preferBusyPoll: 1}
	unix.Syscall(unix.SYS_IOCTL, uintptr(epfd), uintptr(epiocsparams), uintptr(unsafe.Pointer(&p)))
}

const (
	cmsgHdrLen   = 16
	cmsgLenOneFD = 20
	cmsgSpaceFD  = 24
)

var fByte = [1]byte{'F'}

func SendFD(udsFd, clientFd int) error {
	var oob [cmsgSpaceFD]byte
	*(*uint64)(unsafe.Pointer(&oob[0])) = cmsgLenOneFD
	*(*int32)(unsafe.Pointer(&oob[8])) = unix.SOL_SOCKET
	*(*int32)(unsafe.Pointer(&oob[12])) = unix.SCM_RIGHTS
	*(*int32)(unsafe.Pointer(&oob[16])) = int32(clientFd)
	for {
		_, err := unix.SendmsgN(udsFd, fByte[:], oob[:], nil, unix.MSG_NOSIGNAL)
		if err == unix.EINTR {
			continue
		}
		return err
	}
}

func RecvFDs(ctrlFd int, oob []byte, out []int) (fds []int, ok bool, err error) {
	var p [64]byte
	var n, oobn int
	for {
		n, oobn, _, _, err = unix.Recvmsg(ctrlFd, p[:], oob, unix.MSG_CMSG_CLOEXEC|unix.MSG_DONTWAIT)
		if err == unix.EINTR {
			continue
		}
		break
	}
	if err != nil {
		return out, true, err
	}
	if n == 0 {
		return out, false, nil
	}
	buf := oob[:oobn]
	for off := 0; off+cmsgHdrLen <= len(buf); {
		clen := int(*(*uint64)(unsafe.Pointer(&buf[off])))
		if clen < cmsgHdrLen || off+clen > len(buf) {
			break
		}
		level := *(*int32)(unsafe.Pointer(&buf[off+8]))
		typ := *(*int32)(unsafe.Pointer(&buf[off+12]))
		if level == unix.SOL_SOCKET && typ == unix.SCM_RIGHTS {
			for d := off + cmsgHdrLen; d+4 <= off+clen; d += 4 {
				out = append(out, int(*(*int32)(unsafe.Pointer(&buf[d]))))
			}
		}
		off += (clen + 7) &^ 7
	}
	return out, true, nil
}
