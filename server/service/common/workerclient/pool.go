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
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/common/grpctarget"
	"golang.org/x/sync/singleflight"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// DialFunc dials a plaintext gRPC target. Tests may inject bufconn.
type DialFunc func(ctx context.Context, target string, opts ...grpc.DialOption) (*grpc.ClientConn, error)

// Config tunes the WorkerService connection pool.
type Config struct {
	IdleTimeout     time.Duration
	MaxConnections  int
	MaxMessageBytes int
	DefaultHeaders  map[string]string
}

type pooledConn struct {
	conn     *grpc.ClientConn
	refs     int
	lastIdle time.Time
}

// Pool is a reference-counted WorkerService dial pool keyed by normalized worker_target.
type Pool struct {
	dial   DialFunc
	cfg    Config
	header metadata.MD

	mu     sync.Mutex
	conns  map[string]*pooledConn
	closed bool
	flight singleflight.Group
}

// NewPool constructs a WorkerService pool. dial may be nil to use grpc.NewClient + insecure.
func NewPool(cfg Config, dial DialFunc) (*Pool, error) {
	if cfg.MaxConnections <= 0 {
		return nil, fmt.Errorf("workerclient: MaxConnections must be positive, got %d", cfg.MaxConnections)
	}
	if cfg.IdleTimeout <= 0 {
		return nil, fmt.Errorf("workerclient: IdleTimeout must be positive, got %v", cfg.IdleTimeout)
	}
	if cfg.MaxMessageBytes <= 0 {
		return nil, fmt.Errorf("workerclient: MaxMessageBytes must be positive, got %d", cfg.MaxMessageBytes)
	}
	if err := ValidateDefaultHeaders(cfg.DefaultHeaders); err != nil {
		return nil, err
	}
	if dial == nil {
		dial = defaultDial
	}
	return &Pool{
		dial:   dial,
		cfg:    cfg,
		header: metadata.New(cfg.DefaultHeaders),
		conns:  make(map[string]*pooledConn),
	}, nil
}

func defaultDial(ctx context.Context, target string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	return grpc.NewClient(target, opts...)
}

// Acquire returns a WorkerService client for target. Call release when the RPC finishes.
func (p *Pool) Acquire(ctx context.Context, target string) (dexpb.WorkerServiceClient, context.Context, func(), error) {
	normalized, err := grpctarget.NormalizeWorkerTarget(target)
	if err != nil {
		return nil, ctx, nil, err
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, ctx, nil, fmt.Errorf("workerclient: pool closed")
	}
	if existing, ok := p.conns[normalized]; ok {
		existing.refs++
		conn := existing.conn
		p.mu.Unlock()
		return dexpb.NewWorkerServiceClient(conn), p.withHeaders(ctx), p.releaseFunc(normalized), nil
	}
	p.mu.Unlock()

	_, err, _ = p.flight.Do(normalized, func() (interface{}, error) {
		return nil, p.createConn(ctx, normalized)
	})
	if err != nil {
		return nil, ctx, nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, ctx, nil, fmt.Errorf("workerclient: pool closed")
	}
	entry, ok := p.conns[normalized]
	if !ok {
		return nil, ctx, nil, fmt.Errorf("workerclient: connection missing after dial")
	}
	entry.refs++
	return dexpb.NewWorkerServiceClient(entry.conn), p.withHeaders(ctx), p.releaseFunc(normalized), nil
}

func (p *Pool) createConn(ctx context.Context, normalized string) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return fmt.Errorf("workerclient: pool closed")
	}
	if _, ok := p.conns[normalized]; ok {
		p.mu.Unlock()
		return nil
	}
	if err := p.ensureCapacityLocked(); err != nil {
		p.mu.Unlock()
		return err
	}
	p.mu.Unlock()

	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(p.cfg.MaxMessageBytes),
			grpc.MaxCallSendMsgSize(p.cfg.MaxMessageBytes),
		),
	}
	conn, err := p.dial(ctx, normalized, opts...)
	if err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		_ = conn.Close()
		return fmt.Errorf("workerclient: pool closed")
	}
	if _, ok := p.conns[normalized]; ok {
		_ = conn.Close()
		return nil
	}
	if err := p.ensureCapacityLocked(); err != nil {
		_ = conn.Close()
		return err
	}
	p.conns[normalized] = &pooledConn{conn: conn}
	return nil
}

func (p *Pool) ensureCapacityLocked() error {
	if len(p.conns) < p.cfg.MaxConnections {
		return nil
	}
	p.evictIdleLocked(true)
	if len(p.conns) < p.cfg.MaxConnections {
		return nil
	}
	return fmt.Errorf("workerclient: max connections %d exhausted", p.cfg.MaxConnections)
}

func (p *Pool) releaseFunc(target string) func() {
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		entry, ok := p.conns[target]
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
}

func (p *Pool) evictIdleLocked(force bool) {
	now := time.Now()
	for target, entry := range p.conns {
		if entry.refs != 0 {
			continue
		}
		if force || (!entry.lastIdle.IsZero() && now.Sub(entry.lastIdle) >= p.cfg.IdleTimeout) {
			_ = entry.conn.Close()
			delete(p.conns, target)
			if force && len(p.conns) < p.cfg.MaxConnections {
				return
			}
		}
	}
}

func (p *Pool) withHeaders(ctx context.Context) context.Context {
	if len(p.header) == 0 {
		return ctx
	}
	return metadata.NewOutgoingContext(ctx, p.header)
}

// Close closes all pooled connections. Further Acquire calls fail.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	for target, entry := range p.conns {
		_ = entry.conn.Close()
		delete(p.conns, target)
	}
}

// Len returns the number of pooled connections (test helper).
func (p *Pool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.conns)
}
