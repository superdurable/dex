package shardmanager

import (
	"strconv"

	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/cluster"
)

// membership is a thin shard-specific wrapper around cluster.Membership.
// It delegates gossip, hashring, and address management to the shared package
// and adds shard-specific helpers (GetMemberForShard, GetShardsForMember).
type membership struct {
	*cluster.Membership
	memberID string
}

func newMembership(
	cfg config.ClusterConfig,
	logger log.Logger,
	memberID string,
	internalAddress string,
	onRebalance func(),
	onAddressRemoved func(addr string),
) *membership {
	return &membership{
		Membership: cluster.NewMembership(cfg, logger, memberID, internalAddress, onRebalance, onAddressRemoved),
		memberID:   memberID,
	}
}

func (m *membership) start() error {
	return m.Start()
}

func (m *membership) stop() {
	m.Stop()
}

// GetMemberForShard returns which member should own the given shard.
func (m *membership) GetMemberForShard(shardID int32) string {
	return m.GetNodeForKey(strconv.Itoa(int(shardID)))
}

// GetInternalAddress returns the gRPC address for a member.
func (m *membership) GetInternalAddress(memberID string) string {
	return m.GetAddress(memberID)
}
