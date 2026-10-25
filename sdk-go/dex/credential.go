// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dex

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// credentialFileCheckInterval bounds how often a call may stat the credential file.
const credentialFileCheckInterval = time.Second

// Credential supplies the bearer credential that a Client or Worker sends with every FlowService call.
//
// A Dex server with authentication enabled requires a credential on every public API call. Use FileCredential for a token that another
// process, such as a Kubernetes projected service account token, writes and rotates.
// Use APIKeyCredential when the application holds a long-lived API key that a token endpoint exchanges for short-lived access tokens.
//
//	credential, err := dex.FileCredential("/var/run/secrets/dex/token")
//	if err != nil {
//		return err
//	}
//	client, err := dex.NewClient(registry, cache, dex.ClientOptions{Credential: credential})
//
// The credential is sent over the same connection as every other call. Dex may be served over
// plaintext gRPC; protect the network path between the application and Dex accordingly.
type Credential interface {
	// Token returns the current bearer credential. It is called before every FlowService call,
	// may be called concurrently, and must return quickly.
	//
	// ctx is the context of the call. An error fails the call without contacting Dex.
	Token(ctx context.Context) (string, error)
}

// FileCredential returns a Credential that reads a bearer token from path.
//
// The file is read at construction and reread whenever its modification time or size changes,
// checked at most once per second, so a rotated token takes effect without a restart.
// Surrounding whitespace is ignored. A file that becomes empty or unreadable fails the calls
// that need it until it is restored, rather than sending a stale token.
//
// path is the token file. It returns an error when the file cannot be read or holds no token.
func FileCredential(path string) (Credential, error) {
	credential := &fileCredential{path: path}
	if _, err := credential.load(); err != nil {
		return nil, err
	}
	return credential, nil
}

type fileCredential struct {
	path string

	mu        sync.Mutex
	token     string
	modTime   time.Time
	size      int64
	checkedAt time.Time
	loadErr   error
}

func (c *fileCredential) Token(context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.checkedAt) >= credentialFileCheckInterval {
		c.refreshLocked()
	}
	return c.token, c.loadErr
}

func (c *fileCredential) load() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshLocked()
	return c.token, c.loadErr
}

func (c *fileCredential) refreshLocked() {
	c.checkedAt = time.Now()
	info, err := os.Stat(c.path)
	if err != nil {
		c.token, c.loadErr = "", fmt.Errorf("dex: read credential file: %w", err)
		return
	}
	if c.loadErr == nil && c.token != "" && info.ModTime().Equal(c.modTime) && info.Size() == c.size {
		return
	}
	data, err := os.ReadFile(c.path)
	if err != nil {
		c.token, c.loadErr = "", fmt.Errorf("dex: read credential file: %w", err)
		return
	}
	token := string(bytes.TrimSpace(data))
	if token == "" {
		c.token, c.loadErr = "", fmt.Errorf("dex: credential file %s is empty", c.path)
		return
	}
	c.token, c.loadErr, c.modTime, c.size = token, nil, info.ModTime(), info.Size()
}

// rejectedTokenDiscarder is an optional Credential capability for dropping a token that Dex rejected.
type rejectedTokenDiscarder interface {
	// discardRejectedToken forgets token only while it is still the cached token.
	discardRejectedToken(token string)
}

// credentialDialOptions returns the dial option that attaches credential, or none without one.
func credentialDialOptions(credential Credential) []grpc.DialOption {
	if credential == nil {
		return nil
	}
	discarder, canDiscardRejectedToken := credential.(rejectedTokenDiscarder)
	if !canDiscardRejectedToken {
		return []grpc.DialOption{grpc.WithPerRPCCredentials(perRPCCredential{credential: credential})}
	}
	// Interceptors attach the token so a rejection identifies which token the call used.
	interceptor := rejectedTokenInterceptor{credential: credential, discarder: discarder}
	return []grpc.DialOption{
		grpc.WithChainUnaryInterceptor(interceptor.interceptUnary),
		grpc.WithChainStreamInterceptor(interceptor.interceptStream),
	}
}

// rejectedTokenInterceptor attaches the bearer token and reports UNAUTHENTICATED rejections to the credential.
type rejectedTokenInterceptor struct {
	credential Credential
	discarder  rejectedTokenDiscarder
}

// interceptUnary retries an UNAUTHENTICATED call once with a fresh token; Dex rejects it before executing.
func (interceptor rejectedTokenInterceptor) interceptUnary(
	ctx context.Context,
	method string,
	request any,
	reply any,
	connection *grpc.ClientConn,
	invoker grpc.UnaryInvoker,
	callOptions ...grpc.CallOption,
) error {
	token, err := interceptor.credential.Token(ctx)
	if err != nil {
		return err
	}
	err = invoker(withBearerToken(ctx, token), method, request, reply, connection, callOptions...)
	if status.Code(err) != codes.Unauthenticated {
		return err
	}
	interceptor.discarder.discardRejectedToken(token)
	token, err = interceptor.credential.Token(ctx)
	if err != nil {
		return err
	}
	return invoker(withBearerToken(ctx, token), method, request, reply, connection, callOptions...)
}

// interceptStream discards a rejected token without retrying; stream owners reconnect on their own.
func (interceptor rejectedTokenInterceptor) interceptStream(
	ctx context.Context,
	descriptor *grpc.StreamDesc,
	connection *grpc.ClientConn,
	method string,
	streamer grpc.Streamer,
	callOptions ...grpc.CallOption,
) (grpc.ClientStream, error) {
	token, err := interceptor.credential.Token(ctx)
	if err != nil {
		return nil, err
	}
	stream := &rejectedTokenStream{token: token, discarder: interceptor.discarder}
	stream.observer, _ = ctx.Value(rejectedTokenObserverKey{}).(*rejectedTokenObserver)
	stream.ClientStream, err = streamer(withBearerToken(ctx, token), descriptor, connection, method, callOptions...)
	if err != nil {
		return nil, stream.reportRejection(err)
	}
	return stream, nil
}

// rejectedTokenStream reports an UNAUTHENTICATED stream status to the credential that supplied its token.
type rejectedTokenStream struct {
	grpc.ClientStream
	token     string
	discarder rejectedTokenDiscarder
	observer  *rejectedTokenObserver
}

func (stream *rejectedTokenStream) SendMsg(message any) error {
	return stream.reportRejection(stream.ClientStream.SendMsg(message))
}

func (stream *rejectedTokenStream) RecvMsg(message any) error {
	return stream.reportRejection(stream.ClientStream.RecvMsg(message))
}

func (stream *rejectedTokenStream) CloseSend() error {
	return stream.reportRejection(stream.ClientStream.CloseSend())
}

// reportRejection discards the stream's token when err is UNAUTHENTICATED and returns err unchanged.
func (stream *rejectedTokenStream) reportRejection(err error) error {
	if status.Code(err) != codes.Unauthenticated {
		return err
	}
	stream.discarder.discardRejectedToken(stream.token)
	if stream.observer != nil {
		stream.observer.hasDiscardedRejectedToken.Store(true)
	}
	return err
}

// rejectedTokenObserver tells a stream owner that the credential discarded the stream's rejected token.
type rejectedTokenObserver struct {
	hasDiscardedRejectedToken atomic.Bool
}

type rejectedTokenObserverKey struct{}

// withRejectedTokenObserver returns ctx so that streams opened with it report discarded tokens to observer.
func withRejectedTokenObserver(ctx context.Context, observer *rejectedTokenObserver) context.Context {
	return context.WithValue(ctx, rejectedTokenObserverKey{}, observer)
}

func withBearerToken(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
}

type perRPCCredential struct {
	credential Credential
}

func (c perRPCCredential) GetRequestMetadata(ctx context.Context, _ ...string) (map[string]string, error) {
	token, err := c.credential.Token(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]string{"authorization": "Bearer " + token}, nil
}

// RequireTransportSecurity is false because Dex may be served over plaintext gRPC.
func (perRPCCredential) RequireTransportSecurity() bool {
	return false
}
