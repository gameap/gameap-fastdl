//go:build !windows

package main

import (
	"errors"
	"os"
	"syscall"
)

var errServiceUnsupported = errors.New("service command is only available on Windows; use serve with systemd")

func terminationSignal() os.Signal {
	return syscall.SIGTERM
}

func service(string) error {
	return errServiceUnsupported
}
