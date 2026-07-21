// Copyright (c) 2023-2026 Super Durable, Inc.
//
// This file is part of Dex
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package membership

import (
	"encoding/json"

	"github.com/hashicorp/memberlist"
	"github.com/superdurable/dex/server/internal/log/tag"
)

type eventDelegate struct {
	m *membershipImpl
}

func newEventDelegate(m *membershipImpl) *eventDelegate {
	return &eventDelegate{m: m}
}

func (d *eventDelegate) NotifyJoin(node *memberlist.Node) {
	d.m.logger.Info("member joined", tag.NodeName(node.Name))

	var metadata nodeMetadata
	err := json.Unmarshal(node.Meta, &metadata)
	if err != nil {
		panic("failed to unmarshal node metadata")
	}

	d.m.memberMu.Lock()
	d.m.memberGrpcAddresses[node.Name] = metadata.GrpcAddress
	d.m.hring = d.m.hring.AddWeightedNode(node.Name, d.m.cfg.NumberOfVNodes)
	d.m.memberMu.Unlock()

	d.m.onRebalance()
}

func (d *eventDelegate) NotifyLeave(node *memberlist.Node) {
	d.m.logger.Info("member left", tag.NodeName(node.Name))

	d.m.memberMu.Lock()
	departedAddr := d.m.memberGrpcAddresses[node.Name]
	delete(d.m.memberGrpcAddresses, node.Name)
	d.m.hring = d.m.hring.RemoveNode(node.Name)
	d.m.memberMu.Unlock()

	d.m.onMemberLeave(departedAddr)
	d.m.onRebalance()
}

// NotifyUpdate refreshes a same-name node's address after IP change.
func (d *eventDelegate) NotifyUpdate(node *memberlist.Node) {
	var metadata nodeMetadata
	err := json.Unmarshal(node.Meta, &metadata)
	if err != nil {
		panic("failed to unmarshal node metadata")
	}
	newAddr := metadata.GrpcAddress

	d.m.memberMu.Lock()
	changed := d.m.memberGrpcAddresses[node.Name] != newAddr
	if changed {
		d.m.memberGrpcAddresses[node.Name] = newAddr
		// AddWeightedNode is a no-op when the node is already present.
		d.m.hring = d.m.hring.AddWeightedNode(node.Name, d.m.cfg.NumberOfVNodes)
	}
	d.m.memberMu.Unlock()

	if !changed {
		return
	}
	d.m.logger.Info("member address updated", tag.NodeName(node.Name))
	d.m.onRebalance()
}

type metaDelegate struct {
	m *membershipImpl
}

func newMetaDelegate(m *membershipImpl) *metaDelegate {
	return &metaDelegate{m: m}
}

type nodeMetadata struct {
	GrpcAddress string
}

func (d *metaDelegate) NodeMeta(limit int) []byte {
	metadata := nodeMetadata{
		GrpcAddress: d.m.grpcAddress.Load().(string),
	}

	meta, err := json.Marshal(metadata)
	if err != nil {
		panic("failed to marshal node metadata")
	}
	if len(meta) > limit {
		panic("node metadata exceeds limit, please user shorter grpcAddress for instance")
	}
	return meta
}

func (d *metaDelegate) NotifyMsg([]byte)                           {}
func (d *metaDelegate) GetBroadcasts(overhead, limit int) [][]byte { return nil }
func (d *metaDelegate) LocalState(join bool) []byte                { return nil }
func (d *metaDelegate) MergeRemoteState(buf []byte, join bool)     {}
