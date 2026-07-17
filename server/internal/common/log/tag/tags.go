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

package tag

import (
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"
)

const LoggingCallAtKey = "logging-call-at"

// Tag is the interface for logging system
type Tag struct {
	// keep this field private
	field zap.Field
}

// Field returns a zap field
func (t *Tag) Field() zap.Field {
	return t.field
}

func newStringTag(key string, value string) Tag {
	return Tag{
		field: zap.String(key, value),
	}
}

func newInt64(key string, value int64) Tag {
	return Tag{
		field: zap.Int64(key, value),
	}
}

func newInt(key string, value int) Tag {
	return Tag{
		field: zap.Int(key, value),
	}
}

func newBoolTag(key string, value bool) Tag {
	return Tag{
		field: zap.Bool(key, value),
	}
}

func newTimeTag(key string, value time.Time) Tag {
	return Tag{
		field: zap.Time(key, value),
	}
}

func newObjectTag(key string, value interface{}) Tag {
	return Tag{
		field: zap.String(key, fmt.Sprintf("%v", value)),
	}
}

func newErrorTag(key string, value error) Tag {
	//NOTE zap already chosen "error" as key
	return Tag{
		field: zap.Error(value),
	}
}

// TAGS

func Error(err error) Tag {
	return newErrorTag("error", err)
}

func Service(sv string) Tag {
	return newStringTag("service", sv)
}

func Message(msg string) Tag {
	return newStringTag("message", msg)
}

func ProcessId(id string) Tag {
	return newStringTag("processId", id)
}

func ProcessType(pt string) Tag {
	return newStringTag("processType", pt)
}

func Namespace(ns string) Tag {
	return newStringTag("namespace", ns)
}

func ProcessExecutionId(id string) Tag {
	return newStringTag("processExecutionId", id)
}

func StateExecutionId(id string) Tag {
	return newStringTag("stateExecutionId", id)
}

func Shard(shardId int32) Tag {
	return newInt64("shard", int64(shardId))
}

func StatusCode(status int) Tag {
	return newInt("status", int(status))
}

func AnyToStr(v interface{}) string {
	return fmt.Sprintf("%v", v)
}

func Value(v interface{}) Tag {
	return newObjectTag("value", v)
}

func JsonValue(v interface{}) Tag {
	bs, err := json.Marshal(v)
	str := string(bs)
	if err != nil {
		str = "failed to marshal to json"
	}
	return newStringTag("jsonValue", str)
}

func UnixTimestamp(v int64) Tag {
	return newTimeTag("UnixTimestamp", time.Unix(v, 0))
}

func ID(v string) Tag {
	return newStringTag("ID", v)
}

func Key(v string) Tag {
	return newStringTag("Key", v)
}

func DefaultValue(v interface{}) Tag {
	return newObjectTag("default-value", v)
}

func ImmediateTaskType(v string) Tag {
	return newStringTag("ImmediateTaskType", v)
}
