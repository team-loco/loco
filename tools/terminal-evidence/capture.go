//go:build darwin || linux

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

func capture(ctx context.Context, opts captureOptions, theme terminalTheme) (screen *vt.Emulator, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, opts.timeout)
	defer cancel()

	scratch, err := os.MkdirTemp("", "loco-terminal-")
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(scratch)) }()

	em := vt.NewEmulator(opts.cols, opts.rows)
	em.SetDefaultForegroundColor(theme.foreground)
	em.SetDefaultBackgroundColor(theme.background)
	em.SetForegroundColor(theme.foreground)
	em.SetBackgroundColor(theme.background)

	// Close the input pipe before the emulator itself to stop the reply reader safely.
	pipe, ok := em.InputPipe().(io.Closer)
	if !ok {
		return nil, errors.New("emulator input pipe is not closable")
	}
	defer func() { resultErr = errors.Join(resultErr, em.Close()) }()

	cmd := exec.CommandContext(ctx, opts.command[0], opts.command[1:]...)
	// The capture owns cancellation of the whole terminal session, not just its leader.
	cmd.Cancel = nil
	cmd.Env = terminalEnv(theme, scratch)
	master, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(opts.rows), Cols: uint16(opts.cols)})
	if err != nil {
		return nil, fmt.Errorf("start command in terminal: %w", err)
	}

	pid := cmd.Process.Pid
	finished := false
	fd := int(master.Fd())
	if err := unix.SetNonblock(fd, true); err != nil {
		return nil, errors.Join(err, killGroup(pid, false), cmd.Wait(), master.Close())
	}

	// Drain the emulator's terminal query replies to the PTY, never to the host terminal.
	replies := make(chan error, 1)
	go func() { replies <- forwardReplies(fd, em) }()
	defer func() {
		// Stop the entire group before reaping its leader so the PID cannot be reused.
		killErr := killGroup(pid, finished)
		waitErr := cmd.Wait()
		if finished {
			resultErr = errors.Join(resultErr, checkExit(cmd.ProcessState, waitErr, opts.expectExit))
		}
		pipeErr := pipe.Close()
		replyErr := <-replies
		if errors.Is(replyErr, syscall.EIO) {
			replyErr = nil
		}
		resultErr = errors.Join(resultErr, killErr, pipeErr, replyErr, master.Close())
	}()

	buf := make([]byte, 32768)
	for {
		if err := pollTerminal(ctx, master); err != nil {
			return nil, fmt.Errorf("wait for terminal: %w", err)
		}
		n, readErr := unix.Read(fd, buf)
		if errors.Is(readErr, syscall.EAGAIN) || errors.Is(readErr, syscall.EINTR) {
			continue
		}
		if n > 0 {
			if _, err := em.Write(buf[:n]); err != nil {
				return nil, fmt.Errorf("emulate screen: %w", err)
			}
			if opts.until != "" && strings.Contains(em.String(), opts.until) {
				return em, nil
			}
		}
		if n > 0 && readErr == nil {
			continue
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, syscall.EIO) {
			return nil, fmt.Errorf("read terminal: %w", readErr)
		}
		if opts.until != "" {
			return nil, fmt.Errorf("command exited before showing %q", opts.until)
		}
		if err := waitExited(ctx, pid); err != nil {
			return nil, fmt.Errorf("wait for command exit: %w", err)
		}
		finished = true
		return em, nil
	}
}

// Keep reading even if the child stops accepting replies, so terminal parsing cannot block.
func forwardReplies(fd int, em *vt.Emulator) error {
	buf := make([]byte, 4096)
	var writeErr error
	for {
		n, readErr := em.Read(buf)
		if n > 0 && writeErr == nil {
			written, err := unix.Write(fd, buf[:n])
			writeErr = err
			if err == nil && written != n {
				writeErr = io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			return writeErr
		}
		if readErr != nil {
			return errors.Join(writeErr, readErr)
		}
	}
}

func terminalEnv(theme terminalTheme, scratch string) []string {
	env := make([]string, 0, len(os.Environ())+4)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "NO_COLOR", "TERM", "COLORTERM", "COLORFGBG", "TMPDIR":
		default:
			env = append(env, entry)
		}
	}
	return append(env, "TERM=xterm-256color", "COLORTERM=truecolor", "COLORFGBG="+theme.colorFGBG, "TMPDIR="+scratch)
}

func pollTerminal(ctx context.Context, master *os.File) error {
	fd := []unix.PollFd{{Fd: int32(master.Fd()), Events: unix.POLLIN}}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := unix.Poll(fd, 100)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if n != 0 {
			return nil
		}
	}
}

func killGroup(pid int, exited bool) error {
	err := unix.Kill(-pid, unix.SIGKILL)
	// Darwin may report EPERM when the group contains only an unreaped zombie.
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if errors.Is(err, syscall.EPERM) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		if exited || waitExited(ctx, pid) == nil {
			return nil
		}
	}
	return err
}

func checkExit(state *os.ProcessState, waitErr error, want int) error {
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		return waitErr
	}
	if state.ExitCode() != want {
		return fmt.Errorf("command exited %d, expected %d", state.ExitCode(), want)
	}
	return nil
}
