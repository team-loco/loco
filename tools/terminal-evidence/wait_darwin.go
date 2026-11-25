package main

import (
	"context"
	"errors"
	"syscall"

	"golang.org/x/sys/unix"
)

// Wait without reaping the leader; capture must first terminate any surviving descendants.
func waitExited(ctx context.Context, pid int) error {
	queue, err := unix.Kqueue()
	if err != nil {
		return err
	}
	defer unix.Close(queue)
	change := unix.Kevent_t{
		Ident:  uint64(pid),
		Filter: unix.EVFILT_PROC,
		Flags:  unix.EV_ADD | unix.EV_ONESHOT,
		Fflags: unix.NOTE_EXIT,
	}
	if _, err := unix.Kevent(queue, []unix.Kevent_t{change}, nil, nil); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil // The unreaped child has already exited.
		}
		return err
	}
	events := make([]unix.Kevent_t, 1)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		timeout := unix.Timespec{Nsec: 100_000_000}
		n, err := unix.Kevent(queue, nil, events, &timeout)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if n > 0 {
			return nil
		}
	}
}
