// Command testres gives shell and Python test harnesses the shared registry of internal/testres:
//
//	testres lease -name NAME [-pid PID] [-ttl DURATION]  reap, then print a new lease's directory
//	testres add DIR KIND NAME                             record a resource before creating it
//	testres ports DIR N                                   print the lease's N reserved ports on one line
//	testres renew DIR...                                  extend leases by their TTL
//	testres release DIR                                   remove a lease and its resources
//	testres reap                                          release every lease whose owner is gone
//	testres list                                          print the directory of every lease
//
// DIR is a lease directory as lease prints it.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/team-loco/loco/internal/testres"
)

var errUsage = errors.New("usage: testres lease|add|ports|renew|release|reap|list; see the package documentation")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, os.Args[1:])
	stop()
	if err != nil {
		slog.ErrorContext(ctx, "testres", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	registry, err := testres.Default()
	if err != nil {
		return err
	}
	command, args := args[0], args[1:]
	switch {
	case command == "lease":
		return newLease(ctx, registry, args)
	case command == "add" && len(args) == 3:
		lease, err := lookup(registry, args[0])
		if err != nil {
			return err
		}
		return lease.Add(ctx, testres.Kind(args[1]), args[2])
	case command == "ports" && len(args) == 2:
		return printPorts(ctx, registry, args[0], args[1])
	case command == "renew":
		for _, dir := range args {
			lease, err := lookup(registry, dir)
			if err != nil {
				return err
			}
			if err := lease.Renew(); err != nil {
				return err
			}
		}
		return nil
	case command == "release" && len(args) == 1:
		lease, err := lookup(registry, args[0])
		if err != nil {
			return err
		}
		return lease.Release(ctx)
	case command == "reap" && len(args) == 0:
		return reap(ctx, registry)
	case command == "list" && len(args) == 0:
		leases, err := registry.List()
		if err != nil {
			return err
		}
		for _, lease := range leases {
			fmt.Println(lease.Dir())
		}
		return nil
	default:
		return errUsage
	}
}

func newLease(ctx context.Context, registry *testres.Registry, args []string) error {
	flags := flag.NewFlagSet("lease", flag.ContinueOnError)
	name := flags.String("name", "", "prefix of the lease ID, which names the run's resources")
	var owner testres.Owner
	flags.IntVar(&owner.PID, "pid", 0, "keep the lease while this process runs")
	flags.DurationVar(&owner.TTL, "ttl", 0, "keep the lease this long after creation or renewal")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *name == "" || flags.NArg() > 0 {
		return errUsage
	}
	lease, err := registry.New(ctx, *name, owner)
	if err != nil {
		return err
	}
	fmt.Println(lease.Dir())
	return nil
}

func reap(ctx context.Context, registry *testres.Registry) error {
	released, err := registry.Reap(ctx)
	for _, id := range released {
		slog.InfoContext(ctx, "released a lease whose owner is gone", "lease", id)
	}
	return err
}

func printPorts(ctx context.Context, registry *testres.Registry, dir, count string) error {
	n, err := strconv.Atoi(count)
	if err != nil || n <= 0 {
		return errUsage
	}
	lease, err := lookup(registry, dir)
	if err != nil {
		return err
	}
	ports, err := lease.Ports(ctx, n)
	if err != nil {
		return err
	}
	fields := make([]string, len(ports))
	for i, port := range ports {
		fields[i] = strconv.Itoa(port)
	}
	fmt.Println(strings.Join(fields, " "))
	return nil
}

func lookup(registry *testres.Registry, dir string) (*testres.Lease, error) {
	return registry.Get(filepath.Base(dir))
}
