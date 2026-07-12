package cluster

import (
	"testing"

	"github.com/superdurable/dex/server/config"
	"github.com/stretchr/testify/require"
)

func TestBuildDiscoveryTargets_UsesBindPortAndFiltersSelf(t *testing.T) {
	cfg := config.ClusterConfig{
		BindAddress:      "0.0.0.0:7946",
		AdvertiseAddress: "10.0.0.1:7946",
		Discovery: config.DiscoveryConfig{
			Mode:       "dns",
			ServiceDNS: "dex-headless.default.svc.cluster.local",
		},
	}

	targets := buildDiscoveryTargets(cfg, []string{"10.0.0.1", "10.0.0.2", "10.0.0.2", "10.0.0.3"})
	require.Equal(t, []string{"10.0.0.2:7946", "10.0.0.3:7946"}, targets)
}

func TestBuildDiscoveryTargets_UsesOverridePort(t *testing.T) {
	cfg := config.ClusterConfig{
		BindAddress: "0.0.0.0:7946",
		Discovery: config.DiscoveryConfig{
			Mode:       "dns",
			ServiceDNS: "dex-headless.default.svc.cluster.local",
			Port:       9000,
		},
	}

	targets := buildDiscoveryTargets(cfg, []string{"10.0.0.2"})
	require.Equal(t, []string{"10.0.0.2:9000"}, targets)
}
