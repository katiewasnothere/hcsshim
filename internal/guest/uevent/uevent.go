// +build linux

package uevent

import (
	"bytes"
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	kernelEventGroupID uint32 = 1
	udevEventGroupID   uint32 = 2
)

type UeventSocket struct {
	fd int
}

func NewConnection() (_ *UeventSocket, err error) {
	fd, err := unix.Socket(
		unix.AF_NETLINK,
		unix.SOCK_RAW,
		syscall.NETLINK_KOBJECT_UEVENT,
	)
	if err != nil {
		return nil, err
	}

	defer func() {
		if err != nil {
			_ = unix.Close(fd)
		}
	}()

	if err := unix.Bind(fd, &unix.SockaddrNetlink{
		Family: unix.AF_NETLINK,
		Groups: kernelEventGroupID,
	}); err != nil {
		return nil, err
	}
	return &UeventSocket{fd}, nil
}

func (s *UeventSocket) Close() {
	_ = unix.Close(s.fd)
}

func (s *UeventSocket) getMsgBuffer() (*[]byte, error) {
	buf := make([]byte, os.Getpagesize())
	for {
		size, _, err := unix.Recvfrom(s.fd, buf, unix.MSG_PEEK)
		if err != nil {
			return nil, err
		}
		if size < len(buf) {
			break
		}
		buf = make([]byte, len(buf)*2)
	}
	return &buf, nil
}

func (s *UeventSocket) ReadMsg() (int, *[]byte, error) {
	buf, err := s.getMsgBuffer()
	if err != nil {
		return 0, nil, err
	}

	size, _, err := unix.Recvfrom(s.fd, *buf, 0)
	if err != nil {
		return 0, nil, err
	}
	*buf = (*buf)[:size]
	return size, buf, nil
}

type ueventAction int

const (
	ueventAdd ueventAction = iota
	ueventRemove
	ueventChange
	ueventMove
	ueventOnline
	ueventOffline
)

const (
	blockDevice = "block"
	charDevice  = "char"
)

type Message struct {
	Action     string
	DevicePath string
	Attributes map[string]string
}

func Parse(raw *[]byte) (*Message, error) {
	data := bytes.SplitN(*raw, []byte{'@'}, 2)
	if len(data) < 2 {
		return nil, errors.New("uevent message has no operation")
	}
	op := data[0]
	msg := data[1]

	u := &Message{
		Action:     string(op),
		Attributes: make(map[string]string),
	}

	fields := bytes.Split(msg, []byte{0})
	if len(fields) == 0 {
		return nil, errors.New("uevent message has no data")
	}
	u.DevicePath = string(fields[0])

	// the first index contains the device path
	fields = fields[1:]
	for _, f := range fields {
		// these may be environment fields
		env := bytes.SplitN(f, []byte{'='}, 2)
		if len(env) < 2 {
			// this is not an env variable, let's just skip it
			continue
		}
		u.Attributes[string(env[0])] = string(env[1])
	}
	// parse key value environment
	return u, nil
}
