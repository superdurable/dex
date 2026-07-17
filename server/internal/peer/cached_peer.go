package peer

import (
	"sync"
	"time"

	"github.com/superdurable/dex/protos/gengo/dexpb"
	"github.com/superdurable/dex/server/internal/errors"
	"github.com/superdurable/dex/server/internal/log"
	"github.com/superdurable/dex/server/internal/log/tag"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// CachedPeerConnection manages gRPC connections to other cluster peers for cross-node
// routing.
type CachedPeerConnection interface {
	ForEngineService(address string) (dexpb.EngineServiceClient, errors.CategorizedError)
	ForTaskQueueService(address string) (dexpb.TaskQueueServiceClient, errors.CategorizedError)
	// EvictCachedConn is for when a remote instance is shutdown, we need to evict the cached connection to it
	EvictCachedConn(address string)
	// Close closes every cached connection. Call during server shutdown.
	Close()
}

type cachedPeerConnImpl struct {
	mu     sync.RWMutex
	conns  map[string]*grpc.ClientConn
	logger log.Logger
}

func NewRouters(logger log.Logger) CachedPeerConnection {
	if logger == nil {
		panic("routers logger is nil")
	}
	return &cachedPeerConnImpl{
		conns:  make(map[string]*grpc.ClientConn),
		logger: logger,
	}
}

func (r *cachedPeerConnImpl) ForEngineService(address string) (dexpb.EngineServiceClient, errors.CategorizedError) {
	conn, err := r.getOrCreateConn(address)
	if err != nil {
		return nil, err
	}
	return dexpb.NewEngineServiceClient(conn), nil
}

func (r *cachedPeerConnImpl) ForTaskQueueService(address string) (dexpb.TaskQueueServiceClient, errors.CategorizedError) {
	conn, err := r.getOrCreateConn(address)
	if err != nil {
		return nil, err
	}
	return dexpb.NewTaskQueueServiceClient(conn), nil
}

func (r *cachedPeerConnImpl) EvictCachedConn(address string) {
	r.mu.Lock()
	conn, ok := r.conns[address]
	if ok {
		delete(r.conns, address)
	}
	r.mu.Unlock()
	if !ok {
		return
	}

	if err := conn.Close(); err != nil {
		r.logger.Warn("failed to close evicted grpc connection", tag.Address(address), tag.Error(err))
		return
	}
	r.logger.Debug("evicted grpc client connection", tag.Address(address))
}

func (r *cachedPeerConnImpl) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for address, conn := range r.conns {
		if err := conn.Close(); err != nil {
			r.logger.Warn("failed to close grpc connection", tag.Address(address), tag.Error(err))
		}
		delete(r.conns, address)
	}
}

func (r *cachedPeerConnImpl) getOrCreateConn(address string) (*grpc.ClientConn, errors.CategorizedError) {
	r.mu.RLock()
	conn, ok := r.conns[address]
	r.mu.RUnlock()
	if ok && conn.GetState() != connectivity.Shutdown {
		return conn, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// Double-check after acquiring write lock. Only Shutdown means unusable;
	// Idle/Connecting/TransientFailure/Ready all recover on their own via the
	// reconnect backoff configured below.
	if conn, ok = r.conns[address]; ok {
		if conn.GetState() != connectivity.Shutdown {
			return conn, nil
		}
		if err := conn.Close(); err != nil {
			r.logger.Warn("failed to close shutdown grpc connection", tag.Address(address), tag.Error(err))
		}
		delete(r.conns, address)
	}

	conn, dialErr := grpc.NewClient(
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		// Cap reconnect backoff at 5s (gRPC default MaxDelay is 120s). When a pod
		// restarts with a new IP, membership re-routes to the new address; if an
		// address flaps, this bounds recovery to seconds instead of minutes.
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff:           backoff.Config{BaseDelay: 200 * time.Millisecond, Multiplier: 1.6, Jitter: 0.2, MaxDelay: 5 * time.Second},
			MinConnectTimeout: 3 * time.Second,
		}),
		// Detect dead connections promptly so a half-open TCP path to a vanished
		// pod is torn down instead of silently timing out RPCs.
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second,
			Timeout:             3 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	if dialErr != nil {
		r.logger.Error("failed to create grpc client", tag.Address(address), tag.Error(dialErr))
		return nil, errors.NewInternalError("failed to create grpc client", dialErr)
	}
	r.conns[address] = conn
	r.logger.Debug("created grpc client connection", tag.Address(address))
	return conn, nil
}
