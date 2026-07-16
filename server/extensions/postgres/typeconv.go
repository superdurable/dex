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

package postgres

import "time"

var localZone, _ = time.Now().Zone()
var localOffset = getLocalOffset()

// here are the APIs for conversions to/from  go types to postgres datatypes
// TODO https://github.com/uber/cadence/issues/2892
// Why:
// 1. application layer is not consistent with timezone: for example,
// in some case we write timestamp with local timezone but when the time.Time
// is converted from "JSON"(from paging token), the timezone is missing
// 2. Postgres doesn't store any timezone info

// ToPostgresDateTime converts to time to Postgres datetime
func ToPostgresDateTime(t time.Time) time.Time {
	zn, _ := t.Zone()
	if zn != localZone {
		nano := t.UnixNano()
		t := time.Unix(0, nano)
		return t
	}
	return t
}

// FromPostgresDateTime converts postgres datetime and returns go time
func FromPostgresDateTime(t time.Time) time.Time {
	return t.Add(-localOffset)
}

func getLocalOffset() time.Duration {
	_, offsetSecs := time.Now().Zone()
	return time.Duration(offsetSecs) * time.Second
}
