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

package persistence

import "common-go/ids"

type ValueType int32

const (
	ValueTypeInvalid          ValueType = 0
	ValueTypeBlobRefForString ValueType = 1
	ValueTypeBlobRefForObject ValueType = 2
	ValueTypeInt              ValueType = 3
	ValueTypeDouble           ValueType = 4
	ValueTypeBool             ValueType = 5
	ValueTypeNull             ValueType = 6
)

type Value struct {
	Type      ValueType `bson:"type"`
	IntVal    *int64    `bson:"int_val,omitempty"`
	DoubleVal *float64  `bson:"double_val,omitempty"`
	BoolVal   *bool     `bson:"bool_val,omitempty"`
	BlobID    ids.UID   `bson:"blob_id,omitempty"` // for ValueTypeBlobRef
}

type ChannelMessage struct {
	ID    int64 `bson:"id"`
	Value Value `bson:"value"`
}
