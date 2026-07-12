package testhelpers

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/cmd"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/config"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// StartE2EServer boots a full ServerApp in single-shard mode (run-service +
// matching-service + ops-service all in one process) wired against the test
// mongo cluster under dbPrefix. Returns the app plus run/matching clients.
// Callers register cleanup via t.Cleanup automatically. Skips when
// DEX_TEST_MONGO_URI is unset.
func StartE2EServer(t *testing.T, dbPrefix string) (*cmd.ServerApp, pb.RunsServiceClient, pb.MatchingServiceClient) {
	return StartE2EServerWithConfig(t, dbPrefix, nil)
}

// StartE2EServerWithConfig is StartE2EServer plus an optional cfgFn hook to
// tweak the config before NewServerApp (e.g., to enable Prometheus metrics).
func StartE2EServerWithConfig(t *testing.T, dbPrefix string, cfgFn func(*config.Config)) (*cmd.ServerApp, pb.RunsServiceClient, pb.MatchingServiceClient) {
	uri := TestDBURI()
	if uri == "" {
		t.Skip(PersistenceBackendEnvVar + "=" + Backend() + ": backend URI not set")
	}

	cfg := config.DefaultConfig()
	ApplyPersistence(t, &cfg, uri, dbPrefix)

	ApplySingleNodeCluster(t, &cfg)
	cfg.Shard.MaxShards = 2
	cfg.Shard.DefaultShardsForNewNamespaces = 2
	cfg.Shard.ShutdownGracefulPeriod = 100 * time.Millisecond
	if cfgFn != nil {
		cfgFn(&cfg)
	}

	logger := log.NewNoop()
	if os.Getenv("DEX_TEST_DEBUG_LOG") != "" {
		logger = log.MustNewDevelopmentLogger()
	}
	app, err := cmd.NewServerApp(cfg, logger)
	require.NoError(t, err)
	app.RunStore.DeleteAll(context.Background())

	require.NoError(t, app.StartAsync(context.Background()))

	runConn, err := grpc.NewClient(app.RunGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	matchConn, err := grpc.NewClient(app.MatchingGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	t.Cleanup(func() {
		runConn.Close()
		matchConn.Close()
		app.Stop()
	})

	return app, pb.NewRunsServiceClient(runConn), pb.NewMatchingServiceClient(matchConn)
}

// reserveLoopbackAddr binds an ephemeral loopback port, closes it, and
// returns "127.0.0.1:PORT". The brief gap before the server rebinds it is an
// accepted TOCTOU in tests; concrete ports are required because the local
// cross-service clients dial Cluster.AdvertiseGRPCAddress.
func reserveLoopbackAddr(t *testing.T) string {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()
	require.NoError(t, lis.Close())
	return addr
}

// ApplySingleNodeCluster configures cfg as a single-node (1-member) cluster:
// concrete loopback gRPC ports for run+matching (the local clients dial
// Cluster.AdvertiseGRPCAddress), memberlist on an ephemeral port with no
// peers, and MinMembersBeforeReady=1 so the node is ready immediately.
func ApplySingleNodeCluster(t *testing.T, cfg *config.Config) {
	runAddr := reserveLoopbackAddr(t)
	matchAddr := reserveLoopbackAddr(t)
	cfg.GRPCListenAddress = runAddr
	cfg.MatchingGRPCListenAddress = matchAddr
	cfg.OpsGRPCListenAddress = "127.0.0.1:0"
	cfg.Shard.Cluster.BindAddress = "127.0.0.1:0"
	cfg.Shard.Cluster.AdvertiseGRPCAddress = runAddr
	cfg.Shard.Cluster.MinMembersBeforeReady = 1
	cfg.Tasklist.Cluster.BindAddress = "127.0.0.1:0"
	cfg.Tasklist.Cluster.AdvertiseGRPCAddress = matchAddr
	cfg.Tasklist.Cluster.MinMembersBeforeReady = 1
}

// NullPbValue returns the protobuf NULL value, the canonical representation of
// "no value" used by engine and matching APIs.
func NullPbValue() *pb.Value {
	return &pb.Value{Kind: &pb.Value_NullValue{NullValue: pb.NullValue_NULL_VALUE}}
}

// IntPbValue wraps an int64 as a protobuf Value.
func IntPbValue(v int64) *pb.Value {
	return &pb.Value{Kind: &pb.Value_IntValue{IntValue: v}}
}
