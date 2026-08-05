//go:build darwin

package main

import "golang.org/x/sys/unix"

func enableTTYSignals(fd int) error {
	state, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return err
	}
	state.Lflag |= unix.ISIG
	return unix.IoctlSetTermios(fd, unix.TIOCSETA, state)
}
