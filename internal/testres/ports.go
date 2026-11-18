package testres

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// lowestPort starts the reserved range: below it sit well-known services, and from the
// ephemeral range up the kernel and the container engine assign ports themselves.
const lowestPort = 20000

// containerEphemeralStart is where a Linux container engine starts publishing ports, whatever the host's range.
const containerEphemeralStart = 32768

var errNoPorts = errors.New("no free ports left below the ephemeral range")

// Ports returns n ports reserved for the lease, the same ones on every call, reserving more when
// it holds fewer. No other lease gets them until this one is released, and nothing listened on
// them when they were reserved.
func (l *Lease) Ports(ctx context.Context, n int) ([]int, error) {
	highest, err := firstEphemeralPort(ctx)
	if err != nil {
		return nil, err
	}
	var ports []int
	err = l.update(func(rec *record) error {
		start := lowestPort + rand.IntN(highest-lowestPort) //nolint:gosec // spreads concurrent runs; not a secret
		for offset := 0; len(rec.Ports) < n; offset++ {
			if offset == highest-lowestPort {
				return errNoPorts
			}
			port := lowestPort + (start-lowestPort+offset)%(highest-lowestPort)
			taken, takenErr := l.r.portTaken(port)
			if takenErr != nil {
				return takenErr
			}
			if taken || listening(ctx, port) {
				continue
			}
			if writeErr := os.WriteFile(l.r.portPath(port), []byte(l.ID), fileMode); writeErr != nil {
				return fmt.Errorf("reserve port %d: %w", port, writeErr)
			}
			rec.Ports = append(rec.Ports, port)
		}
		ports = rec.Ports[:n]
		return nil
	})
	return ports, err
}

// portTaken reports whether a lease holds the port. A reservation whose lease is gone is free.
func (r *Registry) portTaken(port int) (bool, error) {
	owner, err := os.ReadFile(r.portPath(port))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read port %d: %w", port, err)
	}
	_, err = os.Stat(r.dir(string(owner)))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read port %d: %w", port, err)
	}
	return true, nil
}

func (r *Registry) portPath(port int) string {
	return filepath.Join(r.root, portsDir, strconv.Itoa(port))
}

func listening(ctx context.Context, port int) bool {
	var config net.ListenConfig
	for _, host := range []string{"127.0.0.1", ""} {
		listener, err := config.Listen(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil || listener.Close() != nil {
			return true
		}
	}
	return false
}

func firstEphemeralPort(ctx context.Context) (int, error) {
	text, err := os.ReadFile("/proc/sys/net/ipv4/ip_local_port_range")
	if err != nil {
		text, err = exec.CommandContext(ctx, "sysctl", "-n", "net.inet.ip.portrange.first").Output()
	}
	if err != nil {
		return 0, fmt.Errorf("read the ephemeral port range: %w", err)
	}
	fields := strings.Fields(string(text))
	if len(fields) == 0 {
		return 0, fmt.Errorf("read the ephemeral port range: empty %q", text)
	}
	first, err := strconv.Atoi(fields[0])
	if err != nil || first <= lowestPort {
		return 0, fmt.Errorf("ephemeral ports start at %q, not above %d", fields[0], lowestPort)
	}
	return min(first, containerEphemeralStart), nil
}
