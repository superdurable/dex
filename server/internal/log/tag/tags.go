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
	"log/slog"

	"github.com/superdurable/dex/server/internal/errors"
)

// Tag is the interface for logging system
type Tag struct {
	// Add slog attribute for direct slog usage
	attr slog.Attr
	// In a special case, we need to put more attributes for a tag
	// Currently only used for error-at
	extraAttrs []slog.Attr
}

// Attr returns an slog attribute
func (t *Tag) Attr() slog.Attr {
	return t.attr
}

func (t *Tag) ExtraAttrs() []slog.Attr {
	return t.extraAttrs
}

func (t *Tag) setExtra(attrs []slog.Attr) {
	t.extraAttrs = attrs
}

type Integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64
}

// LoggingCallAtKey is reserved tag
const LoggingCallAtKey = "_log-at"
const ErrorCallAtKey = "_err-at"

// ============ Common Tags ============

// Error returns tag for Error, with support for CategorizedError full chain
func Error(err error) Tag {
	var errStr string
	var errCallAt *string
	// Get formated error for logging to include inner error strings when available
	if err == nil {
		errStr = "nil"
	} else {
		if catErr, ok := err.(errors.CategorizedError); ok {
			errStr = catErr.GetFullError()
			errCallAt = new(catErr.GetCallAtPath())
		} else {
			errStr = err.Error()
		}
	}

	t := Tag{attr: slog.String("error", errStr)}
	if errCallAt != nil {
		t.setExtra([]slog.Attr{slog.String(ErrorCallAtKey, *errCallAt)})
	}
	return t
}

func RunID(key string) Tag {
	return Tag{attr: slog.String("runId", key)}
}

func ShardId[T Integer](id T) Tag {
	return Tag{attr: slog.Int64("shard_id", int64(id))}
}

func TaskQueue(name string) Tag {
	return Tag{attr: slog.String("task_queue", name)}
}

func Address(address string) Tag {
	return Tag{attr: slog.String("address", address)}
}

func Source(source string) Tag {
	return Tag{attr: slog.String("source", source)}
}

func NodeName(name string) Tag {
	return Tag{attr: slog.String("node_name", name)}
}

func NumMembers[T Integer](n T) Tag {
	return Tag{attr: slog.Int64("num_members", int64(n))}
}

func MinMembers[T Integer](n T) Tag {
	return Tag{attr: slog.Int64("min_members", int64(n))}
}
