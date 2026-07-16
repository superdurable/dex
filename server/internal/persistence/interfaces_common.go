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
