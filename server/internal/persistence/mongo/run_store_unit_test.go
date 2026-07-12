package mongo

import (
	"testing"

	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
)

// These are pure unit tests for buildRunUpdateDoc; they do not require a
// running MongoDB and run unconditionally (TestMain skips schema setup when
// DEX_TEST_MONGO_URI is unset, which is fine here).

func intVal(i int64) p.Value {
	return p.Value{Type: p.ValueTypeInt, IntVal: &i}
}

func cm(id int64, val p.Value) p.ChannelMessage {
	return p.ChannelMessage{ID: id, Value: val}
}

func TestBuildRunUpdateDoc_ReplaceUnconsumedChannels_EscapesDotsInKeys(t *testing.T) {
	channel := "mypkg.MyChannel"
	update := &p.RunRowUpdate{
		ReplaceUnconsumedChannels: map[string][]p.ChannelMessage{
			channel: {cm(1, intVal(1)), cm(2, intVal(2))},
		},
	}

	doc := buildRunUpdateDoc(update)
	setFields, ok := doc["$set"].(bson.M)
	require.True(t, ok, "$set must be a bson.M")

	expectedPath := fieldUnconsumedChannelMessages + "." + escapeMongoKey(channel)
	rawPath := fieldUnconsumedChannelMessages + "." + channel

	_, hasEscaped := setFields[expectedPath]
	_, hasRaw := setFields[rawPath]

	assert.True(t, hasEscaped, "expected escaped path %q in $set, got keys: %v", expectedPath, setFieldKeys(setFields))
	assert.False(t, hasRaw, "raw dotted path %q must NOT appear in $set (Mongo would treat it as a nested path)", rawPath)
}


func setFieldKeys(m bson.M) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
