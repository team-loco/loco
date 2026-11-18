package testres

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestConcurrentLeasesNeverShareAPort(t *testing.T) {
	root := t.TempDir()
	const runs, perRun = 8, 25
	reserved := make([][]int, runs)
	var wg sync.WaitGroup
	for i := range runs {
		wg.Go(func() {
			registry, err := Open(root)
			if err != nil {
				t.Error(err)
				return
			}
			lease, err := registry.New(t.Context(), "run", Owner{TTL: time.Hour})
			if err != nil {
				t.Error(err)
				return
			}
			if reserved[i], err = lease.Ports(t.Context(), perRun); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	seen := map[int]bool{}
	for _, ports := range reserved {
		if len(ports) != perRun {
			t.Fatalf("reserved %d ports, want %d", len(ports), perRun)
		}
		for _, port := range ports {
			if seen[port] {
				t.Fatalf("port %d reserved twice", port)
			}
			seen[port] = true
		}
	}
}

func TestPortsAreStableAndFreedOnRelease(t *testing.T) {
	registry := open(t)
	lease, err := registry.New(t.Context(), "run", Owner{TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	first, err := lease.Ports(t.Context(), 2)
	if err != nil {
		t.Fatal(err)
	}
	again, err := lease.Ports(t.Context(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(first, again[:2]) {
		t.Fatalf("ports changed from %v to %v", first, again)
	}
	if taken, err := registry.portTaken(first[0]); err != nil || !taken {
		t.Fatalf("held port taken = %v, %v", taken, err)
	}
	if err := lease.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	if taken, err := registry.portTaken(first[0]); err != nil || taken {
		t.Fatalf("released port taken = %v, %v", taken, err)
	}
}

func TestReapStopsResourcesOfGoneOwnersOnly(t *testing.T) {
	registry := open(t)
	clock := time.Unix(1_000_000, 0)
	registry.now = func() time.Time { return clock }
	owner := exec.CommandContext(t.Context(), "sleep", "60")
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	ownerPath := t.TempDir()

	gone := lease(t, registry, "gone", Owner{PID: owner.Process.Pid})
	goneServer := serve(t, gone)
	living := lease(t, registry, "living", Owner{PID: os.Getpid()})
	livingServer := serve(t, living)
	pathOwned := lease(t, registry, "path", Owner{Path: ownerPath})
	expiring := lease(t, registry, "expiring", Owner{TTL: time.Hour})

	if err := owner.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Wait(); !isExit(err) {
		t.Fatalf("wait for the killed owner: %v", err)
	}
	clock = clock.Add(59 * time.Minute)
	if err := expiring.Renew(); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(59 * time.Minute)
	released, err := registry.Reap(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(released, []string{gone.ID}) {
		t.Fatalf("released %v, want %v", released, gone.ID)
	}
	if ids := ids(t, registry); !slices.Equal(ids, sorted(expiring.ID, living.ID, pathOwned.ID)) {
		t.Fatalf("leases after reap = %v", ids)
	}
	if err := goneServer.Wait(); !isExit(err) {
		t.Fatalf("the gone owner's process: %v", err)
	}
	if started, err := startTime(t.Context(), livingServer.Process.Pid); err != nil || started == "" {
		t.Fatalf("the living owner's process stopped: %v", err)
	}

	clock = clock.Add(2 * time.Minute)
	if err := os.Remove(ownerPath); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Reap(t.Context()); err != nil {
		t.Fatal(err)
	}
	if ids := ids(t, registry); !slices.Equal(ids, []string{living.ID}) {
		t.Fatalf("leases after expiry and path removal = %v", ids)
	}
}

func TestClaimBelongsToOnePath(t *testing.T) {
	registry := open(t)
	path := t.TempDir()
	first, err := registry.Claim(t.Context(), "run", Owner{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if err = first.Add(t.Context(), Container, "run-db"); err != nil {
		t.Fatal(err)
	}
	again, err := registry.Claim(t.Context(), "run", Owner{Path: path})
	if err != nil {
		t.Fatalf("reclaim from the same path: %v", err)
	}
	if err = again.Add(t.Context(), Container, "run-db"); err != nil {
		t.Fatal(err)
	}
	if rec, readErr := registry.read("run"); readErr != nil || len(rec.Resources) != 1 {
		t.Fatalf("resources after recording one twice = %v, %v", rec.Resources, readErr)
	}
	if _, err = registry.Claim(t.Context(), "run", Owner{Path: t.TempDir()}); !errors.Is(err, errClaimed) {
		t.Fatalf("claim from another path = %v, want %v", err, errClaimed)
	}
}

func TestDefaultRegistryIgnoresTMPDIR(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	first, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", t.TempDir())
	second, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if first.root != second.root {
		t.Fatalf("registry moved with TMPDIR: %s, %s", first.root, second.root)
	}
}

func open(t *testing.T) *Registry {
	t.Helper()
	registry, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func lease(t *testing.T, registry *Registry, name string, owner Owner) *Lease {
	t.Helper()
	lease, err := registry.New(t.Context(), name, owner)
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

// serve starts a process group the lease owns.
func serve(t *testing.T, lease *Lease) *exec.Cmd {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Add(t.Context(), Process, strconv.Itoa(cmd.Process.Pid)); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func ids(t *testing.T, registry *Registry) []string {
	t.Helper()
	leases, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(leases))
	for _, lease := range leases {
		ids = append(ids, lease.ID)
	}
	return ids
}

func sorted(ids ...string) []string {
	slices.Sort(ids)
	return ids
}

func TestReleaseRemovesTheLeaseDirectory(t *testing.T) {
	registry := open(t)
	lease := lease(t, registry, "run", Owner{TTL: time.Hour})
	if err := os.WriteFile(filepath.Join(lease.Dir(), "state.json"), []byte("{}"), fileMode); err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lease.Dir()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lease directory after release: %v", err)
	}
}

func TestReleaseRemovesARecordedWorktree(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", repo},
		{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.test", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		if out, err := exec.CommandContext(t.Context(), "git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	registry := open(t)
	lease := lease(t, registry, "run", Owner{TTL: time.Hour})
	worktree := filepath.Join(lease.Dir(), "src")
	if err := lease.Add(t.Context(), Worktree, worktree); err != nil {
		t.Fatal(err)
	}
	add := exec.CommandContext(t.Context(), "git", "-C", repo, "worktree", "add", "-q", "--detach", worktree)
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("add worktree: %v: %s", err, out)
	}
	if err := lease.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), "git", "-C", repo, "worktree", "list", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), worktree) {
		t.Fatalf("worktree %s still registered:\n%s", worktree, out)
	}
}

// isExit reports whether a process ended, as a killed one does, with an exit error.
func isExit(err error) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit)
}

func TestNewLeavesNothingForAnOwnerThatIsNotRunning(t *testing.T) {
	owner := exec.CommandContext(t.Context(), "true")
	if err := owner.Run(); err != nil {
		t.Fatal(err)
	}
	registry := open(t)
	if _, err := registry.New(t.Context(), "run", Owner{PID: owner.Process.Pid}); err == nil {
		t.Fatal("leased to a process that is not running")
	}
	entries, err := os.ReadDir(filepath.Join(registry.root, leasesDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("lease directories left behind: %v", entries)
	}
}
