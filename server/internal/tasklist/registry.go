package tasklist

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/cluster"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/routing"
)

// Registry manages the set of tasklist Managers owned by this matching
// service instance. It enforces ownership via the cluster hash-ring:
// for any tasklist partition, exactly one matching node is the owner,
// and all other nodes either forward or refuse.
//
// Lifecycle responsibilities:
//   - Lazy-create Manager on first AddTask / PollForTask for a partition
//     this node owns.
//   - Periodic ownership scan: drop Managers whose hash-ring ownership
//     has shifted (graceful Stop, then remove from registry).
//   - Coordinate with Membership change events (subscribed at Start).
//
// API surface (called by MatchingServiceHandler):
//   - GetOrCreateManager(id): returns the Manager, creating + starting
//     it on first use. Returns ErrNotOwner if this node does not own
//     the partition per the hash-ring.
//   - Stop: shutdown all managers concurrently and exit the scan loop.
//
// Thread-safety: managers map is guarded by a RWMutex. GetOrCreateManager
// uses double-checked locking so the common (already-exists) path takes
// only the read lock.
type Registry struct {
	cfg          config.MatchingServiceConfig
	logger       log.Logger
	store        p.TasklistStore
	runsClient   pb.RunsServiceClient
	membership   *cluster.Membership
	remoteClient *routing.RemoteClient

	// loadBalancer picks read/write partitions for incoming requests.
	loadBalancer *LoadBalancer

	mu       sync.RWMutex
	managers map[string]*Manager // key: id.String() (namespace + fullName)

	stopCh   chan struct{}
	scanDone chan struct{}
	stopped  atomic.Bool
}

// RegistryDeps groups external dependencies for testability.
type RegistryDeps struct {
	Config       config.MatchingServiceConfig
	Tasklist     config.TasklistConfig
	Logger       log.Logger
	Store        p.TasklistStore
	RunsClient   pb.RunsServiceClient
	Membership   *cluster.Membership
	RemoteClient *routing.RemoteClient
}

// NewRegistry constructs an empty registry. Call Start to launch the
// ownership scan loop.
func NewRegistry(deps RegistryDeps) *Registry {
	return &Registry{
		cfg:          deps.Config,
		logger:       deps.Logger,
		store:        deps.Store,
		runsClient:   deps.RunsClient,
		membership:   deps.Membership,
		remoteClient: deps.RemoteClient,
		loadBalancer: NewLoadBalancer(deps.Tasklist),
		managers:     make(map[string]*Manager),
		stopCh:       make(chan struct{}),
		scanDone:     make(chan struct{}),
	}
}

// ResolveReadPartition turns an inbound (namespace, wire task_list_name)
// into a fully-qualified read-path Identifier, picking a read partition
// when the wire name is a bare base name. Called by the PollForRun
// handler before the ownership check.
func (r *Registry) ResolveReadPartition(namespace, wireName string) (*Identifier, error) {
	return r.loadBalancer.ResolveReadIdentifier(namespace, wireName)
}

// ResolveWritePartition is the write-path counterpart to
// ResolveReadPartition. Called by the DispatchRun handler.
func (r *Registry) ResolveWritePartition(namespace, wireName string) (*Identifier, error) {
	return r.loadBalancer.ResolveWriteIdentifier(namespace, wireName)
}

// Start launches the periodic ownership-scan goroutine. Idempotent in
// practice (registry isn't designed to restart).
func (r *Registry) Start() {
	go r.scanLoop()
}

// Stop signals the scan loop to exit and gracefully stops all managers.
// Idempotent.
func (r *Registry) Stop() {
	if !r.stopped.CompareAndSwap(false, true) {
		return
	}
	close(r.stopCh)
	<-r.scanDone

	r.mu.Lock()
	managers := make([]*Manager, 0, len(r.managers))
	for _, m := range r.managers {
		managers = append(managers, m)
	}
	r.managers = nil
	r.mu.Unlock()

	var wg sync.WaitGroup
	for _, m := range managers {
		wg.Add(1)
		go func(m *Manager) {
			defer wg.Done()
			m.Stop()
		}(m)
	}
	wg.Wait()
	r.logger.Info("Tasklist registry stopped",
		tag.Count(len(managers)))
}

// GetOrCreateManager returns the Manager for the given partition. On
// first use, claims the tasklist (DB write) and starts the writer/reader
// goroutines. Returns ErrNotOwner if this node does not own the
// partition per the cluster hash-ring.
func (r *Registry) GetOrCreateManager(ctx context.Context, id *Identifier) (*Manager, errors.CategorizedError) {
	if r.stopped.Load() {
		return nil, errors.NewUnavailableError("tasklist registry stopped", nil)
	}
	if !r.isOwner(id) {
		owner := r.ownerOf(id)
		return nil, errors.NewConflictError(
			fmt.Sprintf("tasklist %s not owned by this node (owner=%s)", id.String(), owner),
			nil,
		)
	}

	key := id.String()
	r.mu.RLock()
	mgr, ok := r.managers[key]
	r.mu.RUnlock()
	if ok && !mgr.Stopped() {
		return mgr, nil
	}

	r.mu.Lock()
	// Double-check after acquiring write lock.
	mgr, ok = r.managers[key]
	if ok && !mgr.Stopped() {
		r.mu.Unlock()
		return mgr, nil
	}
	if ok && mgr.Stopped() {
		// Stale (self-evicted) — remove and proceed to create fresh.
		delete(r.managers, key)
	}
	mgr = NewManager(id, ManagerDeps{
		Store:           r.store,
		RunsClient:      r.runsClient,
		Membership:      r.membership,
		RemoteClient:    r.remoteClient,
		MemberID:        r.memberID(),
		MatchingAddress: r.matchingAddress(),
		Logger:          r.logger,
		Config:          r.cfg,
	})
	r.managers[key] = mgr
	r.mu.Unlock()
	r.logger.Debugf("Registry.GetOrCreateManager: creating+starting manager for %s (owner=%s)", key, r.memberID())

	if err := mgr.Start(ctx); err != nil {
		// Failed to claim or start. Stop the partially-constructed
		// manager (Stop is safe before Start; it skips the goroutine
		// teardown when started==false). Remove from map so the next
		// attempt starts fresh.
		mgr.Stop()
		r.mu.Lock()
		delete(r.managers, key)
		r.mu.Unlock()
		return nil, err
	}
	return mgr, nil
}

// scanLoop runs every TasklistOwnershipScanInterval, checking each
// owned tasklist against the current hash-ring. If ownership has moved
// to another node, the manager is gracefully stopped and removed.
func (r *Registry) scanLoop() {
	defer close(r.scanDone)

	interval := r.cfg.TasklistOwnershipScanInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.checkOwnershipAndEvict()
		}
	}
}

// checkOwnershipAndEvict scans all currently-owned managers; any whose
// hash-ring ownership has moved is stopped and removed.
func (r *Registry) checkOwnershipAndEvict() {
	r.mu.RLock()
	candidates := make([]*Manager, 0, len(r.managers))
	keys := make([]string, 0, len(r.managers))
	for k, m := range r.managers {
		candidates = append(candidates, m)
		keys = append(keys, k)
	}
	r.mu.RUnlock()

	var evicted []string
	var evictedMgrs []*Manager
	for i, mgr := range candidates {
		if mgr.Stopped() {
			evicted = append(evicted, keys[i])
			evictedMgrs = append(evictedMgrs, mgr)
			continue
		}
		if !r.isOwner(mgr.id) {
			r.logger.Info("Tasklist ownership moved away, evicting",
				tag.Namespace(mgr.id.Namespace()),
				tag.TaskListName(mgr.id.FullName()))
			evicted = append(evicted, keys[i])
			evictedMgrs = append(evictedMgrs, mgr)
		}
	}

	if len(evicted) == 0 {
		return
	}

	// Stop async (no need to hold registry lock).
	for _, m := range evictedMgrs {
		go m.Stop()
	}

	// Remove from map.
	r.mu.Lock()
	for _, k := range evicted {
		delete(r.managers, k)
	}
	r.mu.Unlock()
}

// isOwner reports whether this node owns the given partition per the
// cluster hash-ring. Single-node clusters always return true (membership
// returns this node as owner of any key).
func (r *Registry) isOwner(id *Identifier) bool {
	owner := r.membership.GetNodeForKey(id.String())
	return owner == r.memberID()
}

// ownerOf returns the memberID that owns this partition per the hash-ring.
func (r *Registry) ownerOf(id *Identifier) string {
	return r.membership.GetNodeForKey(id.String())
}

// memberID returns this node's memberID from the membership service.
func (r *Registry) memberID() string {
	return r.membership.MemberID()
}

// matchingAddress returns this node's gRPC address from the membership.
func (r *Registry) matchingAddress() string {
	return r.membership.GetAddress(r.memberID())
}
