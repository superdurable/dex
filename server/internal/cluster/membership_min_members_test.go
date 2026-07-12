package cluster

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/integration/testhelpers/testports"
	"github.com/stretchr/testify/require"
)

// minMembersTestBasePort is reserved by testports.InternalCluster.
// Memberlist needs a fixed UDP+TCP port (cannot use :0); the disjoint range
// from other test packages lets `go test ./...` run them in parallel.
const minMembersTestBasePort = testports.InternalCluster

func buildMinMembersCfg(port, minMembers int, seeds []int) config.ClusterConfig {
	bind := fmt.Sprintf("127.0.0.1:%d", port)
	static := make([]string, 0, len(seeds))
	for _, p := range seeds {
		static = append(static, fmt.Sprintf("127.0.0.1:%d", p))
	}
	return config.ClusterConfig{
		BindAddress:              bind,
		AdvertiseAddress:         bind,
		NumberOfVNodes:           32,
		MinMembersBeforeReady:    minMembers,
		ClaimRetryInterval:       500 * time.Millisecond,
		ClaimRetryIntervalJitter: 100 * time.Millisecond,
		OwnershipOpsMaxAttempts:  3,
		StaticAddresses:          static,
	}
}

// startInBackground starts m.Start in a goroutine and returns a channel that
// is closed when Start returns. The caller is responsible for stopping m.
func startInBackground(t *testing.T, m *Membership) (done chan struct{}, returned *atomic.Bool) {
	t.Helper()
	done = make(chan struct{})
	returned = &atomic.Bool{}
	go func() {
		err := m.Start()
		if err != nil {
			t.Errorf("membership.Start returned error: %v", err)
		}
		returned.Store(true)
		close(done)
	}()
	return done, returned
}

// TestMinMembersBeforeReady_BlocksUntilQuorum verifies that Start() blocks
// until MinMembersBeforeReady members have joined via gossip, and unblocks
// on all nodes once the quorum is reached.
//
// Timeline:
//  1. Start node A alone with minMembers=3. Expect Start() to block (only 1 member).
//  2. Start node B. Expect both Start() calls to still block (only 2 members).
//  3. Start node C. Expect all three Start() calls to return within a few seconds.
func TestMinMembersBeforeReady_BlocksUntilQuorum(t *testing.T) {
	const minMembers = 3

	portA := minMembersTestBasePort
	portB := minMembersTestBasePort + 1
	portC := minMembersTestBasePort + 2

	logger := log.NewNoop()

	// Each node lists the other two as seeds so gossip can converge regardless
	// of join order.
	cfgA := buildMinMembersCfg(portA, minMembers, []int{portB, portC})
	cfgB := buildMinMembersCfg(portB, minMembers, []int{portA, portC})
	cfgC := buildMinMembersCfg(portC, minMembers, []int{portA, portB})

	mA := NewMembership(cfgA, logger, "node-a", cfgA.AdvertiseAddress, nil, nil)
	mB := NewMembership(cfgB, logger, "node-b", cfgB.AdvertiseAddress, nil, nil)
	mC := NewMembership(cfgC, logger, "node-c", cfgC.AdvertiseAddress, nil, nil)

	t.Cleanup(func() {
		mA.Stop()
		mB.Stop()
		mC.Stop()
	})

	doneA, retA := startInBackground(t, mA)

	// Phase 1: A alone cannot reach quorum — Start should still be blocked
	// after a few ticks of waitForMinMembers (ticker fires every 1s).
	time.Sleep(3 * time.Second)
	require.False(t, retA.Load(),
		"node A Start() should block when alone and minMembersBeforeReady=%d", minMembers)

	// Phase 2: Start B. Now two members are present. Still below quorum.
	doneB, retB := startInBackground(t, mB)
	time.Sleep(3 * time.Second)
	require.False(t, retA.Load(),
		"node A Start() should still block with only 2 members (minMembersBeforeReady=%d)", minMembers)
	require.False(t, retB.Load(),
		"node B Start() should still block with only 2 members (minMembersBeforeReady=%d)", minMembers)

	// Phase 3: Start C. All three members present — every Start should return.
	// waitForMinMembers polls every 1s, and memberlist gossip + DNS-free static
	// join converges within a couple seconds on localhost. 10s is a generous bound.
	doneC, _ := startInBackground(t, mC)

	deadline := time.After(10 * time.Second)
	for _, ch := range []chan struct{}{doneA, doneB, doneC} {
		select {
		case <-ch:
		case <-deadline:
			t.Fatalf("timed out waiting for Start() to return after quorum reached (A=%v B=%v C=%v)",
				retA.Load(), retB.Load(), isClosed(doneC))
		}
	}
}

// TestMinMembersBeforeReady_DisabledWhenOne verifies that minMembersBeforeReady=1
// (the documented "disabled" value) skips the wait entirely.
func TestMinMembersBeforeReady_DisabledWhenOne(t *testing.T) {
	port := minMembersTestBasePort + 5
	cfg := buildMinMembersCfg(port, 1, nil)

	m := NewMembership(cfg, log.NewNoop(), "node-solo", cfg.AdvertiseAddress, nil, nil)
	t.Cleanup(m.Stop)

	done, _ := startInBackground(t, m)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Start() did not return when minMembersBeforeReady=1 (should skip wait)")
	}
}

func isClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
