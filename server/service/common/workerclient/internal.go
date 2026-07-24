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

	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service/common/grpctarget"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// InternalService holds a single reusable InternalService connection (CAN dump activity).
type InternalService struct {
	mu     sync.Mutex
	conn   *grpc.ClientConn
	client iwfpb.InternalServiceClient
	header metadata.MD
	target string
	cfg    Config
	dial   DialFunc
	closed bool
}

// NewInternalService dials InternalService once. target is normalized like worker targets.
func NewInternalService(target string, cfg Config, dial DialFunc) (*InternalService, error) {
	if cfg.MaxMessageBytes <= 0 {
		return nil, fmt.Errorf("workerclient: MaxMessageBytes must be positive, got %d", cfg.MaxMessageBytes)
	}
	if err := ValidateDefaultHeaders(cfg.DefaultHeaders); err != nil {
		return nil, err
	}
	normalized, err := grpctarget.NormalizeWorkerTarget(target)
	if err != nil {
		return nil, err
	}
	if dial == nil {
		dial = defaultDial
	}
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(cfg.MaxMessageBytes),
			grpc.MaxCallSendMsgSize(cfg.MaxMessageBytes),
		),
	}
	conn, err := dial(context.Background(), normalized, opts...)
	if err != nil {
		return nil, err
	}
	return &InternalService{
		conn:   conn,
		client: iwfpb.NewInternalServiceClient(conn),
		header: metadata.New(cfg.DefaultHeaders),
		target: normalized,
		cfg:    cfg,
		dial:   dial,
	}, nil
}

// Client returns the InternalService client and a context with default headers.
func (i *InternalService) Client(ctx context.Context) (iwfpb.InternalServiceClient, context.Context, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed || i.client == nil {
		return nil, ctx, fmt.Errorf("workerclient: internal client closed")
	}
	outCtx := ctx
	if len(i.header) > 0 {
		outCtx = metadata.NewOutgoingContext(ctx, i.header)
	}
	return i.client, outCtx, nil
}

// Close closes the InternalService connection.
func (i *InternalService) Close() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.closed = true
	if i.conn != nil {
		_ = i.conn.Close()
		i.conn = nil
		i.client = nil
	}
}
