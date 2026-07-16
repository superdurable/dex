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

package uuid

import (
	"database/sql/driver"
	"fmt"

	"github.com/gofrs/uuid"
)

// UUID represents a 16-byte universally unique identifier
// this type is a wrapper around google/uuid with the following differences
//   - type is a byte slice instead of [16]byte so that it is compatible with some db drivers
//   - db serialization converts uuid to bytes as opposed to string
type UUID []byte

func MustNewUUID() UUID {
	newUuid, err := uuid.NewV7()
	if err != nil {
		panic(err)
	}

	return newUuid[:]
}

// MustParseUUID returns a UUID parsed from the given string representation
// returns nil if the input is empty string
// panics if the given input is malformed
func MustParseUUID(s string) UUID {
	parsed := uuid.FromStringOrNil(s)
	if parsed == uuid.Nil {
		panic("invalid UUID string: " + s)
	}

	return parsed[:]
}

// MustParsePtrUUID returns a UUID parsed from the given string representation
// returns nil if the input is empty string
// panics if the given input is malformed
func MustParsePtrUUID(s *string) UUID {
	if s == nil {
		return nil
	}
	return MustParseUUID(*s)
}

// ParseUUID decodes s into a UUID or returns an error.
func ParseUUID(s string) (UUID, error) {
	parsed := uuid.FromStringOrNil(s)
	if parsed == uuid.Nil {
		return nil, fmt.Errorf("invalid UUID string: %s", s)
	}

	return parsed[:], nil
}

// UUIDPtr simply returns a pointer for the given value type
func UUIDPtr(u UUID) *UUID {
	return &u
}

// String returns the 36 byte hexstring representation of this uuid
// return empty string if this uuid is nil
func (u UUID) String() string {
	if u == nil {
		return ""
	}

	parsed := uuid.FromBytesOrNil(u)
	if parsed == uuid.Nil {
		return ""
	}

	return parsed.String()
}

// Scan implements sql.Scanner interface to allow this type to be
// parsed transparently by database drivers
func (u *UUID) Scan(src interface{}) error {
	if src == nil {
		return nil
	}
	guuid := &uuid.UUID{}
	if err := guuid.Scan(src); err != nil {
		return err
	}
	*u = (*guuid)[:]
	return nil
}

// Value implements sql.Valuer so that UUIDs can be written to databases
// transparently. This method returns a byte slice representation of uuid
func (u UUID) Value() (driver.Value, error) {
	return []byte(u), nil
}
