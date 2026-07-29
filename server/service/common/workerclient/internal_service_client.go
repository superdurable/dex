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
	"log"
	"sync"

	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/common/grpctarget"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// InternalServiceClient owns the reusable continue-as-new dump connection.
type InternalServiceClient struct {
	mu     sync.Mutex
	conn   *grpc.ClientConn
	client dexpb.InternalServiceClient
	header metadata.MD
	closed bool
}

// NewInternalServiceClient creates the continue-as-new dump client.
func NewInternalServiceClient(target string, cfg *config.Config) (*InternalServiceClient, error) {
	if cfg == nil {
		panic("workerclient: config must not be nil")
	}
	maxMessageBytes := cfg.Api.EffectiveGrpcMaxMessageBytes()
	if maxMessageBytes <= 0 {
		return nil, fmt.Errorf("workerclient: GrpcMaxMessageBytes must be positive, got %d", cfg.Api.GrpcMaxMessageBytes)
	}
	if err := ValidateDefaultHeaders(cfg.Worker.DefaultHeaders); err != nil {
		return nil, err
	}
	normalized, err := grpctarget.NormalizeAddress(target)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(
		normalized,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxMessageBytes),
			grpc.MaxCallSendMsgSize(maxMessageBytes),
		),
	)
	if err != nil {
		return nil, err
	}
	return &InternalServiceClient{
		conn:   conn,
		client: dexpb.NewInternalServiceClient(conn),
		header: metadata.New(cfg.Worker.DefaultHeaders),
	}, nil
}

// Client returns the generated client with default headers.
func (i *InternalServiceClient) Client(
	ctx context.Context,
) (dexpb.InternalServiceClient, context.Context, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed || i.client == nil {
		return nil, ctx, fmt.Errorf("workerclient: internal client closed")
	}
	if len(i.header) == 0 {
		return i.client, ctx, nil
	}
	return i.client, metadata.NewOutgoingContext(ctx, i.header), nil
}

// Close closes the InternalService connection.
func (i *InternalServiceClient) Close() {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return
	}
	i.closed = true
	if i.conn == nil {
		return
	}
	if err := i.conn.Close(); err != nil {
		log.Printf("workerclient: close internal connection: %v", err)
	}
	i.conn = nil
	i.client = nil
}
