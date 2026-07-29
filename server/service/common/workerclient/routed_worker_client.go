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
	"math/rand/v2"
	"sync"
	"time"

	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

const (
	initialFailoverBackoff = 100 * time.Millisecond
	maxFailoverBackoff     = time.Second
)

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
	if !c.isHeadless {
		return call(dexpb.NewWorkerServiceClient(c.conn))
	}
	return c.invokeHeadless(ctx, call)
}

func (c *routedWorkerClient) invokeHeadless(
	ctx context.Context,
	call func(dexpb.WorkerServiceClient) (interface{}, error),
) (interface{}, error) {
	failed := make(map[string]struct{})
	var lastErr error
	for attempt := 0; attempt < c.pool.cfg.Worker.EffectiveWorkerServiceRequestMaxAttempts(); attempt++ {
		if attempt > 0 {
			if err := waitForFailover(ctx, failoverBackoff(attempt)); err != nil {
				return nil, err
			}
			nextAddress, err := c.pool.nextFailoverAddress(c.target, c.address, failed)
			if err != nil {
				return nil, err
			}
			if err := c.switchAddress(nextAddress); err != nil {
				return nil, err
			}
		}
		response, err := call(dexpb.NewWorkerServiceClient(c.conn))
		if err == nil {
			c.pool.rememberSticky(c.target, c.flowID, c.address)
			return response, nil
		}
		lastErr = err
		if !c.pool.isFailoverWorkerServiceError(ctx, err) {
			return nil, err
		}
		failed[c.address] = struct{}{}
		c.pool.forgetSticky(c.target, c.flowID, c.address)
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

func (p *WorkerClientPool) isFailoverWorkerServiceError(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	_, ok := p.failoverStatusCodes[int(status.Code(err))]
	return ok
}

func failoverBackoff(failover int) time.Duration {
	backoff := initialFailoverBackoff
	for index := 1; index < failover && backoff < maxFailoverBackoff; index++ {
		backoff *= 2
		if backoff > maxFailoverBackoff {
			backoff = maxFailoverBackoff
		}
	}
	half := backoff / 2
	return half + time.Duration(rand.Int64N(int64(half)+1))
}

func waitForFailover(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
