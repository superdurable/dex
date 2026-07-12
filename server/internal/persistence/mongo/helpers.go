package mongo

import (
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

func toInt32(v interface{}) int32 {
	switch n := v.(type) {
	case int32:
		return n
	case int64:
		return int32(n)
	case float64:
		return int32(n)
	default:
		return 0
	}
}

func toInt64(v interface{}) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int32:
		return int64(n)
	case float64:
		return int64(n)
	default:
		return 0
	}
}

func toString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func toTime(v interface{}) time.Time {
	if t, ok := v.(time.Time); ok {
		return t
	}
	return time.Time{}
}

func extractBytes(v interface{}) []byte {
	switch b := v.(type) {
	case []byte:
		return b
	case bson.RawValue:
		return b.Value
	default:
		raw, err := bson.Marshal(bson.M{"d": v})
		if err != nil {
			return nil
		}
		var out struct {
			D []byte `bson:"d"`
		}
		if err := bson.Unmarshal(raw, &out); err != nil {
			return nil
		}
		return out.D
	}
}
