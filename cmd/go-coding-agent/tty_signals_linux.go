//go:build linux

package main

import "golang.org/x/sys/unix"

func enableTTYSignalsAndOutput(fd int) error {
	state, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return err
	}
	state.Lflag |= unix.ISIG
	state.Oflag |= unix.OPOST
	return unix.IoctlSetTermios(fd, unix.TCSETS, state)
}
