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

package async

import (
	"fmt"
	"strconv"

	"github.com/hashicorp/memberlist"
	"github.com/serialx/hashring"
	"github.com/xcherryio/xcherry/server/common/log"
)

type ClusterEventDelegate struct {
	consistent    *hashring.HashRing
	Logger        log.Logger
	Shard         int
	ServerAddress string
	AsyncService  *Service
}

func (d *ClusterEventDelegate) NotifyJoin(node *memberlist.Node) {
	meta, err := ParseClusterDelegateMetaData(node.Meta)
	if err != nil {
		d.Logger.Fatal(fmt.Sprintf("failed to parse ClusterDelegateMetaData %s", node.Meta))
	}

	hostAddress := BuildHostAddress(node)
	d.Logger.Info(fmt.Sprintf("ClusterEvent JOIN %s: advertise address %s, server address %s", d.ServerAddress, hostAddress, meta.ServerAddress))

	if meta.ServerType == ServerTypeAsync {
		if d.consistent == nil {
			d.consistent = hashring.New([]string{meta.ServerAddress})
		} else {
			d.consistent = d.consistent.AddNode(meta.ServerAddress)
		}

		if d.AsyncService != nil {
			d.asyncServerReBalance()
		}
	}
}

func (d *ClusterEventDelegate) NotifyLeave(node *memberlist.Node) {
	meta, err := ParseClusterDelegateMetaData(node.Meta)
	if err != nil {
		d.Logger.Fatal(fmt.Sprintf("failed to parse ClusterDelegateMetaData %s", node.Meta))
	}

	hostAddress := BuildHostAddress(node)
	d.Logger.Info(fmt.Sprintf("ClusterEvent LEAVE %s: advertise address %s, server address %s", d.ServerAddress, hostAddress, meta.ServerAddress))

	if meta.ServerType == ServerTypeAsync {
		if d.consistent != nil {
			d.consistent = d.consistent.RemoveNode(meta.ServerAddress)
		}

		if d.AsyncService != nil {
			d.asyncServerReBalance()
		}
	}
}

func (d *ClusterEventDelegate) NotifyUpdate(node *memberlist.Node) {
	// skip
}

func (d *ClusterEventDelegate) GetAsyncServerAddressFor(shardId int32) string {
	node, ok := d.consistent.GetNode(strconv.Itoa(int(shardId)))
	if !ok {
		d.Logger.Fatal(fmt.Sprintf("Failed to search shardId %d", shardId))
	}
	return node
}

func (d *ClusterEventDelegate) asyncServerReBalance() {
	var assignedShardIds []int32

	for i := 0; i < d.Shard; i++ {
		if d.GetAsyncServerAddressFor(int32(i)) == d.ServerAddress {
			assignedShardIds = append(assignedShardIds, int32(i))
		}
	}

	(*d.AsyncService).ReBalance(assignedShardIds)
}

func BuildHostAddress(node *memberlist.Node) string {
	return fmt.Sprintf("%s:%d", node.Addr.To4().String(), node.Port)
}
