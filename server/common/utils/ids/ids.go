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

// TaskID is a server-generated UUID v7 for immediate/timer/opsfifo tasks,
// DLQ entries, and run-row timer references (heartbeat / durable).
type TaskID uuid.UUID

// BlobID is a server-generated UUID v7 referencing a row in BlobStore.
type BlobID uuid.UUID

func NewTaskID() TaskID {
	return TaskID(mustV7())
}

func NewBlobID() BlobID {
	return BlobID(mustV7())
}

// parseTaskID is unexported: TaskIDs are server-minted, so external code
// only ever needs NewTaskID or MustParseTaskID. Kept for JSON/BSON decode.
func parseTaskID(s string) (TaskID, error) {
	if s == "" {
		return TaskID(uuid.Nil), nil
	}
	u, err := uuid.Parse(s)
	return TaskID(u), err
}

// parseBlobID is unexported: BlobIDs are server-minted, so external code
// only ever needs NewBlobID or MustParseBlobID. Kept for JSON/BSON decode.
func parseBlobID(s string) (BlobID, error) {
	if s == "" {
		return BlobID(uuid.Nil), nil
	}
	u, err := uuid.Parse(s)
	return BlobID(u), err
}

// MustParseTaskID parses a server-generated TaskID and panics on failure.
// Use only for values from trusted sources (our own store rows), never for
// client-supplied input — a corrupt trusted value is a fail-fast bug.
func MustParseTaskID(s string) TaskID {
	id, err := parseTaskID(s)
	if err != nil {
		panic(fmt.Errorf("ids: parse task id %q: %w", s, err))
	}
	return id
}

// MustParseBlobID parses a server-generated BlobID and panics on failure.
// Use only for values from trusted sources (our own store rows), never for
// client-supplied input — a corrupt trusted value is a fail-fast bug.
func MustParseBlobID(s string) BlobID {
	id, err := parseBlobID(s)
	if err != nil {
		panic(fmt.Errorf("ids: parse blob id %q: %w", s, err))
	}
	return id
}

// ValidateBlobID parses a blob id supplied by an UNTRUSTED source (a client
// over an RPC) and returns an error for any malformed or empty input — wrap
// that error as errors.NewInvalidInputError at the handler. Never use
// MustParseBlobID on client input; a bad value must not crash the server.
//
// Unused today: the server mints every blob id and clients never send one.
// Provided so a future client-facing API (e.g. fetch-blob-by-id) validates
// instead of panicking. See CLAUDE.md / .cursor/rules/no-ignored-errors.mdc.
func ValidateBlobID(s string) (BlobID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return BlobID(uuid.Nil), err
	}
	return BlobID(u), nil
}

func (id TaskID) String() string {
	u := uuid.UUID(id)
	if u == uuid.Nil {
		return ""
	}
	return u.String()
}

func (id TaskID) IsZero() bool {
	return uuid.UUID(id) == uuid.Nil
}

func (id TaskID) UUID() uuid.UUID { return uuid.UUID(id) }

// Compare orders two TaskIDs by their raw UUID bytes. For canonical v7 UUIDs
// this matches both the Postgres UUID column order and the Mongo string order.
func (id TaskID) Compare(other TaskID) int {
	a, b := uuid.UUID(id), uuid.UUID(other)
	return bytes.Compare(a[:], b[:])
}

func (id BlobID) String() string {
	u := uuid.UUID(id)
	if u == uuid.Nil {
		return ""
	}
	return u.String()
}

func (id BlobID) IsZero() bool {
	return uuid.UUID(id) == uuid.Nil
}

func (id BlobID) UUID() uuid.UUID { return uuid.UUID(id) }

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

func (id *TaskID) Scan(src any) error {
	var u uuid.UUID
	if err := scanUUID(&u, src); err != nil {
		return err
	}
	*id = TaskID(u)
	return nil
}

func (id TaskID) Value() (driver.Value, error) {
	if id.IsZero() {
		return nil, nil
	}
	return uuid.UUID(id).String(), nil
}

func (id *BlobID) Scan(src any) error {
	var u uuid.UUID
	if err := scanUUID(&u, src); err != nil {
		return err
	}
	*id = BlobID(u)
	return nil
}

func (id BlobID) Value() (driver.Value, error) {
	if id.IsZero() {
		return nil, nil
	}
	return uuid.UUID(id).String(), nil
}

func (id TaskID) MarshalJSON() ([]byte, error) {
	return json.Marshal(id.String())
}

func (id *TaskID) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	parsed, err := parseTaskID(s)
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

func (id BlobID) MarshalJSON() ([]byte, error) {
	return json.Marshal(id.String())
}

func (id *BlobID) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	parsed, err := parseBlobID(s)
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

func (id TaskID) MarshalBSONValue() (bsontype.Type, []byte, error) {
	return bson.MarshalValue(id.String())
}

func (id *TaskID) UnmarshalBSONValue(t bsontype.Type, data []byte) error {
	var s string
	if err := bson.UnmarshalValue(t, data, &s); err != nil {
		return err
	}
	parsed, err := parseTaskID(s)
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

func (id BlobID) MarshalBSONValue() (bsontype.Type, []byte, error) {
	return bson.MarshalValue(id.String())
}

func (id *BlobID) UnmarshalBSONValue(t bsontype.Type, data []byte) error {
	var s string
	if err := bson.UnmarshalValue(t, data, &s); err != nil {
		return err
	}
	parsed, err := parseBlobID(s)
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}
