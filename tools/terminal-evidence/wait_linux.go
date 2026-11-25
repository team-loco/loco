package main

import (
	"context"
	"time"

	"golang.org/x/sys/unix"
)

// Wait without reaping the leader; capture must first terminate any surviving descendants.
func waitExited(ctx context.Context, pid int) error {
	for {
		var info unix.Siginfo
		if err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOHANG|unix.WNOWAIT, nil); err != nil {
			return err
		}
		if info.Signo != 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}
