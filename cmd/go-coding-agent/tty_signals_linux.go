//go:build linux

package main

import "golang.org/x/sys/unix"

func enableTTYSignals(fd int) error {
	state, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return err
	}
	state.Lflag |= unix.ISIG
	return unix.IoctlSetTermios(fd, unix.TCSETS, state)
}
