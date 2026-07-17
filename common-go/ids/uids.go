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

package ids

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsontype"
)

type UID uuid.UUID

func NewUID() UID {
	return UID(mustV7())
}

func EmptyUId() UID {
	return UID(uuid.Nil)
}

func parseUID(s string) (UID, error) {
	if s == "" {
		return UID(uuid.Nil), nil
	}
	u, err := uuid.Parse(s)
	return UID(u), err
}

func MustParseUID(s string) UID {
	id, err := parseUID(s)
	if err != nil {
		panic(fmt.Errorf("ids: parse uid %q: %w", s, err))
	}
	return id
}

func ValidateUID(s string) (UID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return UID(uuid.Nil), err
	}
	return UID(u), nil
}

func (id UID) String() string {
	u := uuid.UUID(id)
	if u == uuid.Nil {
		return ""
	}
	return u.String()
}

func (id UID) IsZero() bool {
	return uuid.UUID(id) == uuid.Nil
}

func (id UID) UUID() uuid.UUID { return uuid.UUID(id) }

// Compare orders two UIDs by their raw UUID bytes. For canonical v7 UUIDs
// this matches both the Postgres UUID column order and the Mongo string order.
func (id UID) Compare(other UID) int {
	a, b := uuid.UUID(id), uuid.UUID(other)
	return bytes.Compare(a[:], b[:])
}

func mustV7() uuid.UUID {
	u, err := uuid.NewV7()
	if err != nil {
		panic(fmt.Errorf("ids: generate v7 uuid: %w", err))
	}
	return u
}

func scanUUID(dst *uuid.UUID, src any) error {
	if src == nil {
		*dst = uuid.Nil
		return nil
	}
	switch v := src.(type) {
	case uuid.UUID:
		*dst = v
	case [16]byte:
		*dst = uuid.UUID(v)
	case []byte:
		if len(v) == 16 {
			copy(dst[:], v)
			return nil
		}
		u, err := uuid.ParseBytes(v)
		if err != nil {
			return err
		}
		*dst = u
	case string:
		u, err := uuid.Parse(v)
		if err != nil {
			return err
		}
		*dst = u
	default:
		return fmt.Errorf("ids: scan uuid from %T", src)
	}
	return nil
}

func (id *UID) Scan(src any) error {
	var u uuid.UUID
	if err := scanUUID(&u, src); err != nil {
		return err
	}
	*id = UID(u)
	return nil
}

func (id UID) Value() (driver.Value, error) {
	if id.IsZero() {
		return nil, nil
	}
	return uuid.UUID(id).String(), nil
}

func (id UID) MarshalJSON() ([]byte, error) {
	return json.Marshal(id.String())
}

func (id *UID) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	parsed, err := parseUID(s)
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

func (id UID) MarshalBSONValue() (bsontype.Type, []byte, error) {
	return bson.MarshalValue(id.String())
}

func (id *UID) UnmarshalBSONValue(t bsontype.Type, data []byte) error {
	var s string
	if err := bson.UnmarshalValue(t, data, &s); err != nil {
		return err
	}
	parsed, err := parseUID(s)
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}
