package cmd

import (
	"net"
	"os"
	"testing"

	"github.com/superdurable/dex/server/config"
	"github.com/stretchr/testify/require"
)

func TestResolveAdvertisedGRPCAddress_UsesExplicitOverride(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	addr := resolveAdvertisedGRPCAddress(listener, &config.ClusterConfig{
		AdvertiseGRPCAddress: "10.0.0.9:7234",
	})
	require.Equal(t, "10.0.0.9:7234", addr)
}

// A concrete bind (here loopback) is advertised verbatim — that is exactly
// where peers reach us. Advertising os.Hostname() instead would break
// loopback binds (the hostname resolves to a LAN IP where nothing listens).
func TestResolveAdvertisedGRPCAddress_ConcreteBindAdvertisedVerbatim(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	addr := resolveAdvertisedGRPCAddress(listener, nil)
	_, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	require.Equal(t, net.JoinHostPort("127.0.0.1", port), addr)
}

// Only a wildcard bind (0.0.0.0 / ::), where our reachable address is unknown,
// falls back to os.Hostname() (the production k8s path with a routable pod
// hostname).
func TestResolveAdvertisedGRPCAddress_WildcardBindFallsBackToHostname(t *testing.T) {
	listener, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer listener.Close()

	hostname, err := os.Hostname()
	require.NoError(t, err)

	addr := resolveAdvertisedGRPCAddress(listener, nil)
	_, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	require.Equal(t, net.JoinHostPort(hostname, port), addr)
}
