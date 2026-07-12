package engine

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/superdurable/dex/server/internal/persistence/mongo"
)

const testDBName = "dex_test_engine"

func TestMain(m *testing.M) {
	uri := os.Getenv("DEX_TEST_MONGO_URI")
	if uri == "" {
		os.Exit(m.Run())
	}

	if err := mongo.EnsureSchemaForDatabase(context.Background(), uri, testDBName); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize MongoDB schema: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}
