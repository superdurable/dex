package cluster

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/server/cmd"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/integration/testhelpers"
	"github.com/superdurable/dex/server/internal/integration/testhelpers/testports"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const dbPrefix = "dex_test_integration_cluster"

var (
	clusterKeyCounter = dex.NewStateKey[int]("counter")
	clusterKeyMessage = dex.NewStateKey[string]("message")
	clusterKeyResult  = dex.NewStateKey[string]("result")
)

// Minimal 2-step sequential flow used by TestCluster_SDKE2E_ForwardedDispatchCompletes.
// Kept in this file (instead of testhelpers) because cluster is the only
// sub-package that drives the SDK from a multi-node test.
type seqStep1 struct {
	dex.StepDefaults[any]
}

func (s *seqStep1) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	if err := clusterKeyCounter.SetValue(ctx, 1); err != nil {
		return nil, err
	}
	return dex.GoTo(&seqStep2{}, nil), nil
}

type seqStep2 struct {
	dex.StepDefaults[any]
}

func (s *seqStep2) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	counter, err := clusterKeyCounter.GetValue(ctx)
	if err != nil {
		return nil, err
	}
	if err := clusterKeyCounter.SetValue(ctx, counter+1); err != nil {
		return nil, err
	}
	if err := clusterKeyMessage.SetValue(ctx, "done"); err != nil {
		return nil, err
	}
	return dex.Complete(nil), nil
}

type seqFlow struct {
	dex.FlowDefaults
}

func (f *seqFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&seqStep1{}),
		dex.NonStartingStep[any](&seqStep2{}),
	}
}

func (f *seqFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{
			dex.DefineStateKey(clusterKeyCounter),
			dex.DefineStateKey(clusterKeyMessage),
		},
	}
}

// stopBlockSignal / stopBlockCtxFlow back
// TestCluster_StopRun_ForwardsAcrossNodes. The step blocks on a per-runID
// channel until the test signals release (or RunStopped cancels ctx).
type stopBlockSignal struct {
	entered     chan struct{}
	release     chan struct{}
	exitedAfter atomic.Int64
}

var stopBlockSignals sync.Map // map[string]*stopBlockSignal

type stopBlockCtxStep struct {
	dex.StepDefaults[any]
}

func (s *stopBlockCtxStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	v, ok := stopBlockSignals.Load(ctx.RunID())
	if ok {
		sig := v.(*stopBlockSignal)
		close(sig.entered)
		select {
		case <-sig.release:
		case <-ctx.Done():
		}
		sig.exitedAfter.Store(time.Now().UnixNano())
	}
	if err := clusterKeyResult.SetValue(ctx, "ok"); err != nil {
		return nil, err
	}
	return dex.Complete(nil), nil
}

type stopBlockCtxFlow struct{ dex.FlowDefaults }

func (f *stopBlockCtxFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&stopBlockCtxStep{}),
	}
}

func (f *stopBlockCtxFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{dex.DefineStateKey(clusterKeyResult)},
	}
}

// clusterTestMaxShards must match cfg.Shard.MaxShards in startClusterServers.
const clusterTestMaxShards = 4

// exclusiveShardPartition reports whether shards [0,maxShards) are partitioned
// across two nodes with no overlap. After a second node joins, the first node
// releases shards only after a debounced rebalance (shardmanager.rebalanceDebounceInterval);
// until then both nodes can still list the same shard ID as owned locally.
func exclusiveShardPartition(ownedA, ownedB []int32, maxShards int) bool {
	if len(ownedA)+len(ownedB) != maxShards {
		return false
	}
	// With two nodes the converged state is a real split — neither node owns
	// the whole set. A transient (all, none) state during startup (one node
	// claimed everything before the other finished) is NOT convergence.
	if len(ownedA) == 0 || len(ownedB) == 0 {
		return false
	}
	seen := make([]bool, maxShards)
	for _, s := range ownedA {
		if s < 0 || int(s) >= maxShards || seen[s] {
			return false
		}
		seen[s] = true
	}
	for _, s := range ownedB {
		if s < 0 || int(s) >= maxShards || seen[s] {
			return false
		}
		seen[s] = true
	}
	for i := 0; i < maxShards; i++ {
		if !seen[i] {
			return false
		}
	}
	return true
}

type clusterNode struct {
	app         *cmd.ServerApp
	runClient   pb.RunsServiceClient
	matchClient pb.MatchingServiceClient
	runConn     *grpc.ClientConn
	matchConn   *grpc.ClientConn
}

// Fixed memberlist ports for the shared 2-node test cluster. The base port
// (and disjointness from other packages) is reserved by
// testports.IntegrationCluster.
const (
	clusterGossipPortA         = testports.IntegrationCluster
	clusterGossipPortB         = testports.IntegrationCluster + 1
	clusterTasklistGossipPortA = testports.IntegrationCluster + 2
	clusterTasklistGossipPortB = testports.IntegrationCluster + 3
	// Concrete gRPC ports: the local cross-service clients dial
	// Cluster.AdvertiseGRPCAddress, so each node's run/matching gRPC must
	// bind a known port (no :0).
	clusterRunGRPCPortA   = testports.IntegrationCluster + 4
	clusterRunGRPCPortB   = testports.IntegrationCluster + 5
	clusterMatchGRPCPortA = testports.IntegrationCluster + 6
	clusterMatchGRPCPortB = testports.IntegrationCluster + 7
)

// Shared 2-node cluster, set by TestMain and torn down on exit. Tests get it
// via startClusterServers / getSharedCluster — we boot once and let every
// testcase reuse the same nodes (each test isolates with a unique namespace).
// This brings cluster_test from ~73s (10 tests × ~7s of boot+converge each)
// down to ~15s (1× boot, then per-test ops only).
var (
	sharedNodeA, sharedNodeB *clusterNode
	sharedClusterStartErr    error
)

// buildClusterCfg builds a ServerApp config for one node of the shared
// 2-node test cluster. memberID disambiguates the two nodes; the shard
// and tasklist gossip ports point each node at the other so memberlist
// converges.
func buildClusterCfg(uri, memberID string, gossipPort, seedPort, tasklistGossipPort, tasklistSeedPort, runGRPCPort, matchGRPCPort int) config.Config {
	cfg := config.DefaultConfig()
	cfg.Persistence = testhelpers.PersistenceConfigForPrefix(uri, dbPrefix)
	cfg.MemberID = memberID
	runAddr := fmt.Sprintf("127.0.0.1:%d", runGRPCPort)
	matchAddr := fmt.Sprintf("127.0.0.1:%d", matchGRPCPort)
	cfg.GRPCListenAddress = runAddr
	cfg.MatchingGRPCListenAddress = matchAddr
	// Multiple nodes in this test must each bind a unique ops port (default
	// :7235 collides on the second node).
	cfg.OpsGRPCListenAddress = "127.0.0.1:0"

	cfg.Shard.MaxShards = 4
	cfg.Shard.DefaultShardsForNewNamespaces = 4
	cfg.Shard.LeaseDuration = 30 * time.Second
	cfg.Shard.LeaseRenewInterval = 5 * time.Second
	cfg.Shard.LeaseRenewJitter = 500 * time.Millisecond
	cfg.Shard.LeaseExpiryBuffer = 3 * time.Second
	cfg.Shard.ShutdownGracefulPeriod = 100 * time.Millisecond

	// Cross-node sync-match delivery is best-effort: if the multi-hop
	// dispatch→route→forwarded-poll→relay handshake breaks, the run is left
	// Running and is recovered by the run heartbeat timer (see
	// taskprocessor handleDispatchTask's "heartbeat timer will recover"
	// path). The 30s production default would race these tests' deadlines,
	// so use a short timer here; the cluster workers pair it with a 1s
	// worker HeartbeatInterval so healthy runs still heartbeat well inside
	// the window.
	cfg.RunService.HeartbeatTimerDuration = 3 * time.Second

	cfg.Shard.Cluster = config.ClusterConfig{
		BindAddress:              fmt.Sprintf("127.0.0.1:%d", gossipPort),
		AdvertiseAddress:         fmt.Sprintf("127.0.0.1:%d", gossipPort),
		AdvertiseGRPCAddress:     runAddr,
		NumberOfVNodes:           128,
		MinMembersBeforeReady:    1,
		ClaimRetryInterval:       500 * time.Millisecond,
		ClaimRetryIntervalJitter: 200 * time.Millisecond,
		StaticAddresses:          []string{fmt.Sprintf("127.0.0.1:%d", seedPort)},
	}

	cfg.Tasklist.Cluster = config.ClusterConfig{
		BindAddress:              fmt.Sprintf("127.0.0.1:%d", tasklistGossipPort),
		AdvertiseAddress:         fmt.Sprintf("127.0.0.1:%d", tasklistGossipPort),
		AdvertiseGRPCAddress:     matchAddr,
		NumberOfVNodes:           128,
		MinMembersBeforeReady:    1,
		ClaimRetryInterval:       500 * time.Millisecond,
		ClaimRetryIntervalJitter: 200 * time.Millisecond,
		StaticAddresses:          []string{fmt.Sprintf("127.0.0.1:%d", tasklistSeedPort)},
	}

	cfg.TaskProcessor.NumWorkers = 4
	cfg.TaskProcessor.ImmediatePollInterval = 500 * time.Millisecond
	return cfg
}

// bootSharedCluster spawns the 2-node cluster used by every test in this
// sub-package. Returns a cleanup function that stops both apps. Called from
// TestMain so the cluster is reused across testcases.
func bootSharedCluster(uri string) (a, b *clusterNode, cleanup func(), err error) {
	cfgA := buildClusterCfg(uri, "node-a", clusterGossipPortA, clusterGossipPortB, clusterTasklistGossipPortA, clusterTasklistGossipPortB, clusterRunGRPCPortA, clusterMatchGRPCPortA)
	cfgB := buildClusterCfg(uri, "node-b", clusterGossipPortB, clusterGossipPortA, clusterTasklistGossipPortB, clusterTasklistGossipPortA, clusterRunGRPCPortB, clusterMatchGRPCPortB)

	logger := log.NewNoop()
	appA, err := cmd.NewServerApp(cfgA, logger)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("NewServerApp(node-a): %w", err)
	}
	appB, err := cmd.NewServerApp(cfgB, logger)
	if err != nil {
		appA.Stop()
		return nil, nil, nil, fmt.Errorf("NewServerApp(node-b): %w", err)
	}

	// Clean DB once before any test runs so batch readers don't find stale
	// tasks from previous test sessions (which would poison the poll offset
	// cursor). Per-testcase isolation is by namespace.
	if delErr := appA.RunStore.DeleteAll(context.Background()); delErr != nil {
		appA.Stop()
		appB.Stop()
		return nil, nil, nil, fmt.Errorf("DeleteAll: %w", delErr)
	}

	ctx := context.Background()
	if startErr := appA.StartAsync(ctx); startErr != nil {
		appA.Stop()
		appB.Stop()
		return nil, nil, nil, fmt.Errorf("StartAsync(node-a): %w", startErr)
	}
	if startErr := appB.StartAsync(ctx); startErr != nil {
		appA.Stop()
		appB.Stop()
		return nil, nil, nil, fmt.Errorf("StartAsync(node-b): %w", startErr)
	}

	// Memberlist must converge and debounced shard rebalance must run so each
	// node releases shards it no longer owns (see shardmanager.rebalanceDebounceInterval).
	if waitErr := waitForExclusiveShardPartition(appA, appB, 30*time.Second); waitErr != nil {
		appA.Stop()
		appB.Stop()
		return nil, nil, nil, waitErr
	}

	connectNode := func(app *cmd.ServerApp) (*clusterNode, error) {
		runConn, err := grpc.NewClient(app.RunGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, err
		}
		matchConn, err := grpc.NewClient(app.MatchingGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			runConn.Close()
			return nil, err
		}
		return &clusterNode{
			app:         app,
			runClient:   pb.NewRunsServiceClient(runConn),
			matchClient: pb.NewMatchingServiceClient(matchConn),
			runConn:     runConn,
			matchConn:   matchConn,
		}, nil
	}

	nodeA, err := connectNode(appA)
	if err != nil {
		appA.Stop()
		appB.Stop()
		return nil, nil, nil, fmt.Errorf("connect node-a: %w", err)
	}
	nodeB, err := connectNode(appB)
	if err != nil {
		nodeA.runConn.Close()
		nodeA.matchConn.Close()
		appA.Stop()
		appB.Stop()
		return nil, nil, nil, fmt.Errorf("connect node-b: %w", err)
	}

	cleanup = func() {
		nodeA.runConn.Close()
		nodeA.matchConn.Close()
		nodeB.runConn.Close()
		nodeB.matchConn.Close()
		appA.Stop()
		appB.Stop()
	}
	return nodeA, nodeB, cleanup, nil
}

// waitForExclusiveShardPartition polls until shards [0, clusterTestMaxShards)
// are partitioned across the two nodes with no overlap. Returns an error on
// timeout; cluster boot in TestMain surfaces that to the first test via
// sharedClusterStartErr.
func waitForExclusiveShardPartition(appA, appB *cmd.ServerApp, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		a := appA.ShardManager.GetOwnedShards()
		b := appB.ShardManager.GetOwnedShards()
		if exclusiveShardPartition(a, b, clusterTestMaxShards) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	a := appA.ShardManager.GetOwnedShards()
	b := appB.ShardManager.GetOwnedShards()
	return fmt.Errorf("timeout waiting for exclusive shard partition; A=%v B=%v", a, b)
}

// startClusterServers returns the shared 2-node cluster booted once by
// TestMain. Skips when DEX_TEST_MONGO_URI is unset (TestMain skipped
// boot). Per-testcase isolation is by namespace, NOT by DeleteAll: every
// test must use a uuid-namespace so it cannot collide with siblings.
func startClusterServers(t *testing.T) (nodeA, nodeB *clusterNode) {
	if testhelpers.TestDBURI() == "" {
		t.Skip(testhelpers.PersistenceBackendEnvVar + " backend URI not set")
	}
	if sharedClusterStartErr != nil {
		t.Fatalf("shared cluster boot failed: %v", sharedClusterStartErr)
	}
	require.NotNil(t, sharedNodeA, "shared cluster nodeA not initialized")
	require.NotNil(t, sharedNodeB, "shared cluster nodeB not initialized")
	return sharedNodeA, sharedNodeB
}

// ============================================================================
// Cluster: Shard distribution — each node owns a subset, union = all shards
// ============================================================================

func TestCluster_ShardDistribution(t *testing.T) {
	nodeA, nodeB := startClusterServers(t)

	shardsA := nodeA.app.ShardManager.GetOwnedShards()
	shardsB := nodeB.app.ShardManager.GetOwnedShards()

	t.Logf("Node A owns shards: %v", shardsA)
	t.Logf("Node B owns shards: %v", shardsB)

	// Each node should own at least 1 shard (with 4 shards and 2 nodes)
	assert.NotEmpty(t, shardsA, "Node A should own at least one shard")
	assert.NotEmpty(t, shardsB, "Node B should own at least one shard")

	// Union should be all 4 shards with no overlap
	all := append(shardsA, shardsB...)
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	assert.Equal(t, []int32{0, 1, 2, 3}, all, "Union of owned shards should be {0,1,2,3}")
}

// ============================================================================
// Cluster: StartRun on any node creates a run reachable from both
// ============================================================================

func TestCluster_StartRunCrossNode(t *testing.T) {
	nodeA, nodeB := startClusterServers(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	runID := uuid.NewString()
	ns := "cluster-test-" + uuid.NewString()[:8]
	// StartRun on node A
	_, err := nodeA.runClient.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "cluster-e2e", TaskListName: "g1",
	})
	require.NoError(t, err)

	// Verify run is accessible from both nodes via GetRun
	runResp, err := nodeA.runClient.GetRun(ctx, &pb.GetRunRequest{
		Namespace: ns, RunId: runID,
	})
	require.NoError(t, err)
	assert.Equal(t, runID, runResp.RunId)

	runResp2, err := nodeB.runClient.GetRun(ctx, &pb.GetRunRequest{
		Namespace: ns, RunId: runID,
	})
	require.NoError(t, err)
	assert.Equal(t, runID, runResp2.RunId)
}

// ============================================================================
// Cluster: Shard owner address routing is correct
// ============================================================================

func TestCluster_ShardOwnerAddressRouting(t *testing.T) {
	nodeA, nodeB := startClusterServers(t)

	shardsA := nodeA.app.ShardManager.GetOwnedShards()
	shardsB := nodeB.app.ShardManager.GetOwnedShards()

	// For shards owned by B, A should resolve to B's runs address
	for _, shardID := range shardsB {
		addr := nodeA.app.ShardManager.GetShardOwnerAddress(shardID)
		assert.Equal(t, nodeB.app.RunGRPCAddress(), addr,
			"Node A should resolve shard %d to node B's runs address", shardID)
	}

	// For shards owned by A, B should resolve to A's runs address
	for _, shardID := range shardsA {
		addr := nodeB.app.ShardManager.GetShardOwnerAddress(shardID)
		assert.Equal(t, nodeA.app.RunGRPCAddress(), addr,
			"Node B should resolve shard %d to node A's runs address", shardID)
	}

	// Local shards should return "" (no forwarding needed)
	for _, shardID := range shardsA {
		assert.Empty(t, nodeA.app.ShardManager.GetShardOwnerAddress(shardID))
	}
	for _, shardID := range shardsB {
		assert.Empty(t, nodeB.app.ShardManager.GetShardOwnerAddress(shardID))
	}
}

// ============================================================================
// Cluster: Task auto-processing — StartRun → batch reader dispatches → WaitingForWorker
// ============================================================================

func TestCluster_TaskAutoProcessing(t *testing.T) {
	nodeA, nodeB := startClusterServers(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ns := "cluster-autoprocess-" + uuid.NewString()[:8]
	// Find a shard owned by node B
	shardsB := nodeB.app.ShardManager.GetOwnedShards()
	require.NotEmpty(t, shardsB, "Node B must own at least one shard")
	targetShard := shardsB[0]

	// Create a run that maps to node B's shard
	var runID string
	for i := 0; i < 1000; i++ {
		candidate := uuid.NewString()
		if nodeA.app.ShardMapper.GetShardID(ns, candidate) == targetShard {
			runID = candidate
			break
		}
	}
	require.NotEmpty(t, runID, "Could not find a run ID mapping to node B's shard")

	t.Logf("Run %s maps to shard %d (owned by node B)", runID, targetShard)
	t.Logf("Node A owns: %v, Node B owns: %v", nodeA.app.ShardManager.GetOwnedShards(), nodeB.app.ShardManager.GetOwnedShards())
	t.Logf("Node A -> shard %d owner addr: %q, B runs addr: %s",
		targetShard, nodeA.app.ShardManager.GetShardOwnerAddress(targetShard), nodeB.app.RunGRPCAddress())

	// StartRun on node A — writes run + dispatch task to shard owned by B.
	// The batch reader on B should pick it up, call DispatchRun (async match),
	// then task processor transitions status to WaitingForWorker.
	start := time.Now()
	_, err := nodeA.runClient.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "cluster-e2e", TaskListName: "g-auto",
	})
	require.NoError(t, err)

	shardID := nodeA.app.ShardMapper.GetShardID(ns, runID)

	// Poll until the run transitions from Pending.
	// Batch reader first poll may be delayed by gossip-triggered rebalance (shard
	// handoffs take ShutdownGracefulPeriod per shard + DB round trips).
	t.Logf("Polling for run %s on shard %d...", runID, shardID)
	var finalRun *p.RunRow
	for i := 0; i < 300; i++ {
		time.Sleep(100 * time.Millisecond)
		run, gErr := nodeA.app.RunStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
		if gErr != nil {
			if i%20 == 0 {
				t.Logf("  [%v] GetRun error: %v", time.Since(start).Round(time.Millisecond), gErr)
			}
			continue
		}
		if i%20 == 0 {
			t.Logf("  [%v] run status=%d version=%d", time.Since(start).Round(time.Millisecond), run.Status, run.Version)
		}
		if run.Status != p.RunStatusPending {
			finalRun = run
			break
		}
	}
	elapsed := time.Since(start)

	if finalRun != nil {
		t.Logf("Run transitioned to status=%d in %v (version=%d)", finalRun.Status, elapsed, finalRun.Version)
	} else {
		// Didn't transition — log the current state for debugging
		run, _ := nodeA.app.RunStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
		t.Logf("Run STILL Pending after %v (status=%d, version=%d)", elapsed, run.Status, run.Version)
	}

	require.NotNil(t, finalRun, "Run should have transitioned from Pending within 30s")
	assert.Equal(t, p.RunStatusWaitingForWorker, finalRun.Status,
		"Run should transition to WaitingForWorker after batch reader auto-dispatch")
}

// ============================================================================
// Cluster: StartRun forwarding — send to non-owner, verify forwarding succeeds
// ============================================================================

func TestCluster_StartRunForwarding(t *testing.T) {
	nodeA, nodeB := startClusterServers(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ns := "cluster-fwd-" + uuid.NewString()[:8]

	shardsA := nodeA.app.ShardManager.GetOwnedShards()
	shardsB := nodeB.app.ShardManager.GetOwnedShards()
	require.NotEmpty(t, shardsA, "Node A should own at least one shard")
	require.NotEmpty(t, shardsB, "Node B should own at least one shard")
	t.Logf("Node A owns shards: %v, Node B owns shards: %v", shardsA, shardsB)

	shardSetB := make(map[int32]bool, len(shardsB))
	for _, s := range shardsB {
		shardSetB[s] = true
	}
	shardSetA := make(map[int32]bool, len(shardsA))
	for _, s := range shardsA {
		shardSetA[s] = true
	}

	// Collect run IDs that map to each node's shards so we can send them
	// to the OPPOSITE node (forcing the forwarding path).
	const runsPerDirection = 50
	var forwardAtoB []string // runs whose shard is owned by B, sent to A
	var forwardBtoA []string // runs whose shard is owned by A, sent to B

	for len(forwardAtoB) < runsPerDirection || len(forwardBtoA) < runsPerDirection {
		candidate := uuid.NewString()
		shardID := nodeA.app.ShardMapper.GetShardID(ns, candidate)
		if shardSetB[shardID] && len(forwardAtoB) < runsPerDirection {
			forwardAtoB = append(forwardAtoB, candidate)
		} else if shardSetA[shardID] && len(forwardBtoA) < runsPerDirection {
			forwardBtoA = append(forwardBtoA, candidate)
		}
	}

	// Send StartRun for B-owned runs to node A (forwarding A→B)
	for _, runID := range forwardAtoB {
		_, err := nodeA.runClient.StartRun(ctx, &pb.StartRunRequest{
			Namespace: ns, RunId: runID, FlowType: "fwd-test", TaskListName: "g-fwd",
		})
		require.NoError(t, err, "StartRun via forwarding A→B should succeed (runID=%s)", runID)
	}
	t.Logf("All %d StartRun calls forwarded A→B succeeded", len(forwardAtoB))

	// Send StartRun for A-owned runs to node B (forwarding B→A)
	for _, runID := range forwardBtoA {
		_, err := nodeB.runClient.StartRun(ctx, &pb.StartRunRequest{
			Namespace: ns, RunId: runID, FlowType: "fwd-test", TaskListName: "g-fwd",
		})
		require.NoError(t, err, "StartRun via forwarding B→A should succeed (runID=%s)", runID)
	}
	t.Logf("All %d StartRun calls forwarded B→A succeeded", len(forwardBtoA))

	// Verify all runs exist and are readable from either node
	allRunIDs := append(forwardAtoB, forwardBtoA...)
	for _, runID := range allRunIDs {
		resp, err := nodeA.runClient.GetRun(ctx, &pb.GetRunRequest{
			Namespace: ns, RunId: runID,
		})
		require.NoError(t, err, "GetRun should succeed for forwarded run %s", runID)
		assert.Equal(t, runID, resp.RunId)
	}
	t.Logf("All %d forwarded runs verified readable", len(allRunIDs))
}

// TestCluster_CrossNodeMatching removed: relied on the old
// MatchingService.PollForRun streaming protocol (PollRequest /
// WorkerToServerMessage / etc.). The new protocol is unary long-poll
// PollForRun + sticky PollForExternalEvents — Phase 7 will re-add a
// cross-node matching test against the new RPC surface.

// ============================================================================
// Cluster: SDK E2E — 10 runs across 2 nodes, all complete via forwarding
// ============================================================================

// TestCluster_SDKE2E_ForwardedDispatchCompletes starts 10 runs using the SDK
// across a 2-node cluster. Some runs will have shards on node A but the
// tasklist partition owner on node B (or vice versa), requiring
// DispatchRun forwarding.
// All 10 runs must complete — without the forwarding race fix,
// forwarded runs would cycle through heartbeat timeout (~60s each).
// 240s headroom for loaded CI runners (postgres + parallel suite).
func TestCluster_SDKE2E_ForwardedDispatchCompletes(t *testing.T) {
	nodeA, nodeB := startClusterServers(t)

	registry := dex.NewRegistry()
	registry.Register(&seqFlow{})

	// Connect SDK client to node A's RunsService, worker to node B's matching
	// (ensures cross-node forwarding for at least some runs)
	runConn, err := grpc.NewClient(nodeA.app.RunGRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { runConn.Close() })

	matchConn, err := grpc.NewClient(nodeB.app.MatchingGRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { matchConn.Close() })

	taskListName := "cluster-sdk-" + uuid.NewString()[:8]
	client := dex.NewClient(registry, runConn, "default")
	worker := dex.NewWorker(registry, matchConn, runConn, "default", dex.WorkerOptions{
		TaskListName:      taskListName,
		RunConcurrency:    5,
		HeartbeatInterval: 1 * time.Second, // < cluster HeartbeatTimerDuration (3s)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()

	workerDone := make(chan error, 1)
	go func() { workerDone <- worker.Start() }()
	defer func() { worker.Stop(); <-workerDone }()

	time.Sleep(500 * time.Millisecond)

	const numRuns = 10
	runIDs := make([]string, numRuns)
	for i := 0; i < numRuns; i++ {
		runIDs[i] = uuid.NewString()
		startErr := client.StartRunWithOptions(ctx, runIDs[i], &seqFlow{}, &dex.RunOptions{TaskListName: taskListName})
		require.NoError(t, startErr, "StartRun %d failed", i)
	}
	t.Logf("Started %d runs across 2-node cluster", numRuns)

	// All must complete within the shared context budget. A single
	// WaitForRunComplete blocks at most WaitForHistoryMaxTimeout (60s); a
	// cold-cluster run can need longer, so re-issue across cap windows until
	// ctx expires.
	for i, runID := range runIDs {
		runStatus := waitForRunCompleteWithinCtx(t, client, ctx, runID)
		assert.Equal(t, dex.RunStatusCompleted, runStatus,
			"run %d (%s) not completed", i, runID)
		counter, err := clusterKeyCounter.GetRunValue(client, ctx, runID)
		require.NoError(t, err)
		message, err := clusterKeyMessage.GetRunValue(client, ctx, runID)
		require.NoError(t, err)
		assert.Equal(t, 2, counter, "run %d counter", i)
		assert.Equal(t, "done", message, "run %d message", i)
	}
	t.Logf("All %d runs completed in cluster mode", numRuns)

	// Log shard distribution to show cross-node activity
	shardsA := nodeA.app.ShardManager.GetOwnedShards()
	shardsB := nodeB.app.ShardManager.GetOwnedShards()
	t.Logf("Shard distribution: nodeA=%v, nodeB=%v", shardsA, shardsB)
}

// waitForRunCompleteWithinCtx waits for a run to reach a terminal state,
// re-issuing WaitForRunComplete across the server's per-RPC
// WaitForHistoryMaxTimeout windows. A single call blocks at most that cap and
// returns codes.DeadlineExceeded on expiry; while the shared ctx still has
// budget that just means "this window elapsed, re-issue", so we loop until the
// run closes or ctx itself expires.
func waitForRunCompleteWithinCtx(t *testing.T, client *dex.Client, ctx context.Context, runID string) int32 {
	t.Helper()
	for {
		runStatus, err := client.WaitForRunComplete(ctx, runID)
		if err == nil {
			return runStatus
		}
		require.NoError(t, ctx.Err(), "run %s: ctx expired before completion", runID)
		require.Equal(t, codes.DeadlineExceeded, status.Code(err),
			"run %s: unexpected error waiting for completion", runID)
		// Per-RPC cap elapsed but ctx still has budget — re-issue.
	}
}

// ============================================================================
// Cluster: ProcessStepExecuteCompleted forwarding — call on the wrong node,
// verify the request reaches the shard owner's engine.
// ============================================================================

// TestCluster_StepCompletionForwardsToOwner sends ProcessStepExecuteCompleted
// to the node that does NOT own the run's shard. The engine on the owner
// must respond (with NotFound for a non-existent run), proving the RPC was
// forwarded — not silently dropped or processed locally on a non-owner.
// Without forwarding, the non-owner's engine would also return NotFound but
// the durable wait timer that completion paths can write would land on the
// wrong shard's tables. This test covers the forwarding plumbing only; the
// SDK E2E tests cover the full state-machine path.
func TestCluster_StepCompletionForwardsToOwner(t *testing.T) {
	nodeA, nodeB := startClusterServers(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ns := "cluster-step-fwd-" + uuid.NewString()[:8]

	shardsB := nodeB.app.ShardManager.GetOwnedShards()
	require.NotEmpty(t, shardsB, "node B must own at least one shard")
	targetShard := shardsB[0]

	// Find a run ID whose shard is owned by node B; we will send the RPC to
	// node A so the non-owner branch must forward.
	var runID string
	for i := 0; i < 1000; i++ {
		candidate := uuid.NewString()
		if nodeA.app.ShardMapper.GetShardID(ns, candidate) == targetShard {
			runID = candidate
			break
		}
	}
	require.NotEmpty(t, runID, "could not find a run ID mapping to node B's shard")

	_, err := nodeA.runClient.ProcessStepExecuteCompleted(ctx, &pb.StepExecuteCompletedRequest{
		Namespace: ns,
		RunId:     runID,
		StepExeId: "step-1-1",
	})
	// Run does not exist; the owner's engine must reach the GetRun branch.
	// This produces a codes.NotFound on the wire — proof that the request
	// was forwarded to and processed by node B (the shard owner).
	require.Error(t, err)
	t.Logf("ProcessStepExecuteCompleted to non-owner returned %v (expected NotFound from owner's engine)", err)
}

// TestCluster_ProcessAsyncMatchForwardsToOwner sends ProcessAsyncMatch to
// node A for runs on shards owned by both nodes. Non-existent runs must
// return STALE_SUCCESS from each owner's engine — proof forwarding works.
func TestCluster_ProcessAsyncMatchForwardsToOwner(t *testing.T) {
	nodeA, nodeB := startClusterServers(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ns := "cluster-async-fwd-" + uuid.NewString()[:8]

	shardsA := nodeA.app.ShardManager.GetOwnedShards()
	shardsB := nodeB.app.ShardManager.GetOwnedShards()
	require.NotEmpty(t, shardsA)
	require.NotEmpty(t, shardsB)
	shardSetA := make(map[int32]bool, len(shardsA))
	for _, shard := range shardsA {
		shardSetA[shard] = true
	}
	shardSetB := make(map[int32]bool, len(shardsB))
	for _, shard := range shardsB {
		shardSetB[shard] = true
	}

	const perNode = 2
	var aRuns, bRuns []string
	for len(aRuns) < perNode || len(bRuns) < perNode {
		candidate := uuid.NewString()
		shard := nodeA.app.ShardMapper.GetShardID(ns, candidate)
		if shardSetA[shard] && len(aRuns) < perNode {
			aRuns = append(aRuns, candidate)
		} else if shardSetB[shard] && len(bRuns) < perNode {
			bRuns = append(bRuns, candidate)
		}
	}

	for _, runID := range aRuns {
		resp, callErr := nodeA.runClient.ProcessAsyncMatch(ctx, &pb.ProcessAsyncMatchRequest{
			Namespace: ns, RunId: runID, WorkerId: "worker-a",
		})
		require.NoError(t, callErr, "A-owned run %s", runID)
		assert.Equal(t, pb.AsyncMatchOutcome_ASYNC_MATCH_OUTCOME_STALE_SUCCESS, resp.Outcome,
			"non-existent A-owned run %s", runID)
	}
	for _, runID := range bRuns {
		resp, callErr := nodeA.runClient.ProcessAsyncMatch(ctx, &pb.ProcessAsyncMatchRequest{
			Namespace: ns, RunId: runID, WorkerId: "worker-b",
		})
		require.NoError(t, callErr, "B-owned run %s — forwarding likely failed", runID)
		assert.Equal(t, pb.AsyncMatchOutcome_ASYNC_MATCH_OUTCOME_STALE_SUCCESS, resp.Outcome,
			"non-existent B-owned run %s — forwarding likely failed", runID)
	}
	t.Logf("ProcessAsyncMatch forwarding succeeded: %d A-owned + %d B-owned runs", len(aRuns), len(bRuns))
}

// ============================================================================
// Cluster: StopRun forwarding — call StopRun on the wrong node and verify
// it forwards to the shard owner, then the matching service's
// DeliverExternalEvents forwards a StopRequested event back to the
// sticky-tasklist owner so the worker actually receives the stop signal.
// ============================================================================

func TestCluster_StopRun_ForwardsAcrossNodes(t *testing.T) {
	nodeA, nodeB := startClusterServers(t)

	registry := dex.NewRegistry()
	registry.Register(&stopBlockCtxFlow{})

	// Worker connects to node B's matching service. Run will start via node A.
	// Whichever node owns the shard is the one that processes StopRun's
	// engine call; whichever node owns the worker's sticky tasklist is the
	// one that pushes the StopRequested event to the worker. We make this
	// likely cross-node by sending StopRun to the OPPOSITE node (forces
	// RunsService forwarding) and by connecting the worker to a different
	// matching node than where the shard owner lives.
	runConn, err := grpc.NewClient(nodeA.app.RunGRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { runConn.Close() })

	matchConn, err := grpc.NewClient(nodeB.app.MatchingGRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { matchConn.Close() })

	// Use node B's runs client for StopRun to maximize the chance that we
	// hit the shard-owner-forwarding code path (run is created via node A,
	// so node A's batch reader handles the dispatch task).
	stopRunConn, err := grpc.NewClient(nodeB.app.RunGRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { stopRunConn.Close() })

	taskListName := "cluster-stop-" + uuid.NewString()[:8]
	startClient := dex.NewClient(registry, runConn, "default")
	stopClient := dex.NewClient(registry, stopRunConn, "default")
	worker := dex.NewWorker(registry, matchConn, runConn, "default", dex.WorkerOptions{
		TaskListName:      taskListName,
		RunConcurrency:    1,
		HeartbeatInterval: 1 * time.Second, // < cluster HeartbeatTimerDuration (3s)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()

	runID := uuid.NewString()
	sig := &stopBlockSignal{entered: make(chan struct{}), release: make(chan struct{})}
	stopBlockSignals.Store(runID, sig)
	defer stopBlockSignals.Delete(runID)
	defer close(sig.release)

	workerDone := make(chan error, 1)
	go func() { workerDone <- worker.Start() }()
	defer func() { worker.Stop(); <-workerDone }()

	time.Sleep(500 * time.Millisecond)

	require.NoError(t, startClient.StartRunWithOptions(ctx, runID, &stopBlockCtxFlow{}, &dex.RunOptions{TaskListName: taskListName}))

	require.Eventually(t, func() bool {
		select {
		case <-sig.entered:
			return true
		default:
			return false
		}
	}, 110*time.Second, 100*time.Millisecond, "step did not enter Execute")

	// Stop via opposite node — exercises tryForward to shard owner AND the
	// matching StopRun cross-node forward to wherever the worker stream lives.
	require.NoError(t, stopClient.StopRun(ctx, runID, dex.StopRunComplete, ""))

	require.Eventually(t, func() bool {
		return sig.exitedAfter.Load() != 0
	}, 30*time.Second, 100*time.Millisecond,
		"step Execute should return after RunStopped propagates across nodes")

	runStatus, waitErr := stopClient.WaitForRunComplete(ctx, runID)
	require.NoError(t, waitErr)
	assert.Equal(t, dex.RunStatusCompleted, runStatus)
}

// ============================================================================
// Cluster: WaitForHistoryEventId forwarding — issue the long-poll on the
// non-owner node and verify it forwards to the shard owner, whose OpsFIFO
// reader writes the history and rings the notifier that wakes the waiter.
// ============================================================================

func TestCluster_WaitForHistoryEvent_ForwardsToOwner(t *testing.T) {
	nodeA, nodeB := startClusterServers(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ns := "cluster-wfh-fwd-" + uuid.NewString()[:8]

	shardsB := nodeB.app.ShardManager.GetOwnedShards()
	require.NotEmpty(t, shardsB, "node B must own at least one shard")
	shardSetB := make(map[int32]bool, len(shardsB))
	for _, shard := range shardsB {
		shardSetB[shard] = true
	}

	// A run whose shard is owned by node B; every RPC below goes to node A so
	// the non-owner branch must forward to B — where history is written and
	// the notifier lives.
	var runID string
	for range 1000 {
		candidate := uuid.NewString()
		if shardSetB[nodeA.app.ShardMapper.GetShardID(ns, candidate)] {
			runID = candidate
			break
		}
	}
	require.NotEmpty(t, runID, "could not find a run ID mapping to a node B shard")

	// Issue the wait on node A (non-owner) for a B-owned run that does not exist
	// yet. It must forward to node B and BLOCK on B's notifier (a non-forwarded
	// wait would never be woken — the OpsFIFO reader/notifier live only on the
	// owner). Start the run mid-wait; the forwarded waiter wakes once B inserts
	// the RunStart event.
	resCh := make(chan *pb.WaitForHistoryEventResponse, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := nodeA.runClient.WaitForHistoryEvent(ctx, &pb.WaitForHistoryEventRequest{
			Namespace: ns, RunId: runID,
			Condition: &pb.WaitForHistoryEventRequest_UntilEventId{UntilEventId: 1},
		})
		resCh <- resp
		errCh <- err
	}()

	time.Sleep(300 * time.Millisecond)
	// Start the B-owned run via node A (StartRun also forwards to B).
	_, err := nodeA.runClient.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "wfh-fwd", TaskListName: "g",
		StartingSteps: []*pb.NextStep{{StepId: "s1"}},
	})
	require.NoError(t, err)

	select {
	case resp := <-resCh:
		require.NoError(t, <-errCh)
		assert.GreaterOrEqual(t, resp.LatestEventId, int64(1),
			"forwarded wait must observe the RunStart event written on the owner")
	case <-time.After(20 * time.Second):
		t.Fatal("forwarded WaitForHistoryEvent did not wake after the owner inserted RunStart")
	}
}
