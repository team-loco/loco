package cluster

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	podresource "k8s.io/component-helpers/resource"

	"github.com/team-loco/loco/agent/pkg/kube"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	"github.com/team-loco/loco/internal/workloadplatform"
)

var errCapacityNotReady = errors.New("capacity watches have not synced")

const capacityListPageSize = 500

type capacityWatch struct {
	nodes, pods *capacityStore
	ctx         context.Context
	stop        context.CancelFunc
	done        chan struct{}
	ready       chan struct{}
}

// StartCapacity waits for complete snapshots before the agent reports capacity.
// Watches use their own client because the inspector's short request timeout would
// otherwise disconnect them repeatedly.
func (i *Inspector) StartCapacity(ctx context.Context, cfg *rest.Config) error {
	watchConfig := rest.CopyConfig(cfg)
	watchConfig.Timeout = 0
	client, err := kubernetes.NewForConfig(watchConfig)
	if err != nil {
		return fmt.Errorf("create capacity client: %w", err)
	}
	return i.startCapacity(ctx, client)
}

func (i *Inspector) startCapacity(parent context.Context, client kubernetes.Interface) error {
	ctx, stop := context.WithCancel(parent)
	ctx, onWatchError := kube.StopOnDeadWatch(ctx)
	w := &capacityWatch{ctx: ctx, stop: stop, done: make(chan struct{}), ready: make(chan struct{}),
		nodes: newCapacityStore(), pods: newCapacityStore()}
	i.capacity = w

	var running sync.WaitGroup
	start := func(lw *cache.ListWatch, obj runtime.Object, store *capacityStore) {
		var reflector *cache.Reflector
		fatal := func(err error) {
			if apierrors.IsForbidden(err) || apierrors.IsNotFound(err) {
				onWatchError(ctx, reflector, err)
			}
		}
		list := lw.ListWithContextFunc
		lw.ListWithContextFunc = func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
			// The watch cache ignores Limit at RV=0. A consistent snapshot permits
			// paging; client-go takes its watch version from the completed result.
			if opts.ResourceVersion == "0" {
				opts.ResourceVersion = ""
			}
			// The pager retries an expired continuation with an unbounded request.
			// Return expiration instead so the reflector starts a new paged snapshot.
			if opts.Limit <= 0 {
				return nil, apierrors.NewResourceExpired("capacity snapshot must restart with pagination")
			}
			result, err := list(ctx, opts)
			fatal(err)
			if err != nil {
				return nil, err
			}
			return compactCapacityList(result)
		}
		watchResource := lw.WatchFuncWithContext
		lw.WatchFuncWithContext = func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
			stream, err := watchResource(ctx, opts)
			if err != nil {
				fatal(err)
				return nil, err
			}
			output := make(chan watch.Event)
			proxy := watch.NewProxyWatcher(output)
			running.Go(func() {
				defer close(output)
				defer stream.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-proxy.StopChan():
						return
					case event, ok := <-stream.ResultChan():
						if !ok {
							return
						}
						if event.Type == watch.Error {
							fatal(apierrors.FromObject(event.Object))
						}
						select {
						case output <- event:
						case <-ctx.Done():
							return
						case <-proxy.StopChan():
							return
						}
					}
				}
			})
			return proxy, nil
		}
		reflector = cache.NewReflector(cache.ToListWatcherWithWatchListSemantics(lw, client), obj, store, 0)
		reflector.WatchListPageSize = capacityListPageSize
		running.Go(func() { reflector.RunWithContext(ctx) })
	}
	nodes := client.CoreV1().Nodes()
	start(&cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
			return nodes.List(ctx, opts)
		},
		WatchFuncWithContext: nodes.Watch,
	}, &corev1.Node{}, w.nodes)
	pods := client.CoreV1().Pods(metav1.NamespaceAll)
	start(&cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
			opts.FieldSelector = activePodsSelector
			return pods.List(ctx, opts)
		},
		WatchFuncWithContext: func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
			opts.FieldSelector = activePodsSelector
			return pods.Watch(ctx, opts)
		},
	}, &corev1.Pod{}, w.pods)
	go func() {
		running.Wait()
		close(w.done)
	}()
	for _, ready := range []<-chan struct{}{w.nodes.synced, w.pods.synced} {
		select {
		case <-ready:
		case <-ctx.Done():
			i.StopCapacity()
			return fmt.Errorf("sync capacity: %w", context.Cause(ctx))
		}
	}
	if err := context.Cause(ctx); err != nil {
		i.StopCapacity()
		return fmt.Errorf("sync capacity: %w", err)
	}
	close(w.ready)
	return nil
}

func (i *Inspector) StopCapacity() {
	if i.capacity != nil {
		i.capacity.stop()
		<-i.capacity.done
	}
}

func (i *Inspector) WaitCapacity(ctx context.Context) error {
	var stopped <-chan struct{}
	if i.capacity != nil {
		stopped = i.capacity.ctx.Done()
	}
	select {
	case <-stopped:
		<-i.capacity.done
		return context.Cause(i.capacity.ctx)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (i *Inspector) Capacity(ctx context.Context) (*agentv1.AgentCapacity, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if i.capacity == nil {
		return nil, errCapacityNotReady
	}
	if err := context.Cause(i.capacity.ctx); err != nil {
		return nil, err
	}
	select {
	case <-i.capacity.ready:
	default:
		return nil, errCapacityNotReady
	}
	nodes, platforms := i.capacity.nodes.snapshot()
	pods, _ := i.capacity.pods.snapshot()
	return &agentv1.AgentCapacity{
		CpuMillicoresTotal: nodes.cpu, MemoryBytesTotal: nodes.memory, PodsTotal: int32(nodes.pods),
		CpuMillicoresRequested: pods.cpu, MemoryBytesRequested: pods.memory,
		PodsAssigned: int32(pods.pods), PodsRunning: int32(pods.running), Platforms: platforms,
	}, nil
}

type capacityAmount struct {
	cpu, memory, pods, running int64
}

func (a *capacityAmount) add(b capacityAmount, sign int64) {
	a.cpu += sign * b.cpu
	a.memory += sign * b.memory
	a.pods += sign * b.pods
	a.running += sign * b.running
}

type capacityRecord struct {
	uid      types.UID
	amount   capacityAmount
	platform string
}

// Only the initial streaming snapshot needs metadata for client-go's temporary
// store. The long-lived store keeps compact contributions, never Kubernetes objects.
type capacityObject struct {
	metav1.TypeMeta
	metav1.ObjectMeta
	record capacityRecord
}

func (o *capacityObject) DeepCopyObject() runtime.Object {
	cloned := *o
	return &cloned
}

// The fallback LIST is paged and compacted before client-go assembles the full
// snapshot, so a relist also avoids retaining all full Pod objects at once.
func compactCapacityList(obj runtime.Object) (runtime.Object, error) {
	metadata, err := meta.ListAccessor(obj)
	if err != nil {
		return nil, err
	}
	objects, err := meta.ExtractList(obj)
	if err != nil {
		return nil, err
	}
	list := &metav1.List{
		ResourceVersion: metadata.GetResourceVersion(), Continue: metadata.GetContinue(),
		Items: make([]runtime.RawExtension, 0, len(objects)),
	}
	for _, object := range objects {
		compacted, err := capacityContribution(object)
		if err != nil {
			return nil, err
		}
		list.Items = append(list.Items, runtime.RawExtension{Object: compacted})
	}
	return list, nil
}

type capacityStore struct {
	mu        sync.RWMutex
	records   map[string]capacityRecord
	total     capacityAmount
	platforms map[string]int
	synced    chan struct{}
	once      sync.Once
}

func newCapacityStore() *capacityStore {
	return &capacityStore{records: make(map[string]capacityRecord), platforms: make(map[string]int),
		synced: make(chan struct{})}
}

func (*capacityStore) Transformer() cache.TransformFunc {
	return func(obj any) (any, error) { return capacityContribution(obj) }
}

func capacityContribution(obj any) (*capacityObject, error) {
	if record, ok := obj.(*capacityObject); ok {
		return record, nil
	}
	var metadata metav1.ObjectMeta
	var record capacityRecord
	switch obj := obj.(type) {
	case *corev1.Pod:
		metadata = metav1.ObjectMeta{
			Name: obj.Name, Namespace: obj.Namespace, UID: obj.UID, ResourceVersion: obj.ResourceVersion,
		}
		if obj.Spec.NodeName != "" && obj.Status.Phase != corev1.PodSucceeded && obj.Status.Phase != corev1.PodFailed {
			requests := podresource.PodRequests(obj, podresource.PodResourcesOptions{
				UseStatusResources: true, InPlacePodLevelResourcesVerticalScalingEnabled: true,
			})
			record.amount = capacityAmount{cpu: requests.Cpu().MilliValue(), memory: requests.Memory().Value(), pods: 1}
			if obj.Status.Phase == corev1.PodRunning {
				record.amount.running = 1
			}
		}
	case *corev1.Node:
		metadata = metav1.ObjectMeta{Name: obj.Name, UID: obj.UID, ResourceVersion: obj.ResourceVersion}
		record.amount = capacityAmount{cpu: obj.Status.Allocatable.Cpu().MilliValue(),
			memory: obj.Status.Allocatable.Memory().Value(), pods: obj.Status.Allocatable.Pods().Value()}
		platform, err := workloadplatform.Parse(
			obj.Status.NodeInfo.OperatingSystem + "/" + obj.Status.NodeInfo.Architecture,
		)
		if err == nil {
			record.platform = string(platform)
		}
	default:
		return nil, fmt.Errorf("unexpected capacity object %T", obj)
	}
	record.uid = metadata.UID
	return &capacityObject{ObjectMeta: metadata, record: record}, nil
}

func (s *capacityStore) Add(obj any) error { return s.Update(obj) }

func (s *capacityStore) Update(obj any) error {
	value, err := capacityContribution(obj)
	if err != nil {
		return err
	}
	entry := value
	key, err := cache.MetaNamespaceKeyFunc(entry)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.remove(key)
	s.records[key] = entry.record
	s.total.add(entry.record.amount, 1)
	if entry.record.platform != "" {
		s.platforms[entry.record.platform]++
	}
	return nil
}

func (s *capacityStore) Delete(obj any) error {
	metadata, err := meta.Accessor(obj)
	if err != nil {
		return err
	}
	key, err := cache.MetaNamespaceKeyFunc(metadata)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.records[key]; ok && old.uid == metadata.GetUID() {
		s.remove(key)
	}
	return nil
}

func (s *capacityStore) remove(key string) {
	old, ok := s.records[key]
	if !ok {
		return
	}
	delete(s.records, key)
	s.total.add(old.amount, -1)
	if old.platform != "" {
		s.platforms[old.platform]--
		if s.platforms[old.platform] == 0 {
			delete(s.platforms, old.platform)
		}
	}
}

func (s *capacityStore) Replace(objects []any, _ string) error {
	replacement := newCapacityStore()
	for _, obj := range objects {
		if err := replacement.Add(obj); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.records, s.total, s.platforms = replacement.records, replacement.total, replacement.platforms
	s.mu.Unlock()
	s.once.Do(func() { close(s.synced) })
	return nil
}

func (*capacityStore) Resync() error { return nil }

func (s *capacityStore) snapshot() (capacityAmount, []string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	platforms := make([]string, 0, len(s.platforms))
	for platform := range s.platforms {
		platforms = append(platforms, platform)
	}
	slices.Sort(platforms)
	return s.total, platforms
}
