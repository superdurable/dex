// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dex

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/types/known/emptypb"
)

// credentialRecorder records the authorization metadata, :authority, and TLS SNI of every FlowService call it receives.
type credentialRecorder struct {
	mu             sync.Mutex
	authorizations map[string][]string
	authorities    map[string][]string
	tlsServerNames map[string][]string
}

func (recorder *credentialRecorder) intercept(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	recorder.record(ctx, info.FullMethod)
	return handler(ctx, req)
}

func (recorder *credentialRecorder) interceptStream(
	service interface{},
	stream grpc.ServerStream,
	info *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) error {
	recorder.record(stream.Context(), info.FullMethod)
	return handler(service, stream)
}

func (recorder *credentialRecorder) record(ctx context.Context, fullMethod string) {
	incoming, _ := metadata.FromIncomingContext(ctx)
	tlsServerName := ""
	if callPeer, hasPeer := peer.FromContext(ctx); hasPeer {
		if tlsInfo, isTLS := callPeer.AuthInfo.(credentials.TLSInfo); isTLS {
			tlsServerName = tlsInfo.State.ServerName
		}
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.authorizations[fullMethod] = append(recorder.authorizations[fullMethod], incoming.Get("authorization")...)
	recorder.authorities[fullMethod] = append(recorder.authorities[fullMethod], incoming.Get(":authority")...)
	recorder.tlsServerNames[fullMethod] = append(recorder.tlsServerNames[fullMethod], tlsServerName)
}

func (recorder *credentialRecorder) received(fullMethod string) []string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]string(nil), recorder.authorizations[fullMethod]...)
}

func (recorder *credentialRecorder) receivedAuthorities(fullMethod string) []string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]string(nil), recorder.authorities[fullMethod]...)
}

func (recorder *credentialRecorder) receivedTLSServerNames(fullMethod string) []string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]string(nil), recorder.tlsServerNames[fullMethod]...)
}

func startCredentialRecordingFlowService(t *testing.T, service dexpb.FlowServiceServer, serverOptions ...grpc.ServerOption) (string, *credentialRecorder) {
	t.Helper()
	addresses, recorder := startCredentialRecordingFlowServiceOnAddresses(t, []string{"127.0.0.1:0"}, service, serverOptions...)
	return addresses[0], recorder
}

func startCredentialRecordingFlowServiceOnAddresses(
	t *testing.T,
	bindAddresses []string,
	service dexpb.FlowServiceServer,
	serverOptions ...grpc.ServerOption,
) ([]string, *credentialRecorder) {
	t.Helper()
	recorder := &credentialRecorder{
		authorizations: map[string][]string{}, authorities: map[string][]string{}, tlsServerNames: map[string][]string{},
	}
	listeners := make([]net.Listener, 0, len(bindAddresses))
	t.Cleanup(func() {
		for _, listener := range listeners {
			if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				require.NoError(t, err)
			}
		}
	})
	addresses := make([]string, 0, len(bindAddresses))
	for _, bindAddress := range bindAddresses {
		listener, err := net.Listen("tcp", bindAddress)
		require.NoError(t, err)
		listeners = append(listeners, listener)
		addresses = append(addresses, listener.Addr().String())
	}
	grpcServer := grpc.NewServer(append([]grpc.ServerOption{
		grpc.UnaryInterceptor(recorder.intercept), grpc.StreamInterceptor(recorder.interceptStream),
	}, serverOptions...)...)
	dexpb.RegisterFlowServiceServer(grpcServer, service)
	if pullService, ok := service.(dexpb.WorkerTaskServiceServer); ok {
		dexpb.RegisterWorkerTaskServiceServer(grpcServer, pullService)
	}
	serveResults := make(chan error, len(listeners))
	for _, listener := range listeners {
		go func() { serveResults <- grpcServer.Serve(listener) }()
	}
	t.Cleanup(func() {
		grpcServer.Stop()
		for range listeners {
			requireServeStopped(t, <-serveResults)
		}
	})
	return addresses, recorder
}

func TestClientCredentialsReachFlowServiceOverTLS(t *testing.T) {
	caPEM, certificate := newCredentialTLSCertificate(t)
	address, recorder := startCredentialRecordingFlowService(t, &workerSyncFlowService{}, grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12, NextProtos: []string{"h2"},
	})))
	registry, err := NewRegistry(nil)
	require.NoError(t, err)
	cache, err := blobcache.New(&blobcache.Config{Dir: t.TempDir(), MaxBytes: 1 << 20})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close()) })
	tokenPath := filepath.Join(t.TempDir(), "token")
	writeCredentialFile(t, tokenPath, "file-token")
	fileCredential, err := FileCredential(tokenPath)
	require.NoError(t, err)
	tokenEndpoint := startSequentialAPIKeyTokenEndpoint(t)
	apiKeyCredential, err := APIKeyCredential(tokenEndpoint.tokenURL(), testAPIServerID, testAPIKey)
	require.NoError(t, err)
	for _, testCase := range []struct {
		name       string
		credential Credential
		want       string
	}{
		{name: "static", credential: staticTestCredential("static-token"), want: "Bearer static-token"},
		{name: "file", credential: fileCredential, want: "Bearer file-token"},
		{name: "API key", credential: apiKeyCredential, want: "Bearer " + apiKeyAccessTokenA},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			client, err := NewClient(registry, cache, ClientOptions{
				FlowServiceAddress: address, FlowServiceTLS: &FlowServiceTLSOptions{AdditionalRootCAsPEM: caPEM},
				Credential: testCase.credential,
			})
			require.NoError(t, err)
			defer func() { require.NoError(t, client.Close()) }()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err = client.service.GetServerInfo(ctx, &emptypb.Empty{})
			require.NoError(t, err)
			received := recorder.received(dexpb.FlowService_GetServerInfo_FullMethodName)
			require.NotEmpty(t, received)
			require.Equal(t, testCase.want, received[len(received)-1])
		})
	}
	worker, err := newWorkerForTest(t, []Flow{&registrationFlow{flowType: "tls-credential-worker"}}, WorkerOptions{
		BindAddress: unusedWorkerAddress(t), FlowServiceAddress: address,
		FlowServiceTLS: &FlowServiceTLSOptions{AdditionalRootCAsPEM: caPEM}, Credential: staticTestCredential("worker-token"),
	})
	require.NoError(t, err)
	workerResult := make(chan error, 1)
	go func() { workerResult <- worker.Start() }()
	require.Eventually(t, func() bool {
		return len(recorder.received(dexpb.FlowService_SyncAttributeIndexes_FullMethodName)) > 0
	}, 10*time.Second, 50*time.Millisecond)
	require.Equal(t, []string{"Bearer worker-token"}, recorder.received(dexpb.FlowService_SyncAttributeIndexes_FullMethodName))
	require.NoError(t, worker.Stop(context.Background()))
	require.NoError(t, <-workerResult)
}

func TestFlowServiceTLSSendsTargetAuthority(t *testing.T) {
	caPEM, certificate := newCredentialTLSCertificate(t)
	addresses, recorder := startCredentialRecordingFlowServiceOnAddresses(t, []string{"127.0.0.1:0", "[::1]:0"},
		&workerSyncFlowService{}, grpc.Creds(credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12, NextProtos: []string{"h2"},
		})))
	ipv4Address, ipv6Address := addresses[0], addresses[1]
	_, ipv4Port, err := net.SplitHostPort(ipv4Address)
	require.NoError(t, err)
	registry, err := NewRegistry(nil)
	require.NoError(t, err)
	cache, err := blobcache.New(&blobcache.Config{Dir: t.TempDir(), MaxBytes: 1 << 20})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close()) })
	for _, testCase := range []struct {
		name              string
		address           string
		serverName        string
		wantAuthority     string
		wantTLSServerName string
	}{
		{name: "IPv6 literal", address: ipv6Address, wantAuthority: ipv6Address},
		{name: "IPv6 DNS target", address: "dns:///" + ipv6Address, wantAuthority: ipv6Address},
		{name: "localhost", address: "localhost:" + ipv4Port, wantAuthority: "localhost:" + ipv4Port, wantTLSServerName: "localhost"},
		{name: "localhost DNS target", address: "dns:///localhost:" + ipv4Port, wantAuthority: "localhost:" + ipv4Port, wantTLSServerName: "localhost"},
		{name: "server name override", address: ipv4Address, serverName: "localhost", wantAuthority: "localhost", wantTLSServerName: "localhost"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			tlsOptions := &FlowServiceTLSOptions{AdditionalRootCAsPEM: caPEM, ServerName: testCase.serverName}
			transportCredentials, err := flowServiceTransportCredentials(testCase.address, tlsOptions)
			require.NoError(t, err)
			require.Equal(t, testCase.serverName, transportCredentials.Info().ServerName)
			client, err := NewClient(registry, cache, ClientOptions{FlowServiceAddress: testCase.address, FlowServiceTLS: tlsOptions})
			require.NoError(t, err)
			defer func() { require.NoError(t, client.Close()) }()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err = client.service.GetServerInfo(ctx, &emptypb.Empty{})
			require.NoError(t, err)
			authorities := recorder.receivedAuthorities(dexpb.FlowService_GetServerInfo_FullMethodName)
			require.NotEmpty(t, authorities)
			require.Equal(t, testCase.wantAuthority, authorities[len(authorities)-1])
			tlsServerNames := recorder.receivedTLSServerNames(dexpb.FlowService_GetServerInfo_FullMethodName)
			require.Equal(t, testCase.wantTLSServerName, tlsServerNames[len(tlsServerNames)-1])
		})
	}
}

type credentialPullFlowService struct {
	workerSyncFlowService
	registrationTestServer
}

func TestWorkerCredentialReachesPullStreamOverTLS(t *testing.T) {
	caPEM, certificate := newCredentialTLSCertificate(t)
	pullService := &credentialPullFlowService{registrationTestServer: registrationTestServer{
		streams: make(chan *registrationTestStream, 8),
	}}
	address, recorder := startCredentialRecordingFlowService(t, pullService, grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12, NextProtos: []string{"h2"},
	})))
	worker, err := newWorkerForTest(t, []Flow{&registrationFlow{flowType: "tls-credential-pull"}}, WorkerOptions{
		FlowServiceAddress: address,
		FlowServiceTLS:     &FlowServiceTLSOptions{AdditionalRootCAsPEM: caPEM},
		Credential:         staticTestCredential("pull-token"),
		Pull:               &PullWorkerOptions{AppWorkerID: "tls-credential-pull-worker"},
	})
	require.NoError(t, err)
	workerResult := make(chan error, 1)
	go func() { workerResult <- worker.Start() }()
	firstRegistration := awaitRegistrationTestStream(t, &pullService.registrationTestServer, 10*time.Second)
	secondRegistration := awaitRegistrationTestStream(t, &pullService.registrationTestServer, 10*time.Second)
	require.Equal(t, []string{"Bearer pull-token", "Bearer pull-token"}, recorder.received(dexpb.WorkerTaskService_PollWorkerTask_FullMethodName))
	firstRegistration.register()
	secondRegistration.register()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, worker.WaitPullRegistered(ctx))
	require.NoError(t, worker.Stop(ctx))
	require.NoError(t, <-workerResult)
}

func newCredentialTLSCertificate(t *testing.T) ([]byte, tls.Certificate) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Dex test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	caCertificate, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCertificate, &serverKey.PublicKey, caKey)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), tls.Certificate{
		Certificate: [][]byte{serverDER}, PrivateKey: serverKey,
	}
}

func writeCredentialFile(t *testing.T, path string, token string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(token+"\n"), 0o600))
}

func TestClientSendsAndRereadsFileCredential(t *testing.T) {
	address, recorder := startCredentialRecordingFlowService(t, &workerSyncFlowService{})
	tokenPath := filepath.Join(t.TempDir(), "token")
	writeCredentialFile(t, tokenPath, "first-token")
	credential, err := FileCredential(tokenPath)
	require.NoError(t, err)
	registry, err := NewRegistry(nil)
	require.NoError(t, err)
	cache, err := blobcache.New(&blobcache.Config{Dir: t.TempDir(), MaxBytes: 1 << 20})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close()) })
	client, err := NewClient(registry, cache, ClientOptions{FlowServiceAddress: address, Credential: credential})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = client.service.GetServerInfo(ctx, &emptypb.Empty{})
	require.NoError(t, err)
	require.Equal(t, []string{"Bearer first-token"}, recorder.received(dexpb.FlowService_GetServerInfo_FullMethodName))

	writeCredentialFile(t, tokenPath, "rotated-token")
	require.Eventually(t, func() bool {
		_, err := client.service.GetServerInfo(ctx, &emptypb.Empty{})
		require.NoError(t, err)
		received := recorder.received(dexpb.FlowService_GetServerInfo_FullMethodName)
		return received[len(received)-1] == "Bearer rotated-token"
	}, 5*time.Second, 100*time.Millisecond, "a rotated token takes effect without a restart")

	require.NoError(t, os.WriteFile(tokenPath, nil, 0o600))
	callsBefore := len(recorder.received(dexpb.FlowService_GetServerInfo_FullMethodName))
	require.Eventually(t, func() bool {
		_, err := client.service.GetServerInfo(ctx, &emptypb.Empty{})
		return err != nil
	}, 5*time.Second, 100*time.Millisecond, "an emptied token file fails calls instead of sending a stale token")
	received := recorder.received(dexpb.FlowService_GetServerInfo_FullMethodName)
	require.NotContains(t, received[callsBefore:], "")

	_, err = FileCredential(filepath.Join(t.TempDir(), "missing"))
	require.Error(t, err)
}

func TestWorkerSendsCredentialWithStartupCalls(t *testing.T) {
	address, recorder := startCredentialRecordingFlowService(t, &workerSyncFlowService{})
	tokenPath := filepath.Join(t.TempDir(), "token")
	writeCredentialFile(t, tokenPath, "worker-token")
	credential, err := FileCredential(tokenPath)
	require.NoError(t, err)
	worker, err := newWorkerForTest(t, []Flow{&registrationFlow{flowType: "credential-worker"}}, WorkerOptions{
		BindAddress:        unusedWorkerAddress(t),
		FlowServiceAddress: address,
		Credential:         credential,
	})
	require.NoError(t, err)
	startResult := make(chan error, 1)
	go func() { startResult <- worker.Start() }()
	require.Eventually(t, func() bool {
		return len(recorder.received(dexpb.FlowService_SyncAttributeIndexes_FullMethodName)) == 1
	}, 10*time.Second, 50*time.Millisecond)
	require.Equal(t, []string{"Bearer worker-token"}, recorder.received(dexpb.FlowService_GetServerInfo_FullMethodName))
	require.Equal(t, []string{"Bearer worker-token"}, recorder.received(dexpb.FlowService_SyncAttributeIndexes_FullMethodName))
	require.NoError(t, worker.Stop(context.Background()))
	require.NoError(t, <-startResult)
}

func TestClientWithoutCredentialSendsNone(t *testing.T) {
	address, recorder := startCredentialRecordingFlowService(t, &workerSyncFlowService{})
	registry, err := NewRegistry(nil)
	require.NoError(t, err)
	cache, err := blobcache.New(&blobcache.Config{Dir: t.TempDir(), MaxBytes: 1 << 20})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close()) })
	client, err := NewClient(registry, cache, ClientOptions{FlowServiceAddress: address})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	_, err = client.service.GetServerInfo(context.Background(), &emptypb.Empty{})
	require.NoError(t, err)
	require.Empty(t, recorder.received(dexpb.FlowService_GetServerInfo_FullMethodName))
}
