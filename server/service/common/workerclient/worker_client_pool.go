// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package workerclient

import (
	"container/list"
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/common/grpctarget"
	"golang.org/x/sync/singleflight"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	initialRequestBackoff = 100 * time.Millisecond
	maxRequestBackoff     = time.Second
)

type hostResolver interface {
	LookupHost(context.Context, string) ([]string, error)
}

type pooledConn struct {
	conn     *grpc.ClientConn
	refs     int
	lastIdle time.Time
}

type headlessTarget struct {
	addresses []string
	available map[string]struct{}
	next      uint64
}

type stickyKey struct {
	target string
	flowID string
}

type stickyRoute struct {
	key     stickyKey
	address string
}

// WorkerClientPool shares WorkerService connections and routing state.
type WorkerClientPool struct {
	cfg      *config.Config
	header   metadata.MD
	resolver hostResolver

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu        sync.Mutex
	conns     map[string]*pooledConn
	headless  map[string]*headlessTarget
	sticky    map[stickyKey]*list.Element
	stickyLRU *list.List
	closed    bool
	flight    singleflight.Group
}

// NewWorkerClientPool constructs a WorkerService client pool.
func NewWorkerClientPool(cfg *config.Config) (*WorkerClientPool, error) {
	return newWorkerClientPool(cfg, net.DefaultResolver)
}

func newWorkerClientPool(cfg *config.Config, resolver hostResolver) (*WorkerClientPool, error) {
	if cfg == nil {
		panic("workerclient: config must not be nil")
	}
	if resolver == nil {
		panic("workerclient: resolver must not be nil")
	}
	workerCfg := &cfg.Worker
	if workerCfg.EffectiveMaxWorkerConnections() <= 0 {
		return nil, fmt.Errorf("workerclient: MaxConnections must be positive, got %d", workerCfg.MaxWorkerConnections)
	}
	if workerCfg.EffectiveWorkerConnectionIdleTimeout() <= 0 {
		return nil, fmt.Errorf("workerclient: ConnectionIdleTimeout must be positive, got %v", workerCfg.WorkerConnectionIdleTimeout)
	}
	if workerCfg.EffectiveHeadlessAddressRefreshInterval() <= 0 {
		return nil, fmt.Errorf(
			"workerclient: HeadlessAddressRefreshInterval must be positive, got %v",
			workerCfg.HeadlessAddressRefreshInterval,
		)
	}
	if cfg.Api.EffectiveGrpcMaxMessageBytes() <= 0 {
		return nil, fmt.Errorf("workerclient: GrpcMaxMessageBytes must be positive, got %d", cfg.Api.GrpcMaxMessageBytes)
	}
	if workerCfg.EffectiveMaxStickyRoutingEntries() <= 0 {
		return nil, fmt.Errorf(
			"workerclient: MaxStickyRoutingEntries must be positive, got %d",
			workerCfg.MaxStickyRoutingEntries,
		)
	}
	if workerCfg.EffectiveWorkerServiceRequestMaxAttempts() <= 0 {
		return nil, fmt.Errorf(
			"workerclient: RequestMaxAttempts must be positive, got %d",
			workerCfg.WorkerServiceRequestMaxAttempts,
		)
	}
	if err := ValidateDefaultHeaders(workerCfg.DefaultHeaders); err != nil {
		return nil, err
	}
	poolCtx, cancel := context.WithCancel(context.Background())
	return &WorkerClientPool{
		cfg:       cfg,
		header:    metadata.New(workerCfg.DefaultHeaders),
		resolver:  resolver,
		ctx:       poolCtx,
		cancel:    cancel,
		conns:     make(map[string]*pooledConn),
		headless:  make(map[string]*headlessTarget),
		sticky:    make(map[stickyKey]*list.Element),
		stickyLRU: list.New(),
	}, nil
}

// Acquire returns a flow-routed WorkerService client.
func (p *WorkerClientPool) Acquire(
	ctx context.Context,
	workerTarget *dexpb.WorkerTarget,
	flowID string,
) (dexpb.WorkerServiceClient, context.Context, func(), error) {
	if flowID == "" {
		return nil, ctx, nil, fmt.Errorf("workerclient: flowID is required")
	}
	normalized, err := grpctarget.NormalizeWorkerTarget(workerTarget)
	if err != nil {
		return nil, ctx, nil, err
	}
	logicalTarget := normalized.GetAddress()
	if normalized.GetIsHeadlessAddress() {
		if err := p.ensureHeadlessTarget(ctx, logicalTarget); err != nil {
			return nil, ctx, nil, err
		}
	}
	address, err := p.selectAddress(logicalTarget, normalized.GetIsHeadlessAddress(), flowID)
	if err != nil {
		return nil, ctx, nil, err
	}
	conn, err := p.acquireConn(address)
	if err != nil {
		return nil, ctx, nil, err
	}
	client := &routedWorkerClient{
		pool:       p,
		target:     logicalTarget,
		isHeadless: normalized.GetIsHeadlessAddress(),
		flowID:     flowID,
		address:    address,
		conn:       conn,
	}
	return client, p.withHeaders(ctx), client.release, nil
}

func (p *WorkerClientPool) ensureHeadlessTarget(ctx context.Context, target string) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return fmt.Errorf("workerclient: pool closed")
	}
	if _, ok := p.headless[target]; ok {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	_, err, _ := p.flight.Do("resolve:"+target, func() (interface{}, error) {
		return nil, p.createHeadlessTarget(ctx, target)
	})
	return err
}

func (p *WorkerClientPool) createHeadlessTarget(ctx context.Context, target string) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return fmt.Errorf("workerclient: pool closed")
	}
	if _, ok := p.headless[target]; ok {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	addresses, err := p.resolveHeadlessTarget(ctx, target)
	if err != nil {
		return err
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return fmt.Errorf("workerclient: pool closed")
	}
	if _, ok := p.headless[target]; ok {
		p.mu.Unlock()
		return nil
	}
	p.headless[target] = newHeadlessTarget(addresses)
	p.wg.Add(1)
	p.mu.Unlock()
	go p.refreshHeadlessTarget(target)
	return nil
}

func (p *WorkerClientPool) refreshHeadlessTarget(target string) {
	defer p.wg.Done()
	ticker := time.NewTicker(p.cfg.Worker.EffectiveHeadlessAddressRefreshInterval())
	defer ticker.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			addresses, err := p.resolveHeadlessTarget(p.ctx, target)
			if err != nil {
				continue
			}
			p.replaceResolvedAddresses(target, addresses)
		}
	}
}

func (p *WorkerClientPool) replaceResolvedAddresses(target string, addresses []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	current, ok := p.headless[target]
	if !ok {
		return
	}
	replacement := newHeadlessTarget(addresses)
	replacement.next = current.next
	p.headless[target] = replacement
}

func (p *WorkerClientPool) resolveHeadlessTarget(ctx context.Context, target string) ([]string, error) {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return nil, fmt.Errorf("workerclient: split headless target %q: %w", target, err)
	}
	resolvedHosts, err := p.resolver.LookupHost(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("workerclient: resolve headless target %q: %w", target, err)
	}
	unique := make(map[string]struct{}, len(resolvedHosts))
	for _, resolvedHost := range resolvedHosts {
		if net.ParseIP(resolvedHost) == nil {
			continue
		}
		unique[net.JoinHostPort(resolvedHost, port)] = struct{}{}
	}
	if len(unique) == 0 {
		return nil, fmt.Errorf("workerclient: headless target %q resolved no IP addresses", target)
	}
	addresses := make([]string, 0, len(unique))
	for address := range unique {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	return addresses, nil
}

func newHeadlessTarget(addresses []string) *headlessTarget {
	available := make(map[string]struct{}, len(addresses))
	for _, address := range addresses {
		available[address] = struct{}{}
	}
	return &headlessTarget{
		addresses: addresses,
		available: available,
	}
}

func (p *WorkerClientPool) selectAddress(
	target string,
	isHeadless bool,
	flowID string,
) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return "", fmt.Errorf("workerclient: pool closed")
	}
	if !isHeadless {
		return target, nil
	}
	resolved, ok := p.headless[target]
	if !ok || len(resolved.addresses) == 0 {
		return "", fmt.Errorf("workerclient: headless target %q has no resolved addresses", target)
	}
	key := stickyKey{target: target, flowID: flowID}
	if element, ok := p.sticky[key]; ok {
		route := element.Value.(*stickyRoute)
		if _, available := resolved.available[route.address]; available {
			p.stickyLRU.MoveToFront(element)
			return route.address, nil
		}
		p.removeStickyLocked(element)
	}
	address := resolved.addresses[resolved.next%uint64(len(resolved.addresses))]
	resolved.next++
	return address, nil
}

func (p *WorkerClientPool) rememberSticky(target, flowID, address string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	resolved, ok := p.headless[target]
	if !ok {
		return
	}
	if _, available := resolved.available[address]; !available {
		return
	}
	key := stickyKey{target: target, flowID: flowID}
	if element, ok := p.sticky[key]; ok {
		route := element.Value.(*stickyRoute)
		route.address = address
		p.stickyLRU.MoveToFront(element)
		return
	}
	element := p.stickyLRU.PushFront(&stickyRoute{key: key, address: address})
	p.sticky[key] = element
	for p.stickyLRU.Len() > p.cfg.Worker.EffectiveMaxStickyRoutingEntries() {
		p.removeStickyLocked(p.stickyLRU.Back())
	}
}

func (p *WorkerClientPool) removeStickyLocked(element *list.Element) {
	if element == nil {
		return
	}
	route := element.Value.(*stickyRoute)
	delete(p.sticky, route.key)
	p.stickyLRU.Remove(element)
}

func (p *WorkerClientPool) nextRetryAddress(
	target string,
	isHeadless bool,
	current string,
	failed map[string]struct{},
) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return "", fmt.Errorf("workerclient: pool closed")
	}
	if !isHeadless {
		return target, nil
	}
	resolved, ok := p.headless[target]
	if !ok || len(resolved.addresses) == 0 {
		return "", fmt.Errorf("workerclient: headless target %q has no resolved addresses", target)
	}
	start := 0
	for index, address := range resolved.addresses {
		if address == current {
			start = index + 1
			break
		}
	}
	for offset := 0; offset < len(resolved.addresses); offset++ {
		address := resolved.addresses[(start+offset)%len(resolved.addresses)]
		if _, alreadyFailed := failed[address]; !alreadyFailed {
			return address, nil
		}
	}
	clear(failed)
	return resolved.addresses[start%len(resolved.addresses)], nil
}

func (p *WorkerClientPool) acquireConn(address string) (*grpc.ClientConn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, fmt.Errorf("workerclient: pool closed")
	}
	if existing, ok := p.conns[address]; ok {
		existing.refs++
		return existing.conn, nil
	}
	if err := p.ensureCapacityLocked(); err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(p.cfg.Api.EffectiveGrpcMaxMessageBytes()),
			grpc.MaxCallSendMsgSize(p.cfg.Api.EffectiveGrpcMaxMessageBytes()),
		),
	)
	if err != nil {
		return nil, err
	}
	p.conns[address] = &pooledConn{conn: conn, refs: 1}
	return conn, nil
}

func (p *WorkerClientPool) ensureCapacityLocked() error {
	if len(p.conns) < p.cfg.Worker.EffectiveMaxWorkerConnections() {
		return nil
	}
	p.evictIdleLocked(true)
	if len(p.conns) < p.cfg.Worker.EffectiveMaxWorkerConnections() {
		return nil
	}
	return fmt.Errorf("workerclient: max connections %d exhausted", p.cfg.Worker.EffectiveMaxWorkerConnections())
}

func (p *WorkerClientPool) releaseConn(address string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.conns[address]
	if !ok {
		return
	}
	if entry.refs > 0 {
		entry.refs--
	}
	if entry.refs == 0 {
		entry.lastIdle = time.Now()
	}
	p.evictIdleLocked(false)
}

func (p *WorkerClientPool) evictIdleLocked(force bool) {
	now := time.Now()
	for address, entry := range p.conns {
		if entry.refs != 0 {
			continue
		}
		if force || (!entry.lastIdle.IsZero() && now.Sub(entry.lastIdle) >= p.cfg.Worker.EffectiveWorkerConnectionIdleTimeout()) {
			p.closeConn(entry.conn)
			delete(p.conns, address)
			if force && len(p.conns) < p.cfg.Worker.EffectiveMaxWorkerConnections() {
				return
			}
		}
	}
}

func (p *WorkerClientPool) withHeaders(ctx context.Context) context.Context {
	if len(p.header) == 0 {
		return ctx
	}
	return metadata.NewOutgoingContext(ctx, p.header)
}

// Close stops refreshes and closes pooled connections.
func (p *WorkerClientPool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.cancel()
	for address, entry := range p.conns {
		p.closeConn(entry.conn)
		delete(p.conns, address)
	}
	clear(p.headless)
	clear(p.sticky)
	p.stickyLRU.Init()
	p.mu.Unlock()
	p.wg.Wait()
}

func (p *WorkerClientPool) closeConn(conn *grpc.ClientConn) {
	if err := conn.Close(); err != nil {
		log.Printf("workerclient: close connection: %v", err)
	}
}

type routedWorkerClient struct {
	pool       *WorkerClientPool
	target     string
	isHeadless bool
	flowID     string

	mu       sync.Mutex
	address  string
	conn     *grpc.ClientConn
	released bool
}

func (c *routedWorkerClient) InvokeWaitForMethod(
	ctx context.Context,
	req *dexpb.InvokeWaitForMethodRequest,
	opts ...grpc.CallOption,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	response, err := c.invoke(ctx, func(client dexpb.WorkerServiceClient) (interface{}, error) {
		return client.InvokeWaitForMethod(ctx, req, opts...)
	})
	if err != nil {
		return nil, err
	}
	return response.(*dexpb.InvokeWaitForMethodResponse), nil
}

func (c *routedWorkerClient) InvokeExecuteMethod(
	ctx context.Context,
	req *dexpb.InvokeExecuteMethodRequest,
	opts ...grpc.CallOption,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	response, err := c.invoke(ctx, func(client dexpb.WorkerServiceClient) (interface{}, error) {
		return client.InvokeExecuteMethod(ctx, req, opts...)
	})
	if err != nil {
		return nil, err
	}
	return response.(*dexpb.InvokeExecuteMethodResponse), nil
}

func (c *routedWorkerClient) InvokeWorkerRPC(
	ctx context.Context,
	req *dexpb.InvokeWorkerRPCRequest,
	opts ...grpc.CallOption,
) (*dexpb.InvokeWorkerRPCResponse, error) {
	response, err := c.invoke(ctx, func(client dexpb.WorkerServiceClient) (interface{}, error) {
		return client.InvokeWorkerRPC(ctx, req, opts...)
	})
	if err != nil {
		return nil, err
	}
	return response.(*dexpb.InvokeWorkerRPCResponse), nil
}

func (c *routedWorkerClient) invoke(
	ctx context.Context,
	call func(dexpb.WorkerServiceClient) (interface{}, error),
) (interface{}, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.released || c.conn == nil {
		return nil, fmt.Errorf("workerclient: acquired client released")
	}
	failed := make(map[string]struct{})
	var lastErr error
	for attempt := 0; attempt < c.pool.cfg.Worker.EffectiveWorkerServiceRequestMaxAttempts(); attempt++ {
		if attempt > 0 {
			if err := waitForRetry(ctx, requestBackoff(attempt)); err != nil {
				return nil, err
			}
			nextAddress, err := c.pool.nextRetryAddress(
				c.target,
				c.isHeadless,
				c.address,
				failed,
			)
			if err != nil {
				return nil, err
			}
			if err := c.switchAddress(nextAddress); err != nil {
				return nil, err
			}
		}
		response, err := call(dexpb.NewWorkerServiceClient(c.conn))
		if err == nil {
			if c.isHeadless {
				c.pool.rememberSticky(c.target, c.flowID, c.address)
			}
			return response, nil
		}
		lastErr = err
		failed[c.address] = struct{}{}
		if !isRetryableWorkerServiceError(ctx, err) {
			return nil, err
		}
	}
	return nil, lastErr
}

func (c *routedWorkerClient) switchAddress(address string) error {
	if address == c.address {
		return nil
	}
	c.pool.releaseConn(c.address)
	c.conn = nil
	conn, err := c.pool.acquireConn(address)
	if err != nil {
		return err
	}
	c.address = address
	c.conn = conn
	return nil
}

func (c *routedWorkerClient) release() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.released {
		return
	}
	c.released = true
	if c.conn != nil {
		c.pool.releaseConn(c.address)
		c.conn = nil
	}
}

func requestBackoff(retry int) time.Duration {
	backoff := initialRequestBackoff
	for index := 1; index < retry && backoff < maxRequestBackoff; index++ {
		backoff *= 2
		if backoff > maxRequestBackoff {
			backoff = maxRequestBackoff
		}
	}
	half := backoff / 2
	return half + time.Duration(rand.Int64N(int64(half)+1))
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isRetryableWorkerServiceError(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	code := status.Code(err)
	return code == codes.Unavailable || code == codes.DeadlineExceeded
}
