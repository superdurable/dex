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
	"github.com/hashicorp/memberlist"
	"github.com/superdurable/dex/server/internal/log/tag"
)

type eventDelegate struct {
	m *membershipImpl
}

func (d *eventDelegate) NotifyJoin(node *memberlist.Node) {
	d.m.logger.Info("member joined", tag.NodeName(node.Name))

	d.m.mu.Lock()
	d.m.members[node.Name] = struct{}{}
	if len(node.Meta) > 0 {
		d.m.memberAddresses[node.Name] = string(node.Meta)
	}
	if d.m.hashRing != nil {
		d.m.hashRing = d.m.hashRing.AddWeightedNode(node.Name, d.m.cfg.NumberOfVNodes)
	}
	d.m.mu.Unlock()

	if d.m.onRebalance != nil {
		d.m.onRebalance()
	}
}

func (d *eventDelegate) NotifyLeave(node *memberlist.Node) {
	d.m.logger.Info("member left", tag.NodeName(node.Name))

	d.m.mu.Lock()
	departedAddr := d.m.memberAddresses[node.Name]
	delete(d.m.members, node.Name)
	delete(d.m.memberAddresses, node.Name)
	if d.m.hashRing != nil {
		d.m.hashRing = d.m.hashRing.RemoveNode(node.Name)
	}
	d.m.mu.Unlock()

	d.m.notifyAddressRemoved(departedAddr)
	if d.m.onRebalance != nil {
		d.m.onRebalance()
	}
}

// NotifyUpdate refreshes a same-name node's address after IP change.
func (d *eventDelegate) NotifyUpdate(node *memberlist.Node) {
	newAddr := string(node.Meta)
	if newAddr == "" {
		return
	}

	d.m.mu.Lock()
	changed := d.m.memberAddresses[node.Name] != newAddr
	if changed {
		d.m.members[node.Name] = struct{}{}
		d.m.memberAddresses[node.Name] = newAddr
		if d.m.hashRing != nil {
			// AddWeightedNode is a no-op when the node is already present.
			d.m.hashRing = d.m.hashRing.AddWeightedNode(node.Name, d.m.cfg.NumberOfVNodes)
		}
	}
	d.m.mu.Unlock()

	if !changed {
		return
	}
	d.m.logger.Info("member address updated", tag.NodeName(node.Name))
	if d.m.onRebalance != nil {
		d.m.onRebalance()
	}
}

type metaDelegate struct {
	m *membershipImpl
}

func (d *metaDelegate) NodeMeta(limit int) []byte {
	meta := []byte(d.m.internalAddress)
	if len(meta) > limit {
		return meta[:limit]
	}
	return meta
}

func (d *metaDelegate) NotifyMsg([]byte)                           {}
func (d *metaDelegate) GetBroadcasts(overhead, limit int) [][]byte { return nil }
func (d *metaDelegate) LocalState(join bool) []byte                { return nil }
func (d *metaDelegate) MergeRemoteState(buf []byte, join bool)     {}
