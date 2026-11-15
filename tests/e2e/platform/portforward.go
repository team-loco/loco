package platform

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"testing"
)

var forwarding = regexp.MustCompile(`Forwarding from 127\.0\.0\.1:([0-9]+)`)

// PortForward forwards a free local port to remotePort of target in namespace until the test
// ends, and returns the local port.
func (p *Platform) PortForward(tb testing.TB, namespace, target string, remotePort int) int {
	tb.Helper()
	cmd := exec.CommandContext(
		tb.Context(),
		"kubectl",
		"--context",
		p.kubeContext,
		"-n",
		namespace,
		"port-forward",
		"--address",
		"127.0.0.1",
		target,
		":"+strconv.Itoa(remotePort),
	)
	cmd.Env = p.env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		tb.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			tb.Error(err)
		}
		// Kill ends it with a signal, so Wait reports that, not a failure.
		if err := cmd.Wait(); err != nil && cmd.ProcessState == nil {
			tb.Error(err)
		}
	})
	ports := make(chan int, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if match := forwarding.FindStringSubmatch(scanner.Text()); match != nil {
				port, err := strconv.Atoi(match[1])
				if err == nil {
					ports <- port
				}
				break
			}
		}
		for scanner.Scan() {
		}
	}()
	ctx, cancel := context.WithTimeout(tb.Context(), readyTimeout)
	defer cancel()
	select {
	case port := <-ports:
		return port
	case <-ctx.Done():
		tb.Fatal(
			fmt.Errorf(
				"port-forward to %s/%s within %s: %w",
				namespace,
				target,
				readyTimeout,
				ctx.Err(),
			),
		)
		return 0
	}
}
