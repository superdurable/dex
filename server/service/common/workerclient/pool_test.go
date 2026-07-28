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
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"
)

const bufSize = 1024 * 1024

type stubWorkerServer struct {
	dexpb.UnimplementedWorkerServiceServer
	calls int
}

func (s *stubWorkerServer) InvokeWorkerRPC(
	ctx context.Context, req *dexpb.InvokeWorkerRPCRequest,
) (*dexpb.InvokeWorkerRPCResponse, error) {
	s.calls++
	return &dexpb.InvokeWorkerRPCResponse{}, nil
}

func (s *stubWorkerServer) InvokeWaitForMethod(
	ctx context.Context, req *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	return &dexpb.InvokeWaitForMethodResponse{}, nil
}

func (s *stubWorkerServer) InvokeExecuteMethod(
	ctx context.Context, req *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	return &dexpb.InvokeExecuteMethodResponse{
		StepDecision: &dexpb.StepDecision{
			NextSteps: []*dexpb.StepMovement{{StepType: "done"}},
		},
	}, nil
}

func TestPoolAcquireReuseAndCapacity(t *testing.T) {
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	stub := &stubWorkerServer{}
	dexpb.RegisterWorkerServiceServer(srv, stub)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.Stop()
		_ = lis.Close()
	})

	dial := func(ctx context.Context, target string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
		opts = append(opts,
			grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
				return lis.Dial()
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		return grpc.NewClient("passthrough:///bufnet", opts...)
	}

	pool, err := NewPool(Config{
		IdleTimeout:     time.Minute,
		MaxConnections:  1,
		MaxMessageBytes: 1024 * 1024,
	}, dial)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	client1, _, release1, err := pool.Acquire(context.Background(), "worker-a:1")
	require.NoError(t, err)
	require.Equal(t, 1, pool.Len())
	_, err = client1.InvokeWorkerRPC(context.Background(), &dexpb.InvokeWorkerRPCRequest{})
	require.NoError(t, err)

	client2, _, release2, err := pool.Acquire(context.Background(), "worker-a:1")
	require.NoError(t, err)
	require.Equal(t, 1, pool.Len())
	release2()
	release1()

	_, _, _, err = pool.Acquire(context.Background(), "worker-b:1")
	require.NoError(t, err)
	require.Equal(t, 1, pool.Len())
	_ = client2
	_ = emptypb.Empty{}
}

func TestNewPoolRejectsBadHeaders(t *testing.T) {
	_, err := NewPool(Config{
		IdleTimeout:     time.Minute,
		MaxConnections:  1,
		MaxMessageBytes: 1024,
		DefaultHeaders:  map[string]string{"Bad-Key": "x"},
	}, nil)
	require.Error(t, err)
}

func TestRejectWorkerBlobIDs(t *testing.T) {
	err := RejectWorkerBlobIDs(&dexpb.Value{
		Kind: &dexpb.Value_InternalBlobIdForStringValue{InternalBlobIdForStringValue: "s|p"},
	})
	require.Error(t, err)
}
