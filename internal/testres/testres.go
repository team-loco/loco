// Package testres records which local test run owns each container, compose project, kind
// cluster, worktree, process and port a test harness creates, and removes them once that run is
// gone. Every harness on a machine shares one registry, so concurrent runs never get the same
// port or name and never remove each other's resources.
package testres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"
)

// Kind is the kind of a resource a lease owns; it decides how release removes it.
type Kind string

const (
	// Container is a container name, removed with its anonymous volumes.
	Container Kind = "container"
	// Compose is a compose project name, removed with its volumes and networks.
	Compose Kind = "compose"
	// KindCluster is a kind cluster name.
	KindCluster Kind = "kind"
	// Worktree is the path of a git worktree.
	Worktree Kind = "worktree"
	// Process is the ID of a process group leader, stopped only while it is the same process.
	Process Kind = "process"
)

// Owner says how long a lease lives: while any of its set fields holds.
type Owner struct {
	// PID keeps the lease while this process runs.
	PID int
	// Path keeps the lease while this path exists.
	Path string
	// TTL keeps the lease until TTL after its creation or last renewal.
	TTL time.Duration
}

const (
	registryName = "loco-test-resources"
	leasesDir    = "leases"
	portsDir     = "ports"
	leaseFile    = "lease.json"
	lockFile     = "lock"
	idBytes      = 4
	dirMode      = 0o755
	fileMode     = 0o644
)

var (
	errNoOwner = errors.New("a lease needs a PID, a path or a TTL")
	errClaimed = errors.New("lease is owned by another path")
	errUnknown = errors.New("no such lease")
)

// Registry is a directory of leases and port reservations.
type Registry struct {
	root string
	now  func() time.Time
}

// Default opens the machine-wide registry in the user's cache directory, which does not depend on
// TMPDIR or the working directory.
func Default() (*Registry, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("find cache directory: %w", err)
	}
	return Open(filepath.Join(cache, registryName))
}

// Open opens the registry at root, creating it when missing.
func Open(root string) (*Registry, error) {
	for _, dir := range []string{leasesDir, portsDir} {
		if err := os.MkdirAll(filepath.Join(root, dir), dirMode); err != nil {
			return nil, fmt.Errorf("create registry: %w", err)
		}
	}
	return &Registry{root: root, now: time.Now}, nil
}

// Lease is a run's claim on the resources and ports recorded under its ID.
type Lease struct {
	r  *Registry
	ID string
}

type record struct {
	ID        string     `json:"id"`
	PID       int        `json:"pid,omitempty"`
	Started   string     `json:"started,omitempty"`
	Path      string     `json:"path,omitempty"`
	TTL       int64      `json:"ttl,omitempty"`
	Expires   int64      `json:"expires,omitempty"`
	Releasing bool       `json:"releasing,omitempty"`
	Resources []resource `json:"resources,omitempty"`
	Ports     []int      `json:"ports,omitempty"`
}

type resource struct {
	Kind    Kind   `json:"kind"`
	Name    string `json:"name"`
	Started string `json:"started,omitempty"`
}

// New releases the leases whose owner is gone, then creates a lease with a unique ID that starts
// with name.
func (r *Registry) New(ctx context.Context, name string, owner Owner) (*Lease, error) {
	if owner.PID == 0 && owner.Path == "" && owner.TTL <= 0 {
		return nil, errNoOwner
	}
	r.reapLeftovers(ctx)
	var id string
	err := r.locked(func() error {
		for {
			suffix := make([]byte, idBytes)
			if _, err := rand.Read(suffix); err != nil {
				return fmt.Errorf("random lease id: %w", err)
			}
			id = name + "-" + hex.EncodeToString(suffix)
			err := os.Mkdir(r.dir(id), dirMode)
			if errors.Is(err, fs.ErrExist) {
				continue
			}
			if err != nil {
				return fmt.Errorf("create lease: %w", err)
			}
			return r.create(id, owner)
		}
	})
	if err != nil {
		return nil, err
	}
	return &Lease{r: r, ID: id}, nil
}

// Claim releases the leases whose owner is gone, then returns the lease with a fixed ID, owned by
// owner.Path, creating it when missing. It fails when another path owns the ID.
func (r *Registry) Claim(ctx context.Context, id string, owner Owner) (*Lease, error) {
	if owner.Path == "" {
		return nil, errNoOwner
	}
	r.reapLeftovers(ctx)
	err := r.locked(func() error {
		rec, err := r.read(id)
		if errors.Is(err, fs.ErrNotExist) {
			if mkdirErr := os.MkdirAll(r.dir(id), dirMode); mkdirErr != nil {
				return fmt.Errorf("create lease: %w", mkdirErr)
			}
			return r.create(id, owner)
		}
		if err != nil {
			return err
		}
		if rec.Path != owner.Path || rec.Releasing {
			return fmt.Errorf("%s: %w %s", id, errClaimed, rec.Path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &Lease{r: r, ID: id}, nil
}

// reapLeftovers reaps before a new run starts. A leftover that cannot be removed now stays for a
// later reap and does not stop the new run.
func (r *Registry) reapLeftovers(ctx context.Context) {
	released, err := r.Reap(ctx)
	for _, id := range released {
		slog.InfoContext(ctx, "released the leftovers of a test run whose owner is gone", "lease", id)
	}
	if err != nil {
		slog.WarnContext(ctx, "reap leftovers of earlier test runs", "error", err)
	}
}

// Get returns the existing lease with an ID.
func (r *Registry) Get(id string) (*Lease, error) {
	if _, err := r.read(id); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%s: %w", id, errUnknown)
		}
		return nil, err
	}
	return &Lease{r: r, ID: id}, nil
}

// List returns every lease that is not being released.
func (r *Registry) List() ([]*Lease, error) {
	records, err := r.records()
	if err != nil {
		return nil, err
	}
	var leases []*Lease
	for _, rec := range records {
		if !rec.Releasing {
			leases = append(leases, &Lease{r: r, ID: rec.ID})
		}
	}
	return leases, nil
}

// Reap releases every lease whose owner is gone or whose TTL ran out, finishes releases that were
// interrupted, and returns the IDs of the leases it released.
func (r *Registry) Reap(ctx context.Context) ([]string, error) {
	var dead []string
	err := r.locked(func() error {
		records, err := r.records()
		if err != nil {
			return err
		}
		var errs []error
		for _, rec := range records {
			alive, err := r.alive(ctx, rec)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if alive {
				continue
			}
			rec.Releasing = true
			if err := r.write(rec); err != nil {
				errs = append(errs, err)
				continue
			}
			dead = append(dead, rec.ID)
		}
		return errors.Join(errs...)
	})
	var released []string
	for _, id := range dead {
		if removeErr := r.remove(ctx, id); removeErr != nil {
			err = errors.Join(err, removeErr)
			continue
		}
		released = append(released, id)
	}
	return released, err
}

// Dir is the lease's directory, where its owner may keep files; release removes it.
func (l *Lease) Dir() string {
	return l.r.dir(l.ID)
}

// Add records a resource before its owner creates it, so a reaper finds it even when the owner
// dies mid-creation. Adding a recorded resource again changes nothing.
func (l *Lease) Add(ctx context.Context, kind Kind, name string) error {
	added := resource{Kind: kind, Name: name}
	if kind == Process {
		pid, err := parsePID(name)
		if err != nil {
			return err
		}
		if added.Started, err = startTime(ctx, pid); err != nil {
			return err
		}
	}
	return l.update(func(rec *record) error {
		if !slices.ContainsFunc(
			rec.Resources,
			func(res resource) bool { return res.Kind == kind && res.Name == name },
		) {
			rec.Resources = append(rec.Resources, added)
		}
		return nil
	})
}

// Renew extends a lease with a TTL by that TTL from now.
func (l *Lease) Renew() error {
	return l.update(func(rec *record) error {
		if rec.TTL > 0 {
			rec.Expires = l.r.now().Unix() + rec.TTL
		}
		return nil
	})
}

// Release removes the lease's resources, its directory and its ports.
func (l *Lease) Release(ctx context.Context) error {
	if err := l.update(func(rec *record) error {
		rec.Releasing = true
		return nil
	}); err != nil {
		return err
	}
	return l.r.remove(ctx, l.ID)
}

func (l *Lease) update(change func(*record) error) error {
	return l.r.locked(func() error {
		rec, err := l.r.read(l.ID)
		if err != nil {
			return err
		}
		if err := change(&rec); err != nil {
			return err
		}
		return l.r.write(rec)
	})
}

// create writes a new lease's record into its directory, or removes the directory when it cannot.
func (r *Registry) create(id string, owner Owner) error {
	if err := r.writeNew(id, owner); err != nil {
		return errors.Join(err, os.RemoveAll(r.dir(id)))
	}
	return nil
}

func (r *Registry) writeNew(id string, owner Owner) error {
	rec := record{ID: id, PID: owner.PID, Path: owner.Path, TTL: int64(owner.TTL / time.Second)}
	if rec.TTL > 0 {
		rec.Expires = r.now().Unix() + rec.TTL
	}
	if owner.PID != 0 {
		started, err := startTime(context.Background(), owner.PID)
		if err != nil {
			return err
		}
		if started == "" {
			return fmt.Errorf("owner process %d is not running", owner.PID)
		}
		rec.Started = started
	}
	return r.write(rec)
}

func (r *Registry) alive(ctx context.Context, rec record) (bool, error) {
	if rec.Releasing {
		return false, nil
	}
	if rec.Expires > r.now().Unix() {
		return true, nil
	}
	if rec.Path != "" {
		_, err := os.Stat(rec.Path)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return false, fmt.Errorf("lease %s: %w", rec.ID, err)
		}
	}
	if rec.PID != 0 {
		started, err := startTime(ctx, rec.PID)
		if err != nil {
			return false, fmt.Errorf("lease %s: %w", rec.ID, err)
		}
		return started == rec.Started, nil
	}
	return false, nil
}

// remove deletes the resources of a lease marked as releasing, newest first, then its ports and
// directory. A failure leaves the lease for the next reap.
func (r *Registry) remove(ctx context.Context, id string) error {
	rec, err := r.read(id)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, res := range slices.Backward(rec.Resources) {
		if err := removeResource(ctx, res); err != nil {
			return fmt.Errorf("release %s: %w", id, err)
		}
	}
	return r.locked(func() error {
		for _, port := range rec.Ports {
			owner, err := os.ReadFile(r.portPath(port))
			if errors.Is(err, fs.ErrNotExist) || string(owner) != id {
				continue
			}
			if err != nil {
				return fmt.Errorf("release port %d: %w", port, err)
			}
			if err := os.Remove(r.portPath(port)); err != nil {
				return fmt.Errorf("release port %d: %w", port, err)
			}
		}
		if err := os.RemoveAll(r.dir(id)); err != nil {
			return fmt.Errorf("release %s: %w", id, err)
		}
		return nil
	})
}

func (r *Registry) records() ([]record, error) {
	entries, err := os.ReadDir(filepath.Join(r.root, leasesDir))
	if err != nil {
		return nil, fmt.Errorf("list leases: %w", err)
	}
	var records []record
	for _, entry := range entries {
		rec, err := r.read(entry.Name())
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	return records, nil
}

func (r *Registry) read(id string) (record, error) {
	var rec record
	data, err := os.ReadFile(filepath.Join(r.dir(id), leaseFile))
	if err != nil {
		return rec, fmt.Errorf("read lease %s: %w", id, err)
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		return rec, fmt.Errorf("read lease %s: %w", id, err)
	}
	return rec, nil
}

func (r *Registry) write(rec record) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("write lease %s: %w", rec.ID, err)
	}
	path := filepath.Join(r.dir(rec.ID), leaseFile)
	if err := os.WriteFile(path+".tmp", data, fileMode); err != nil {
		return fmt.Errorf("write lease %s: %w", rec.ID, err)
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		return fmt.Errorf("write lease %s: %w", rec.ID, err)
	}
	return nil
}

func (r *Registry) dir(id string) string {
	return filepath.Join(r.root, leasesDir, id)
}

// locked runs fn while holding the registry's lock, which serializes every change across
// processes.
func (r *Registry) locked(fn func() error) error {
	file, err := os.OpenFile(filepath.Join(r.root, lockFile), os.O_CREATE|os.O_RDWR, fileMode)
	if err != nil {
		return fmt.Errorf("open registry lock: %w", err)
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock registry: %w", err)
	}
	return fn()
}
