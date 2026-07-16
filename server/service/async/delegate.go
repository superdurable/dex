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
	"encoding/json"
)

type ClusterDelegate struct {
	Meta ClusterDelegateMetaData
}

func (d *ClusterDelegate) NodeMeta(limit int) []byte {
	return d.Meta.Bytes()
}
func (d *ClusterDelegate) LocalState(join bool) []byte {
	// not use, noop
	return []byte("")
}
func (d *ClusterDelegate) NotifyMsg(msg []byte) {
	// not use
}
func (d *ClusterDelegate) GetBroadcasts(overhead, limit int) [][]byte {
	// not use, noop
	return nil
}
func (d *ClusterDelegate) MergeRemoteState(buf []byte, join bool) {
	// not use
}

type ClusterDelegateMetaData struct {
	ServerType    string
	ServerAddress string
}

func (m ClusterDelegateMetaData) Bytes() []byte {
	data, err := json.Marshal(m)
	if err != nil {
		return []byte("")
	}
	return data
}

func ParseClusterDelegateMetaData(data []byte) (ClusterDelegateMetaData, error) {
	meta := ClusterDelegateMetaData{}

	err := json.Unmarshal(data, &meta)
	if err != nil {
		return ClusterDelegateMetaData{}, err
	}
	return meta, err
}
