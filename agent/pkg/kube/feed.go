package kube

import (
	"sync"

	"google.golang.org/protobuf/proto"
)

// Feed keeps the latest status of every watched object and forwards each change to the attached
// sink, so a reconnected sync stream can replay what the cluster last reported.
type Feed[T proto.Message] struct {
	// order serializes forwarding with Attach's replay and with Freeze, so a sink never receives
	// a status out of order and an inventory sent under Freeze is never older than a status
	// already forwarded.
	order    sync.Mutex
	mu       sync.Mutex
	statuses map[string]T
	sink     func(T)
	sinkID   uint64
}

func NewFeed[T proto.Message]() *Feed[T] {
	return &Feed[T]{statuses: make(map[string]T)}
}

// Attach replays every known status to sink, forwards later changes to it in place of the
// previous sink, and returns a function that detaches it.
func (f *Feed[T]) Attach(sink func(T)) func() {
	f.order.Lock()
	defer f.order.Unlock()

	f.mu.Lock()
	f.sinkID++
	id := f.sinkID
	f.sink = sink
	snapshot := make([]T, 0, len(f.statuses))
	for _, status := range f.statuses {
		snapshot = append(snapshot, status)
	}
	f.mu.Unlock()

	for _, status := range snapshot {
		sink(status)
	}

	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.sinkID == id {
			f.sink = nil
		}
	}
}

// Publish records status as the latest for key and forwards it unless it equals the previous one.
func (f *Feed[T]) Publish(key string, status T) {
	f.order.Lock()
	defer f.order.Unlock()

	f.mu.Lock()
	previous, ok := f.statuses[key]
	if ok && proto.Equal(previous, status) {
		f.mu.Unlock()
		return
	}
	f.statuses[key] = status
	sink := f.sink
	f.mu.Unlock()

	if sink != nil {
		sink(status)
	}
}

func (f *Feed[T]) Forget(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.statuses, key)
}

// Freeze runs fn while no status is forwarded or replayed.
func (f *Feed[T]) Freeze(fn func() error) error {
	f.order.Lock()
	defer f.order.Unlock()
	return fn()
}
