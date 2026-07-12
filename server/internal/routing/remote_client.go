package routing

import (
	"sync"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/internal/metrics"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// RemoteClient manages gRPC connections to OTHER cluster members, used for
// same-service cross-node forwarding (run->run shard-owner, matching->matching
// partition-owner). Connections are lazy-created on first use and cached per
// address. Cross-SERVICE calls within a node (run<->matching) use the local
// loopback clients instead, not this pool.
type RemoteClient struct {
	mu     sync.RWMutex
	conns  map[string]*grpc.ClientConn
	logger log.Logger
}

func NewRemoteClient(logger log.Logger) *RemoteClient {
	return &RemoteClient{
		conns:  make(map[string]*grpc.ClientConn),
		logger: logger,
	}
}

func (p *RemoteClient) getConn(address string) (*grpc.ClientConn, errors.CategorizedError) {
	p.mu.RLock()
	conn, ok := p.conns[address]
	p.mu.RUnlock()
	if ok && conn.GetState() != connectivity.Shutdown {
		return conn, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after acquiring write lock. Only Shutdown means unusable:
	// Idle/Connecting/TransientFailure/Ready are normal states gRPC recovers
	// from on its own (reconnect backoff capped via WithConnectParams below).
	if conn, ok := p.conns[address]; ok {
		if conn.GetState() != connectivity.Shutdown {
			return conn, nil
		}
		conn.Close()
		delete(p.conns, address)
	}

	conn, err := grpc.NewClient(
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		// Cap reconnect backoff at 5s (default MaxDelay is 120s). When a pod
		// restarts with a new IP, membership re-routes to the new address;
		// but if an address flaps, this bounds recovery to seconds.
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff:           backoff.Config{BaseDelay: 200 * time.Millisecond, Multiplier: 1.6, Jitter: 0.2, MaxDelay: 5 * time.Second},
			MinConnectTimeout: 3 * time.Second,
		}),
		// Detect dead connections promptly so a half-open TCP path to a
		// vanished pod is torn down instead of silently timing out RPCs.
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second,
			Timeout:             3 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.WithChainUnaryInterceptor(
			metrics.UnaryClientMetricsReportingInterceptor(),
			metrics.UnaryClientErrorLoggingInterceptor(p.logger),
		),
		grpc.WithChainStreamInterceptor(
			metrics.StreamClientMetricsReportingInterceptor(),
			metrics.StreamClientErrorLoggingInterceptor(p.logger),
		),
	)
	if err != nil {
		return nil, errors.NewInternalError("failed to connect to remote service: "+address, err)
	}
	p.conns[address] = conn
	return conn, nil
}

// Evict closes and drops the cached connection for address. Called by the
// membership cleanup when a member's address leaves the ring, so a retired
// pod IP's connection doesn't linger. Safe to call for an unknown address.
func (p *RemoteClient) Evict(address string) {
	p.mu.Lock()
	conn, ok := p.conns[address]
	if ok {
		delete(p.conns, address)
	}
	p.mu.Unlock()
	if ok {
		conn.Close()
		p.logger.Debug("RemoteClient evicted connection", tag.Address(address))
	}
}

func (p *RemoteClient) GetRunsServiceClient(address string) (pb.RunsServiceClient, errors.CategorizedError) {
	conn, err := p.getConn(address)
	if err != nil {
		return nil, err
	}
	return pb.NewRunsServiceClient(conn), nil
}

func (p *RemoteClient) GetMatchingServiceClient(address string) (pb.MatchingServiceClient, errors.CategorizedError) {
	conn, err := p.getConn(address)
	if err != nil {
		return nil, err
	}
	return pb.NewMatchingServiceClient(conn), nil
}

func (p *RemoteClient) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for addr, conn := range p.conns {
		conn.Close()
		delete(p.conns, addr)
	}
}
