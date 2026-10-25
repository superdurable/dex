// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

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
	"io"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
)

var tlsTransportValue = dex.DefineAttribute[string]("tls-transport-value")

type tlsTransportFlow struct{ dex.FlowDefaults }

func (tlsTransportFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(tlsTransportStep{})}
}

func (flow tlsTransportFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.GetTransportValue, nil)}
}

func (tlsTransportFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{tlsTransportValue}}
}

func (tlsTransportFlow) GetTransportValue(ctx dex.Context, _ dex.None) (*dex.RPCResult[string], error) {
	value, err := tlsTransportValue.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[string]{Output: value}, nil
}

type tlsTransportStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (tlsTransportStep) Execute(ctx dex.Context, value string) (*dex.StepDecision, error) {
	if err := tlsTransportValue.Set(ctx, value); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(value), nil
}

type tlsTestProxy struct {
	t           *testing.T
	listener    net.Listener
	upstream    string
	mu          sync.Mutex
	connections map[net.Conn]struct{}
	serverNames []string
	workers     sync.WaitGroup
}

func startTLSFlowServiceProxy(t *testing.T) (string, []byte, *tlsTestProxy) {
	return startTLSFlowServiceProxyWithConfig(t, "127.0.0.1:0", []string{"h2"})
}

func startTLSFlowServiceProxyWithConfig(t *testing.T, bindAddress string, nextProtos []string) (string, []byte, *tlsTestProxy) {
	t.Helper()
	caPEM, certificate := newTLSTestCertificate(t)
	listener, err := tls.Listen("tcp", bindAddress, &tls.Config{
		Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12, NextProtos: nextProtos,
	})
	require.NoError(t, err)
	proxy := &tlsTestProxy{t: t, listener: listener, upstream: flowServiceAddress(), connections: make(map[net.Conn]struct{})}
	proxy.workers.Add(1)
	go proxy.accept()
	t.Cleanup(func() {
		require.NoError(t, listener.Close())
		proxy.mu.Lock()
		for connection := range proxy.connections {
			if err := connection.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				t.Errorf("close TLS proxy connection: %v", err)
			}
		}
		proxy.mu.Unlock()
		proxy.workers.Wait()
	})
	return listener.Addr().String(), caPEM, proxy
}

func (proxy *tlsTestProxy) accept() {
	defer proxy.workers.Done()
	for {
		connection, err := proxy.listener.Accept()
		if err != nil {
			return
		}
		proxy.mu.Lock()
		proxy.connections[connection] = struct{}{}
		proxy.mu.Unlock()
		proxy.workers.Add(1)
		go proxy.forward(connection)
	}
}

func (proxy *tlsTestProxy) forward(downstream net.Conn) {
	defer proxy.workers.Done()
	defer func() {
		proxy.mu.Lock()
		delete(proxy.connections, downstream)
		proxy.mu.Unlock()
		if err := downstream.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			proxy.t.Logf("close TLS downstream: %v", err)
		}
	}()
	tlsConnection := downstream.(*tls.Conn)
	if err := tlsConnection.Handshake(); err != nil || tlsConnection.ConnectionState().NegotiatedProtocol != "h2" {
		return
	}
	proxy.mu.Lock()
	proxy.serverNames = append(proxy.serverNames, tlsConnection.ConnectionState().ServerName)
	proxy.mu.Unlock()
	upstream, err := net.Dial("tcp", proxy.upstream)
	if err != nil {
		return
	}
	defer func() {
		if err := upstream.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			proxy.t.Logf("close Dex upstream: %v", err)
		}
	}()
	copyDone := make(chan struct{})
	go func() {
		if _, err := io.Copy(upstream, downstream); err != nil && !errors.Is(err, net.ErrClosed) {
			proxy.t.Logf("copy TLS downstream to Dex: %v", err)
		}
		if err := upstream.(*net.TCPConn).CloseWrite(); err != nil {
			proxy.t.Logf("close Dex write half: %v", err)
		}
		close(copyDone)
	}()
	if _, err := io.Copy(downstream, upstream); err != nil && !errors.Is(err, net.ErrClosed) {
		proxy.t.Logf("copy Dex to TLS downstream: %v", err)
	}
	if err := downstream.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		proxy.t.Logf("close TLS downstream: %v", err)
	}
	<-copyDone
}

func (proxy *tlsTestProxy) hasObservedServerName(serverName string) bool {
	proxy.mu.Lock()
	defer proxy.mu.Unlock()
	for _, observedServerName := range proxy.serverNames {
		if observedServerName == serverName {
			return true
		}
	}
	return false
}

func newTLSTestCertificate(t *testing.T) ([]byte, tls.Certificate) {
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

func TestFlowServiceTLSWithPullWorkerAndLargeValue(t *testing.T) {
	proxyAddress, caPEM, proxy := startTLSFlowServiceProxy(t)
	_, port, err := net.SplitHostPort(proxyAddress)
	require.NoError(t, err)
	address := net.JoinHostPort("localhost", port)
	registry, err := dex.NewRegistry([]dex.Flow{tlsTransportFlow{}})
	require.NoError(t, err)
	cache := newUncachedMessageSizeCache(t)
	tlsOptions := &dex.FlowServiceTLSOptions{AdditionalRootCAsPEM: caPEM}
	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{
		FlowServiceAddress: address, FlowServiceTLS: tlsOptions,
		Pull: &dex.PullWorkerOptions{AppWorkerID: "tls-" + newFlowID(t, "worker")},
	})
	require.NoError(t, err)
	workerResult := make(chan error, 1)
	go func() { workerResult <- worker.Start() }()
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		require.NoError(t, worker.Stop(stopCtx))
		require.NoError(t, <-workerResult)
	})
	require.NoError(t, worker.WaitPullRegistered(integrationContext(t)))
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{
		FlowServiceAddress: address, FlowServiceTLS: tlsOptions, WorkerTarget: worker.WorkerTarget(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	flowID := newFlowID(t, "tls-transport")
	value := strings.Repeat("t", 5<<20)
	_, err = client.StartFlow(integrationContext(t), tlsTransportFlow{}, flowID, value, dex.StartFlowOptions{})
	require.NoError(t, err)
	result, err := client.WaitForFlow(integrationContext(t), flowID, dex.WaitForFlowOptions{NeedsResults: true})
	require.NoError(t, err)
	require.Equal(t, dex.FlowCompleted, result.Status)
	var output string
	require.NoError(t, result.DecodeSingleOutput(&output))
	require.Equal(t, value, output)
	var storedValue string
	require.NoError(t, client.InvokeRPC(integrationContext(t), flowID, tlsTransportFlow{}.GetTransportValue, nil, &storedValue))
	require.Equal(t, value, storedValue)
	require.Eventually(t, func() bool {
		page, searchErr := client.SearchFlows(integrationContext(t), "FlowType = '"+dex.GetFinalFlowType(tlsTransportFlow{})+"'", 100, "")
		if searchErr != nil {
			return false
		}
		for _, flow := range page.Flows {
			if flow.FlowID == flowID && flow.Status == dex.FlowCompleted {
				return true
			}
		}
		return false
	}, 20*time.Second, 200*time.Millisecond)
	require.True(t, proxy.hasObservedServerName("localhost"), "TLS proxy did not observe localhost SNI")
}

func TestFlowServiceTLSRejectsInvalidTransport(t *testing.T) {
	address, caPEM, _ := startTLSFlowServiceProxy(t)
	registry, err := dex.NewRegistry(nil)
	require.NoError(t, err)
	cache := newPullSDKCache(t)
	for _, testCase := range []struct {
		name       string
		tlsOptions *dex.FlowServiceTLSOptions
		want       string
	}{
		{name: "plaintext", want: "server preface"},
		{name: "untrusted CA", tlsOptions: &dex.FlowServiceTLSOptions{}, want: "failed to verify certificate"},
		{name: "wrong server name", tlsOptions: &dex.FlowServiceTLSOptions{AdditionalRootCAsPEM: caPEM, ServerName: "wrong.example"}, want: "wrong.example"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			client, err := dex.NewClient(registry, cache, dex.ClientOptions{FlowServiceAddress: address, FlowServiceTLS: testCase.tlsOptions})
			require.NoError(t, err)
			defer func() { require.NoError(t, client.Close()) }()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err = client.SearchFlows(ctx, "FlowType = 'tlsTransportFlow'", 1, "")
			require.ErrorContains(t, err, testCase.want)
			if testCase.name == "untrusted CA" {
				require.NotContains(t, err.Error(), "is valid for")
			}
		})
	}
	_, err = dex.NewClient(registry, cache, dex.ClientOptions{FlowServiceAddress: address,
		FlowServiceTLS: &dex.FlowServiceTLSOptions{AdditionalRootCAsPEM: []byte("invalid")}})
	require.ErrorContains(t, err, "FlowServiceTLS.AdditionalRootCAsPEM")
	_, err = dex.NewWorker(registry, cache, dex.WorkerOptions{FlowServiceAddress: address,
		FlowServiceTLS: &dex.FlowServiceTLSOptions{AdditionalRootCAsPEM: []byte("invalid")}, Pull: &dex.PullWorkerOptions{}})
	require.ErrorContains(t, err, "FlowServiceTLS.AdditionalRootCAsPEM")
	_, err = dex.NewClient(registry, cache, dex.ClientOptions{FlowServiceAddress: "https://localhost:443",
		FlowServiceTLS: &dex.FlowServiceTLSOptions{}})
	require.ErrorContains(t, err, "FlowServiceAddress")
	_, err = dex.NewWorker(registry, cache, dex.WorkerOptions{FlowServiceAddress: address,
		FlowServiceTLS: &dex.FlowServiceTLSOptions{ServerName: "localhost:443"}, Pull: &dex.PullWorkerOptions{}})
	require.ErrorContains(t, err, "FlowServiceTLS.ServerName")
	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{
		FlowServiceAddress: address, FlowServiceTLS: &dex.FlowServiceTLSOptions{},
		Pull: &dex.PullWorkerOptions{}, AttributeIndexSyncTimeout: 5 * time.Second,
	})
	require.NoError(t, err)
	err = worker.Start()
	require.ErrorContains(t, err, "FlowService connection failed")
	require.ErrorContains(t, err, "failed to verify certificate")
	require.NotContains(t, err.Error(), "incompatible")
}

func TestFlowServiceTLSRejectsUnsupportedAddresses(t *testing.T) {
	registry, err := dex.NewRegistry(nil)
	require.NoError(t, err)
	cache := newPullSDKCache(t)
	for _, address := range []string{
		"unix:8443", "passthrough:443", "dns:443", "unix:///tmp/dex.sock",
		"unix:/tmp/dex.sock", "dns:///localhost/path:443", "https://localhost:443",
	} {
		t.Run(address, func(t *testing.T) {
			_, err := dex.NewClient(registry, cache, dex.ClientOptions{
				FlowServiceAddress: address, FlowServiceTLS: &dex.FlowServiceTLSOptions{},
			})
			require.ErrorContains(t, err, "FlowServiceAddress as host:port, dns:///host:port, or dns://authority/host:port")
		})
	}
}

func TestFlowServiceTLSRejectsServerWithoutHTTP2ALPN(t *testing.T) {
	address, caPEM, proxy := startTLSFlowServiceProxyWithConfig(t, "127.0.0.1:0", []string{"http/1.1"})
	registry, err := dex.NewRegistry(nil)
	require.NoError(t, err)
	client, err := dex.NewClient(registry, newPullSDKCache(t), dex.ClientOptions{
		FlowServiceAddress: address,
		FlowServiceTLS:     &dex.FlowServiceTLSOptions{AdditionalRootCAsPEM: caPEM},
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, client.Close()) }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = client.SearchFlows(ctx, "FlowType = 'tlsTransportFlow'", 1, "")
	require.ErrorContains(t, err, "no application protocol")
	require.False(t, proxy.hasObservedServerName(""))
}

func TestFlowServiceTLSDNSTargetAndServerNameOverride(t *testing.T) {
	for _, testCase := range []struct {
		name                   string
		usesDNSTarget          bool
		usesServerNameOverride bool
		usesIPv6               bool
		usesDNSAuthority       bool
	}{
		{name: "DNS target", usesDNSTarget: true},
		{name: "server name override", usesServerNameOverride: true},
		{name: "IPv6 literal", usesDNSTarget: true, usesIPv6: true},
		{name: "DNS authority", usesDNSTarget: true, usesDNSAuthority: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			bindAddress := "127.0.0.1:0"
			if testCase.usesIPv6 {
				bindAddress = "[::1]:0"
			}
			proxyAddress, caPEM, proxy := startTLSFlowServiceProxyWithConfig(t, bindAddress, []string{"h2"})
			address := proxyAddress
			if testCase.usesDNSTarget {
				host, port, err := net.SplitHostPort(proxyAddress)
				require.NoError(t, err)
				if testCase.usesIPv6 {
					address = "dns:///" + net.JoinHostPort(host, port)
				} else if testCase.usesDNSAuthority {
					address = "dns://127.0.0.1:53/" + net.JoinHostPort(host, port)
				} else {
					address = "dns:///localhost:" + port
				}
			}
			tlsOptions := &dex.FlowServiceTLSOptions{AdditionalRootCAsPEM: caPEM}
			if testCase.usesServerNameOverride {
				tlsOptions.ServerName = "localhost"
			}
			registry, err := dex.NewRegistry(nil)
			require.NoError(t, err)
			client, err := dex.NewClient(registry, newPullSDKCache(t), dex.ClientOptions{FlowServiceAddress: address, FlowServiceTLS: tlsOptions})
			require.NoError(t, err)
			defer func() { require.NoError(t, client.Close()) }()
			_, err = client.SearchFlows(integrationContext(t), "FlowType = 'tlsTransportFlow'", 1, "")
			require.NoError(t, err)
			wantServerName := "localhost"
			if testCase.usesIPv6 || testCase.usesDNSAuthority {
				wantServerName = ""
			}
			require.True(t, proxy.hasObservedServerName(wantServerName), "TLS proxy did not observe expected SNI %q", wantServerName)
		})
	}
}
