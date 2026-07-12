package mongo

import (
	"context"
	"fmt"
	"os"
	"testing"
)

const testDBName = "dex_test_store"

func TestMain(m *testing.M) {
	uri := os.Getenv("DEX_TEST_MONGO_URI")
	if uri == "" {
		os.Exit(m.Run())
	}

	if err := EnsureSchemaForDatabase(context.Background(), uri, testDBName); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize MongoDB schema: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}
